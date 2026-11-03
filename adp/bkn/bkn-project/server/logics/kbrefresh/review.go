// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"time"
)

func approvalScope(r *interfaces.KBRefreshRecord) (string, error) {
	if r == nil || r.Pending == nil || r.Pending.Build == nil {
		return "", ErrInvalidManifest
	}
	build := r.Pending.Build
	raw, err := json.Marshal(struct {
		Network, Runtime, ID, Model, ModelID, ModelName string
		Agent                                           int
		Prompt                                          *int
		Files                                           []interfaces.KBResourceFile
		Removed                                         []interfaces.KBResourceFile
	}{Network: r.NetworkID, Runtime: r.Pending.RuntimeDigest, ID: build.ID, Model: build.ModelFingerprint, ModelID: build.ModelID, ModelName: build.Model, Agent: build.AgentVersion, Prompt: build.PromptVersion, Files: refreshFiles(build), Removed: r.Pending.Removed})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func refreshFiles(build *interfaces.KBBuild) []interfaces.KBResourceFile {
	files := []interfaces.KBResourceFile{}
	for _, step := range build.Files {
		files = append(files, step.File)
	}
	return files
}

// ReleaseBatch records only a soft-gate exception for this exact source/model
// intent. The transport must first verify the caller's network modify right.
// Invalid evidence, uncertain inference or lost source access cannot use it.
func (s *Service) ReleaseBatch(ctx context.Context, lineage, lease string, revision int64, caller interfaces.AccountInfo) error {
	if s.Journal == nil || caller.ID == "" || caller.Type != "user" {
		return ErrInvalidManifest
	}
	now := time.Now().UnixMilli()
	r, err := s.Journal.ClaimReview(ctx, lineage, lease, revision, now, now+refreshLease.Milliseconds())
	if err != nil {
		return err
	}
	if r == nil || r.State != "attention" || r.Reason != "batch_review_required" || r.Pending == nil {
		return ErrInvalidManifest
	}
	prepared, err := s.resume(ctx, r)
	if err != nil {
		return err
	}
	if err := prepared.Guard(ctx); err != nil {
		return err
	}
	scope, err := approvalScope(r)
	if err != nil {
		return err
	}
	r.Pending.Approval = &interfaces.KBRefreshApproval{By: caller, Time: now, Scope: scope}
	r.State, r.Reason, r.NextCheck = "running", "", 0
	return s.saveRecord(ctx, r)
}

func approvedBatch(r *interfaces.KBRefreshRecord) bool {
	if r.Pending == nil || r.Pending.Estimate == nil {
		return false
	}
	if len(r.Pending.Estimate.Reasons) == 0 {
		return true
	}
	approval := r.Pending.Approval
	if approval == nil || approval.By.ID == "" || approval.By.Type != "user" || approval.Time <= 0 {
		return false
	}
	scope, err := approvalScope(r)
	return err == nil && scope == approval.Scope
}
