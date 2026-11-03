// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"errors"
	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"net"
	"slices"
	"time"
)

var ErrLostLease = errors.New("refresh process lease or revision changed")

const refreshLease = 5 * time.Minute
const refreshInterval = time.Minute
const refreshRetry = 30 * time.Second

func temporaryScanFailure(err error) bool {
	if errors.Is(err, ErrSourceChanged) || errors.Is(err, ErrSourceNotReady) || errors.Is(err, kb.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var source *kb.SourceError
	if errors.As(err, &source) {
		return source.StatusCode >= 500 || source.StatusCode == 429
	}
	var transport net.Error
	return errors.As(err, &transport) && transport.Timeout()
}

// Begin persists a complete batch intent and anonymous hold ids before any hold,
// Agent submission or Dataset mutation. A restart returns the existing intent;
// an uncertain save is not permission to repeat an inference or invent a batch.
func (s *Service) Begin(ctx context.Context, lineage, id, lease string) (*interfaces.KBRefreshRecord, error) {
	if s.Journal == nil || id == "" || lease == "" {
		return nil, ErrInvalidManifest
	}
	if s.Enabled != nil && !s.Enabled() {
		return nil, ErrInvalidManifest
	}
	now := time.Now().UnixMilli()
	if err := s.Journal.Ensure(ctx, lineage, now); err != nil {
		return nil, err
	}
	record, err := s.Journal.Claim(ctx, lineage, lease, now, now+refreshLease.Milliseconds())
	if err != nil || record == nil {
		return nil, err
	}
	current, err := Resolve(ctx, lineage, s.Releases, s.Publications)
	if err != nil {
		return nil, err
	}
	if s.ExpectedNetwork != "" && current.NetworkID != s.ExpectedNetwork {
		return nil, ErrNoPublishedTarget
	}
	if record.Pending != nil {
		if record.NetworkID != current.NetworkID || record.Pending.RuntimeDigest != current.RuntimeDigest || record.Pending.PublicationRevision != current.Revision {
			return nil, ErrNoPublishedTarget
		}
		return record, nil
	}
	// Dataset generations can be shared by versions, but unrelated generations
	// must keep their own actual manifest and retained evidence during rollback.
	previous := record.Previous
	holds, cursors, holdNetwork := record.Holds, record.Cursors, record.HoldNetworkID
	if holdNetwork == "" && len(holds) > 0 {
		holdNetwork = record.NetworkID
	}
	if previous != nil && previous.GenerationID != current.GenerationID {
		baseline := record.Generations[current.GenerationID]
		previous, holds, cursors, holdNetwork = baseline.Build, baseline.Holds, baseline.Cursors, baseline.HoldNetworkID
	}
	if previous != nil && (previous.ProjectID != current.ProjectID || previous.GenerationID != current.GenerationID || previous.ModelFingerprint != current.ModelFingerprint) {
		return nil, ErrInvalidManifest
	}

	scoped := *s
	scoped.guard = func(ctx context.Context) error {
		latest, err := s.Journal.Get(ctx, lineage)
		if err != nil {
			return err
		}
		if latest == nil || latest.Revision != record.Revision || latest.LeaseID != lease || latest.LeaseUntil <= time.Now().UnixMilli() {
			return ErrLostLease
		}
		return nil
	}
	prepared, err := scoped.Prepare(ctx, lineage, id, previous)
	if err != nil {
		if err == ErrLostLease {
			return nil, err
		}
		if temporaryScanFailure(err) {
			record.State, record.Reason = "idle", "source_unavailable"
			record.NextCheck = time.Now().Add(refreshRetry).UnixMilli()
			if saveErr := s.saveRecord(ctx, record); saveErr != nil {
				return nil, saveErr
			}
			return record, err
		}
		if saveErr := s.attention(ctx, record, "source_unavailable"); saveErr != nil {
			return nil, saveErr
		}
		return record, err
	}
	if record.Previous != nil && record.Previous.GenerationID != current.GenerationID {
		if record.Generations == nil {
			record.Generations = map[string]interfaces.KBRefreshBaseline{}
		}
		owner := record.HoldNetworkID
		if owner == "" && len(record.Holds) > 0 {
			owner = record.NetworkID
		}
		record.Generations[record.Previous.GenerationID] = interfaces.KBRefreshBaseline{Build: record.Previous, Holds: record.Holds, Cursors: record.Cursors, HoldNetworkID: owner}
	}
	delete(record.Generations, current.GenerationID)
	record.Holds, record.Cursors, record.HoldNetworkID = holds, cursors, holdNetwork
	record.Previous = prepared.Plan.Baseline
	record.NetworkID = current.NetworkID
	if len(prepared.Plan.Changed) == 0 && len(prepared.Plan.Removed) == 0 {
		prepared.Plan.Build.State = "succeeded"
		// An unchanged scan is not a new extraction. Keep the actual build
		// identity used by canonical fusion receipts and avoid no-op rewrites.
		prepared.Plan.Build.ID = prepared.Plan.Baseline.ID
		prepared.Plan.Build.CreateTime = prepared.Plan.Baseline.CreateTime
		record.Previous = prepared.Plan.Build
		record.Cursors = prepared.Cursors
		record.State, record.Reason = "idle", ""
		record.NextCheck = time.Now().Add(refreshInterval).UnixMilli()
	} else {
		groups := map[string][]string{}
		for _, step := range prepared.Plan.Build.Files {
			groups[step.File.KnowledgeBaseID] = append(groups[step.File.KnowledgeBaseID], step.File.ContentVersionID)
		}
		ids := []string{}
		for kb := range groups {
			ids = append(ids, kb)
		}
		slices.Sort(ids)
		holds := []interfaces.KBHoldGroup{}
		for _, kb := range ids {
			versions := groups[kb]
			slices.Sort(versions)
			versions = slices.Compact(versions)
			holds = append(holds, interfaces.KBHoldGroup{ID: uuid.NewString(), KnowledgeBaseID: kb, VersionIDs: versions})
		}
		estimate, err := EvaluateLimits(s.limits(), prepared.Plan.Baseline, prepared.Plan)
		if err != nil {
			return nil, err
		}
		if record.ReviewNextBatch {
			estimate.Reasons = append(estimate.Reasons, "previous_batch_discarded")
			record.ReviewNextBatch = false
		}
		if record.Previous == nil {
			record.Previous = prepared.Plan.Baseline
		}
		record.Pending = &interfaces.KBRefreshBatch{Estimate: estimate, Build: prepared.Plan.Build, Changed: prepared.Plan.Changed, Removed: prepared.Plan.Removed, Holds: holds, Cursors: prepared.Cursors, RuntimeDigest: current.RuntimeDigest, PublicationRevision: current.Revision}
		record.State = "running"
		if len(estimate.Reasons) > 0 {
			record.State, record.Reason = "attention", "batch_review_required"
		}
	}
	if err := prepared.Guard(ctx); err != nil {
		return nil, err
	}
	revision := record.Revision
	record.Revision++
	record.UpdateTime = max(time.Now().UnixMilli(), record.UpdateTime+1)
	until := record.UpdateTime + refreshLease.Milliseconds()
	saved, err := s.Journal.Save(ctx, record, revision, lease, record.UpdateTime, until)
	if err != nil {
		return nil, err
	}
	if !saved {
		return nil, ErrLostLease
	}
	record.LeaseUntil = until
	return record, nil
}
