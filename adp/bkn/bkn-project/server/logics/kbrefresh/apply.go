// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
	"slices"
	"strings"
	"time"
)

type fileRetirement struct {
	Spec  interfaces.DraftDatasetSpec
	Scope kb.DatasetFileScope
	Keep  []string
}
type application struct {
	staged  *StagedBatch
	fusion  *kbinstance.PublishedFusionPlan
	writes  []kb.DatasetWrite
	retired []fileRetirement
	digest  string
}

func (s *Service) application(ctx context.Context, r *interfaces.KBRefreshRecord) (*application, error) {
	staged, fusion, err := s.StageFusion(ctx, r)
	if err != nil {
		return nil, err
	}
	byID := map[string]kb.DatasetWrite{}
	// Final fact/link decisions intentionally replace their staged initial row.
	// Both are validated against exactly the same prepared physical generation.
	for _, group := range [][]kb.DatasetWrite{staged.Writes, fusion.Writes, fusion.FusionWrites} {
		for _, write := range group {
			id, ok := write.Row["_id"].(string)
			if !ok || id == "" {
				return nil, ErrInvalidManifest
			}
			byID[write.Spec.ResourceID+"/"+id] = write
		}
	}
	writes := []kb.DatasetWrite{}
	for _, write := range byID {
		writes = append(writes, write)
	}
	writeDigest, err := kbinstance.ValidateWriteSet(r.Pending.Build, staged.Preparation.Model, writes)
	if err != nil {
		return nil, err
	}
	allKept := append(slices.Clone(writes), fusion.Preserved...)
	retired := []fileRetirement{}
	addFile := func(file interfaces.KBResourceFile, keep bool) error {
		document, err := kbinstance.DocumentID(file.KnowledgeBaseID, file.FileID)
		if err != nil {
			return err
		}
		scope := kb.DatasetFileScope{KnowledgeBaseID: file.KnowledgeBaseID, FileID: file.FileID, ContentVersionID: file.ContentVersionID, DocumentID: document, ModelFingerprint: r.Pending.Build.ModelFingerprint}
		for _, plan := range staged.Preparation.Model.Items.DatasetPlans {
			if plan == nil || plan.ID != r.Pending.Build.GenerationID {
				continue
			}
			for _, spec := range plan.Datasets {
				entity := false
				for _, object := range staged.Preparation.Model.Items.ObjectTypes {
					if object != nil && object.ID == spec.ObjectTypeID && object.Document != nil && object.Document.Role == "entity" {
						entity = true
					}
				}
				if entity {
					continue
				}
				ids := []string{}
				if keep {
					for _, write := range allKept {
						if write.Spec.ResourceID == spec.ResourceID && rowInFileScope(spec, write.Row, scope) {
							id, ok := write.Row["_id"].(string)
							if !ok {
								return ErrInvalidManifest
							}
							ids = append(ids, id)
						}
					}
				}
				slices.Sort(ids)
				ids = slices.Compact(ids)
				retired = append(retired, fileRetirement{Spec: spec, Scope: scope, Keep: ids})
			}
		}
		return nil
	}
	for _, index := range r.Pending.Changed {
		if err := addFile(r.Pending.Build.Files[index].File, true); err != nil {
			return nil, err
		}
	}
	for _, file := range r.Pending.Removed {
		for _, step := range r.Pending.Build.Files {
			if step.File.Key() == file.Key() && step.File.ContentVersionID == file.ContentVersionID {
				return nil, ErrInvalidManifest
			}
		}
		if err := addFile(file, false); err != nil {
			return nil, err
		}
	}
	for _, scope := range fusion.Scopes {
		retired = append(retired, fileRetirement{Spec: scope.Spec, Scope: scope.File, Keep: scope.Keep})
	}
	slices.SortFunc(retired, func(a, b fileRetirement) int {
		return strings.Compare(a.Spec.ResourceID+"/"+a.Scope.DocumentID+"/"+a.Scope.ContentVersionID, b.Spec.ResourceID+"/"+b.Scope.DocumentID+"/"+b.Scope.ContentVersionID)
	})
	raw, err := json.Marshal(struct {
		Rows, Source, Fusion string
		Retired              []fileRetirement
		Entities             any
	}{writeDigest, staged.Digest, fusion.Preview.Signature, retired, fusion.Entities})
	if err != nil || len(raw) > 32<<20 {
		return nil, ErrInvalidManifest
	}
	sum := sha256.Sum256(raw)
	return &application{staged: staged, fusion: fusion, writes: writes, retired: retired, digest: hex.EncodeToString(sum[:])}, nil
}

