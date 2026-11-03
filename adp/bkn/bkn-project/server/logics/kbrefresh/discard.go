// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"time"
)

// DiscardBatch is called only after the transport authorizes network modify.
// A started application cannot be dropped: even its first write might already
// have completed with a lost acknowledgement. It must resume the same intent.
func (s *Service) DiscardBatch(ctx context.Context, lineage, lease string, revision int64, caller interfaces.AccountInfo) error {
	if s.Journal == nil || caller.ID == "" || caller.Type != "user" {
		return ErrInvalidManifest
	}
	now := time.Now().UnixMilli()
	r, err := s.Journal.ClaimReview(ctx, lineage, lease, revision, now, now+refreshLease.Milliseconds())
	if err != nil {
		return err
	}
	if r == nil || r.Pending == nil || r.Pending.Build == nil || r.Pending.ApplicationDigest != "" {
		return ErrInvalidManifest
	}
	r.Pending.DiscardedBy = &caller
	r.State, r.Reason, r.NextCheck = "discarding", "discard_requested", 0
	return s.saveRecord(ctx, r)
}
func (s *Service) DiscardStep(ctx context.Context, lineage, lease string) error {
	now := time.Now().UnixMilli()
	r, err := s.Journal.Claim(ctx, lineage, lease, now, now+refreshLease.Milliseconds())
	if err != nil || r == nil {
		return err
	}
	if r.State != "discarding" {
		return nil
	}
	if r.Pending == nil || r.Pending.Build == nil || r.Pending.ApplicationDigest != "" {
		return ErrInvalidManifest
	}
	publication, err := s.Publications.Get(ctx, r.NetworkID)
	if err != nil {
		return err
	}
	if publication == nil || publication.LineageID != lineage || publication.RuntimeDigest != r.Pending.RuntimeDigest || publication.ModelFingerprint != r.Pending.Build.ModelFingerprint || publication.GenerationID != r.Pending.Build.GenerationID {
		return ErrInvalidManifest
	}
	cleanup, ok := s.Sources.(kb.PublishedHoldCleanup)
	if !ok {
		return kb.ErrUnavailable
	}
	guard := func(ctx context.Context) error {
		latest, err := s.Journal.Get(ctx, lineage)
		if err != nil {
			return err
		}
		if latest == nil || latest.Revision != r.Revision || latest.State != "discarding" || latest.LeaseID != lease || latest.LeaseUntil <= time.Now().UnixMilli() {
			return ErrLostLease
		}
		return ctx.Err()
	}
	for index, hold := range r.Pending.Holds {
		if r.Pending.ReuseHolds {
			break
		}
		if hold.Released {
			continue
		}
		if err := cleanup.ReleasePublishedHold(ctx, publication, hold, guard); err != nil {
			return err
		}
		r.Pending.Holds[index].Released = true
		return s.saveRecord(ctx, r)
	}
	r.Pending = nil
	r.State, r.Reason, r.NextCheck = "idle", "", 0
	return s.saveRecord(ctx, r)
}

// Check queues a fresh read only when no inference/application intent is being
// abandoned. Retrying source discovery never repeats a submitted model call.
func (s *Service) Check(ctx context.Context, lineage, id, lease string) error {
	r, err := s.Journal.Get(ctx, lineage)
	if err != nil {
		return err
	}
	if r != nil && r.State == "attention" && r.Pending == nil {
		now := time.Now().UnixMilli()
		r, err = s.Journal.ClaimReview(ctx, lineage, lease, r.Revision, now, now+refreshLease.Milliseconds())
		if err != nil {
			return err
		}
		if r == nil {
			return ErrLostLease
		}
		r.State, r.Reason, r.NextCheck = "idle", "", 0
		if err := s.saveRecord(ctx, r); err != nil {
			return err
		}
	}
	_, err = s.Begin(ctx, lineage, id, lease)
	return err
}
