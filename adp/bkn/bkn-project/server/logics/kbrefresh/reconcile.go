// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"time"
)

// Reconcile reuses acknowledged source rows after a complete unchanged scan.
// System-table changes and effective-date transitions use the existing K5 plan;
// this path neither submits inference nor re-acquires/releases source holds.
func (s *Service) Reconcile(ctx context.Context, record *interfaces.KBRefreshRecord, lease string) error {
	if record == nil || record.State != "idle" || record.Pending != nil || record.Previous == nil || record.LeaseID != lease {
		return ErrInvalidManifest
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	var candidate interfaces.KBRefreshRecord
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return err
	}
	candidate.LeaseID, candidate.LeaseUntil = record.LeaseID, record.LeaseUntil
	publication, err := Resolve(ctx, record.LineageID, s.Releases, s.Publications)
	if err != nil {
		return err
	}
	if publication.NetworkID != record.NetworkID {
		return ErrNoPublishedTarget
	}
	zero := int64(0)
	candidate.Pending = &interfaces.KBRefreshBatch{
		Build: candidate.Previous, Holds: candidate.Holds, Cursors: candidate.Cursors, ReuseHolds: true, EvaluationTime: time.Now().UnixMilli(),
		RuntimeDigest: publication.RuntimeDigest, PublicationRevision: publication.Revision,
		Changed: []int{}, Removed: []interfaces.KBResourceFile{},
		Estimate: &interfaces.KBRefreshEstimate{PreviousFiles: len(candidate.Previous.Files), DurationMillis: &zero, Reasons: []string{}},
	}
	staged, fusion, err := s.StageFusion(ctx, &candidate)
	if err != nil {
		return err
	}
	if record.Previous.Linking != nil && record.Previous.Linking.Signature == fusion.Preview.Signature {
		return staged.Preparation.Guard(ctx)
	}
	// Persist a frozen evaluation time before any live mutation. The same normal
	// apply/recovery path rechecks this plan and completes its durable intent.
	candidate.Pending.RowsDigest = staged.Digest
	candidate.State, candidate.Reason, candidate.NextCheck = "ready", "", 0
	return s.saveRecord(ctx, &candidate)
}
