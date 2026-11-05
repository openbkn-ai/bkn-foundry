// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package interfaces

import (
	"context"
	"fmt"
)

// KBRefreshRecord is a single lineage's durable refresh journal and write lease.
// A consumer switch cannot start a second writer against shared Datasets while
// its predecessor still owns this lease. Extracted values remain outside SQL.
type KBRefreshRecord struct {
	ReviewNextBatch bool                         `json:"review_next_batch,omitempty"`
	HoldNetworkID   string                       `json:"hold_network_id,omitempty"`
	Generations     map[string]KBRefreshBaseline `json:"generations,omitempty"`
	LineageID       string                       `json:"lineage_id"`
	NetworkID       string                       `json:"network_id"`
	Revision        int64                        `json:"revision"`
	State           string                       `json:"state"`
	Reason          string                       `json:"reason,omitempty"`
	Previous        *KBBuild                     `json:"previous,omitempty"`
	Pending         *KBRefreshBatch              `json:"pending,omitempty"`
	Holds           []KBHoldGroup                `json:"holds,omitempty"`
	Cursors         map[string]string            `json:"cursors,omitempty"`
	NextCheck       int64                        `json:"next_check"`
	UpdateTime      int64                        `json:"update_time"`
	ProgressTime    int64                        `json:"progress_time,omitempty"`
	ProgressMark    string                       `json:"progress_mark,omitempty"`
	LeaseID         string                       `json:"-"`
	LeaseUntil      int64                        `json:"-"`
}

// KBRefreshBaseline retains process receipts for an inactive Dataset generation.
// A consumer rollback must use that generation's last actual manifest and holds.
type KBRefreshBaseline struct {
	Build         *KBBuild          `json:"build"`
	Holds         []KBHoldGroup     `json:"holds,omitempty"`
	HoldNetworkID string            `json:"hold_network_id,omitempty"`
	Cursors       map[string]string `json:"cursors,omitempty"`
}

// KBRefreshBatch records execution receipts and source decisions only. Agent
// responses and materialized rows are reread/validated under their fixed scope.
type KBRefreshBatch struct {
	EvaluationTime      int64              `json:"evaluation_time,omitempty"`
	ReuseHolds          bool               `json:"reuse_holds,omitempty"`
	DiscardedBy         *AccountInfo       `json:"discarded_by,omitempty"`
	Estimate            *KBRefreshEstimate `json:"estimate,omitempty"`
	Approval            *KBRefreshApproval `json:"approval,omitempty"`
	ApplicationDigest   string             `json:"application_digest,omitempty"`
	FusionSignature     string             `json:"fusion_signature,omitempty"`
	WriteFailures       int                `json:"write_failures,omitempty"`
	Build               *KBBuild           `json:"build"`
	Changed             []int              `json:"changed"`
	Removed             []KBResourceFile   `json:"removed"`
	Holds               []KBHoldGroup      `json:"holds"`
	Cursors             map[string]string  `json:"cursors"`
	RuntimeDigest       string             `json:"runtime_digest"`
	PublicationRevision int64              `json:"publication_revision"`
	ApplyIndex          int                `json:"apply_index"`
	RowsDigest          string             `json:"rows_digest,omitempty"`
}

type KBRefreshAccess interface {
	Ensure(context.Context, string, int64) error
	Get(context.Context, string) (*KBRefreshRecord, error)
	Claim(context.Context, string, string, int64, int64) (*KBRefreshRecord, error)
	ClaimReview(context.Context, string, string, int64, int64, int64) (*KBRefreshRecord, error)
	Save(context.Context, *KBRefreshRecord, int64, string, int64, int64) (bool, error)
}

// Estimates and approvals are process decisions, never independent quality scores.
type KBRefreshEstimate struct {
	PreviousFiles  int      `json:"previous_files"`
	ChangedFiles   int      `json:"changed_files"`
	RemovedFiles   int      `json:"removed_files"`
	DurationMillis *int64   `json:"duration_millis,omitempty"`
	Reasons        []string `json:"reasons"`
}
type KBRefreshApproval struct {
	By    AccountInfo `json:"by"`
	Time  int64       `json:"time"`
	Scope string      `json:"scope"`
}

// MarkProgress moves ProgressTime when the state or the position within the batch
// changed. UpdateTime moves on every poll, so it cannot tell a stall from work.
func (r *KBRefreshRecord) MarkProgress(now int64) {
	mark := r.State
	if r.Pending != nil {
		mark += fmt.Sprintf("|%d", r.Pending.ApplyIndex)
		if r.Pending.Build != nil {
			mark += fmt.Sprintf("|%d", r.Pending.Build.progressMark())
		}
	}
	if mark != r.ProgressMark || r.ProgressTime == 0 {
		r.ProgressMark, r.ProgressTime = mark, now
	}
}
