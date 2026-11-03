// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Status is an allowlisted business projection. Hold UUIDs, task ids, model
// fingerprints, source locators and raw model/instance bodies never reach it.
type Status struct {
	Available     bool                          `json:"available"`
	NetworkID     string                        `json:"network_id,omitempty"`
	State         string                        `json:"state"`
	Reason        string                        `json:"reason,omitempty"`
	Revision      int64                         `json:"revision"`
	CanManage     bool                          `json:"can_manage"`
	CanRelease    bool                          `json:"can_release"`
	CanDiscard    bool                          `json:"can_discard"`
	Files         int                           `json:"files"`
	Changed       int                           `json:"changed"`
	Removed       int                           `json:"removed"`
	Reused        int                           `json:"reused"`
	Completed     int                           `json:"completed"`
	PendingLinks  int64                         `json:"pending_links"`
	PendingFields int                           `json:"pending_fields"`
	PendingFacts  int                           `json:"pending_facts"`
	Estimate      *interfaces.KBRefreshEstimate `json:"estimate,omitempty"`
	UpdateTime    int64                         `json:"update_time"`
	NextCheck     int64                         `json:"next_check"`
	Limits        Limits                        `json:"limits"`
}

func (s *Service) Status(ctx context.Context, lineage string, canManage bool) (*Status, error) {
	publication, err := Resolve(ctx, lineage, s.Releases, s.Publications)
	if errors.Is(err, ErrNoPublishedTarget) {
		return &Status{State: "unavailable", Limits: s.limits()}, nil
	}
	if err != nil {
		return nil, err
	}
	if s.ExpectedNetwork != "" && publication.NetworkID != s.ExpectedNetwork {
		return nil, ErrNoPublishedTarget
	}
	result := &Status{Available: true, NetworkID: publication.NetworkID, State: "idle", CanManage: canManage, Limits: s.limits()}
	r, err := s.Journal.Get(ctx, lineage)
	if err != nil {
		return nil, err
	}
	var build *interfaces.KBBuild
	if r == nil {
		stored, err := s.Builds.Get(ctx, publication.ProjectID, publication.FullBuildID)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			build = stored.Build
		}
	} else {
		result.Revision, result.UpdateTime, result.NextCheck = r.Revision, r.UpdateTime, r.NextCheck
		if r.Pending != nil && r.NetworkID != publication.NetworkID {
			// Managing the current version does not expose a prior version's sources.
			result.State, result.Reason = "attention", "previous_version_update"
			if r.State == "discarding" {
				result.State, result.Reason = "discarding", "discard_requested"
			}
			result.CanDiscard = canManage && r.State == "attention" && r.Pending.ApplicationDigest == ""
			return result, nil
		}
		result.State, result.Reason = r.State, r.Reason
		build = r.Previous
		if r.Pending != nil {
			build = r.Pending.Build
			result.Changed = len(r.Pending.Changed)
			result.Estimate = r.Pending.Estimate
			if r.Pending.Estimate != nil {
				result.Removed = r.Pending.Estimate.RemovedFiles
			}
			result.CanRelease = canManage && r.State == "attention" && r.Reason == "batch_review_required"
			result.CanDiscard = canManage && r.State == "attention" && r.Pending.ApplicationDigest == ""
		}
	}
	if build != nil {
		result.Files = len(build.Files)
		result.Reused = max(0, result.Files-result.Changed)
		for _, file := range build.Files {
			if file.State == "succeeded" || file.State == "validated" {
				result.Completed++
			}
		}
		if build.Linking != nil {
			result.PendingLinks = build.Linking.Pending
			result.PendingFields = build.Linking.FusionPending
			result.PendingFacts = build.Linking.FactPending
		}
	}
	return result, nil
}