// BeginApply is a compare-and-set of a fully checked plan, not an authorization
// endpoint. Its caller must first apply the automatic soft gates or authorize
// the owner's explicit release of an exceptional batch. No live data is written.
func (s *Service) BeginApply(ctx context.Context, lineage, lease, sourceDigest, fusionSignature string) error {
	if s.Journal == nil {
		return kb.ErrUnavailable
	}
	now := time.Now().UnixMilli()
	r, err := s.Journal.Claim(ctx, lineage, lease, now, now+refreshLease.Milliseconds())
	if err != nil {
		return err
	}
	if r == nil || r.Pending == nil || r.State != "ready" {
		return ErrLostLease
	}
	if !approvedBatch(r) {
		return ErrInvalidManifest
	}
	plan, err := s.application(ctx, r)
	if err != nil {
		return err
	}
	if plan.staged.Digest != sourceDigest || plan.fusion.Preview.Signature != fusionSignature {
		return ErrInvalidManifest
	}
	commit := func() error {
		// Serialize only the final guard and durable intent with consumer switches.
		// Remote extraction and Dataset writes never hold the Git lock.
		if err := plan.staged.Preparation.Guard(ctx); err != nil {
			return err
		}
		r.Pending.ApplicationDigest = plan.digest
		r.Pending.FusionSignature = fusionSignature
		r.Pending.ApplyIndex = 0
		r.State, r.Reason = "applying", ""
		return s.saveRecord(ctx, r)
	}
	if s.Git != nil {
		return s.Git.WithLineageLock(ctx, lineage, func(interfaces.GitStore) error { return commit() })
	}
	return commit()
}

// ApplyStep resumes only an already gated, immutable application intent. A
// bounded number of stable row replacements/prunes are checkpointed per step.
// Unknown write acknowledgements replay the same plan, never another inference.
func (s *Service) ApplyStep(ctx context.Context, lineage, lease string) error {
	if s.Journal == nil {
		return kb.ErrUnavailable
	}
	now := time.Now().UnixMilli()
	r, err := s.Journal.Claim(ctx, lineage, lease, now, now+refreshLease.Milliseconds())
	if err != nil || r == nil {
		return err
	}
	if r.State == "finishing" {
		return s.finish(ctx, r)
	}
	if r.State != "applying" {
		return nil
	}
	plan, err := s.application(ctx, r)
	if err != nil {
		return s.attention(ctx, r, "write_plan_unavailable")
	}
	if plan.digest != r.Pending.ApplicationDigest || plan.fusion.Preview.Signature != r.Pending.FusionSignature {
		return s.attention(ctx, r, "write_plan_changed")
	}
	data, ok := s.Storage.(refreshDatasets)
	if !ok {
		return s.attention(ctx, r, "storage_unavailable")
	}
	count := len(plan.writes) + len(plan.retired) + len(plan.fusion.Entities)
	if r.Pending.ApplyIndex < 0 || r.Pending.ApplyIndex > count {
		return s.attention(ctx, r, "write_plan_changed")
	}
	for advanced := 0; r.Pending.ApplyIndex < count && advanced < 16; advanced++ {
		if err := plan.staged.Preparation.Guard(ctx); err != nil {
			return s.attention(ctx, r, "source_access_changed")
		}
		index := r.Pending.ApplyIndex
		switch {
		case index < len(plan.writes):
			write := plan.writes[index]
			err = data.Replace(ctx, write.Spec, write.Row)
		case index < len(plan.writes)+len(plan.retired):
			scope := plan.retired[index-len(plan.writes)]
			err = data.Prune(ctx, scope.Spec, scope.Scope, scope.Keep, plan.staged.Preparation.Guard)
		default:
			scope := plan.fusion.Entities[index-len(plan.writes)-len(plan.retired)]
			err = data.PruneEntities(ctx, scope.Spec, scope.Model, scope.Keep, plan.staged.Preparation.Guard)
		}
		if err != nil {
			r.Pending.WriteFailures++
			r.Reason = "dataset_write_unavailable"
			r.NextCheck = time.Now().Add(refreshRetry).UnixMilli()
			if saveErr := s.saveRecord(ctx, r); saveErr != nil {
				return saveErr
			}
			return kb.ErrUnavailable
		}
		r.Pending.ApplyIndex++
		r.Pending.WriteFailures = 0
		r.Reason = ""
		if err := s.saveRecord(ctx, r); err != nil {
			return err
		}
	}
	if r.Pending.ApplyIndex < count {
		return nil
	}
	build := r.Pending.Build
	for _, index := range r.Pending.Changed {
		build.Files[index].State = "succeeded"
		build.Files[index].RowsWritten = int64(len(plan.staged.Files[index]))
	}
	receipt := &interfaces.KBLinkingReceipt{OperationID: build.ID, Signature: plan.fusion.Preview.Signature, State: "succeeded", RowsWritten: int64(len(plan.fusion.Writes) + len(plan.fusion.FusionWrites)), Eligible: plan.fusion.Preview.Eligible, Linked: plan.fusion.Preview.Linked, Pending: plan.fusion.Preview.Pending, FusionApplied: true}
	for _, summary := range plan.fusion.Preview.Fusion {
		receipt.FusionCurrent += summary.Current
		receipt.FusionPending += summary.Pending
	}
	for _, summary := range plan.fusion.Preview.Facts {
		receipt.FactCurrent += summary.Current
		receipt.FactSuperseded += summary.Superseded
		receipt.FactPending += summary.Pending
	}
	build.Linking = receipt
	build.State = "succeeded"
	r.State = "finishing"
	return s.saveRecord(ctx, r)
}

