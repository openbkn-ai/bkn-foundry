// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// How SyncBranch brought main into a branch.
const (
	// SYNC_RESULT_UP_TO_DATE: main was already an ancestor of the branch; nothing changed.
	SYNC_RESULT_UP_TO_DATE = "up_to_date"
	// SYNC_RESULT_FAST_FORWARD: the branch had no commits of its own and now points at main.
	SYNC_RESULT_FAST_FORWARD = "fast_forward"
	// SYNC_RESULT_MERGED: a merge commit with the branch and main as parents was written.
	SYNC_RESULT_MERGED = "merged"
)

type CreateLineageRequest struct {
	KNID string `json:"kn_id"`
}

type CreateLineageResult struct {
	LineageID string `json:"lineage_id"`
	Commit    string `json:"commit"`
}

type CreateBranchRequest struct {
	Name string `json:"name"`
	// From is the ref the branch starts at; main when empty.
	From string `json:"from"`
}

type CreateBranchResult struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type CommitDefinitionsResult struct {
	Commit string `json:"commit"`
	// Changed lists the bkn/ paths the commit added, changed or removed; all empty
	// when the upload matched the branch and no commit was made.
	Changed PathDiff `json:"changed"`
}

type SyncBranchResult struct {
	Result string `json:"result"`
	// Commit is the head of the branch after the sync.
	Commit string `json:"commit"`
	// Summary counts what the semantic merge took from each side; only for SYNC_RESULT_MERGED.
	Summary *MergeSummary `json:"summary,omitempty"`
}

type DriftRequest struct {
	KNID string `json:"kn_id"`
}

type DriftResult struct {
	*DriftReport
	// Commit is the new head of main when the live definitions were committed.
	Commit string `json:"commit,omitempty"`
}

// LineageService sets up release lineages and keeps project branches in step with main.
// Errors are *httperrors.HTTPError.
//
//go:generate mockgen -source ../interfaces/lineage_service.go -destination ../interfaces/mock/mock_lineage_service.go
type LineageService interface {
	// CreateLineage exports the live network knID with token and starts its lineage, whose id is knID.
	CreateLineage(ctx context.Context, token string, knID string, author GitAuthor) (*CreateLineageResult, error)
	// CreateBranch creates a project branch at the commit from resolves to.
	CreateBranch(ctx context.Context, lineageID string, name string, from string) (*CreateBranchResult, error)
	// CommitDefinitions replaces the definitions on a project branch with files.
	CommitDefinitions(ctx context.Context, lineageID string, branch string, files map[string][]byte,
		message string, author GitAuthor) (*CommitDefinitionsResult, error)
	// SyncBranch brings main into a project branch: no-op, fast-forward or semantic merge.
	SyncBranch(ctx context.Context, lineageID string, branch string, author GitAuthor) (*SyncBranchResult, error)
	// CheckDrift compares the live network knID with main; with commit it also commits the live set to main.
	CheckDrift(ctx context.Context, token string, lineageID string, knID string, commit bool, author GitAuthor) (*DriftResult, error)
}