func (s *Service) finish(ctx context.Context, r *interfaces.KBRefreshRecord) error {
	prepared, err := s.resume(ctx, r)
	if err != nil {
		return s.attention(ctx, r, "source_or_publication_changed")
	}
	for i, hold := range r.Holds {
		if r.Pending.ReuseHolds {
			break
		}
		if hold.Released {
			continue
		}
		if err := prepared.Guard(ctx); err != nil {
			return s.attention(ctx, r, "source_access_changed")
		}
		owner := r.HoldNetworkID
		if owner == "" {
			owner = r.NetworkID
		}
		if owner == r.NetworkID {
			err = prepared.Source.ReleaseHold(ctx, kb.SourceScope{NetworkID: owner, KnowledgeBaseID: hold.KnowledgeBaseID}, hold.ID)
		} else {
			cleanup, ok := s.Sources.(kb.PublishedHoldCleanup)
			if !ok {
				return s.attention(ctx, r, "hold_release_unavailable")
			}
			prior, lookupErr := s.Publications.Get(ctx, owner)
			if lookupErr != nil || prior == nil || prior.NetworkID != owner || prior.LineageID != r.LineageID || prior.GenerationID != r.Pending.Build.GenerationID {
				return s.attention(ctx, r, "hold_release_unavailable")
			}
			err = cleanup.ReleasePublishedHold(ctx, prior, hold, prepared.Guard)
		}
		if err != nil {
			return s.attention(ctx, r, "hold_release_unavailable")
		}
		r.Holds[i].Released = true
		return s.saveRecord(ctx, r)
	}
	r.Previous = r.Pending.Build
	r.Cursors = r.Pending.Cursors
	r.Holds = r.Pending.Holds
	if !r.Pending.ReuseHolds {
		r.HoldNetworkID = r.NetworkID
	}
	r.Pending = nil
	r.State, r.Reason = "idle", ""
	r.NextCheck = time.Now().Add(refreshInterval).UnixMilli()
	return s.saveRecord(ctx, r)
}

// Native document resources use the KB/file pair; occurrence, claim and
// business resources use document_id. This matches the Dataset adapter scope.
func rowInFileScope(spec interfaces.DraftDatasetSpec, row map[string]any, scope kb.DatasetFileScope) bool {
	if row["model_fingerprint"] != scope.ModelFingerprint || row["content_version_id"] != scope.ContentVersionID {
		return false
	}
	if spec.Layout.Mapping["document_id"] != "" {
		return row["document_id"] == scope.DocumentID
	}
	return row["knowledge_base_id"] == scope.KnowledgeBaseID && row["file_id"] == scope.FileID
}
