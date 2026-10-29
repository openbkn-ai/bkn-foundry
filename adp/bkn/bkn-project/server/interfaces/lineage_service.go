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

// DefinitionError is a check of the BKN specification a set of definitions fails: where (the
// table, such as object_type:part, and the column), what (code) and why (message).
type DefinitionError struct {
	Table   string `json:"table"`
	Column  string `json:"column,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Why a project branch is not ready for definitions: it is not kept in BKN 3.0, and syncing would
// move it once main is (branch), or main has to be moved first by committing its drift (lineage).
const (
	DEFINITIONS_BRANCH_NOT_V3  = "branch_not_v3"
	DEFINITIONS_LINEAGE_NOT_V3 = "lineage_not_v3"
)

// DefinitionsPreflight is what adding definitions at some paths to a branch would be refused for,
// as read without committing: NotV3 when the branch is not kept in BKN 3.0, and the paths the branch
// holds that the definitions do not own.
type DefinitionsPreflight struct {
	NotV3    string   `json:"not_v3,omitempty"`
	Existing []string `json:"existing"`
	// ObjectTypeNames are the names of the object types the branch keeps beside the definitions,
	// sorted: an alias of an added object type may not be one of them.
	ObjectTypeNames []string `json:"object_type_names,omitempty"`
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

type PublishReleaseRequest struct {
	// Branch is the project branch to publish.
	Branch string `json:"branch"`
	// Version is X.Y.Z; the release is tagged v<version>.
	Version   string `json:"version"`
	ProjectID string `json:"project_id"`
	Notes     string `json:"notes"`
}

type PublishReleaseResult struct {
	Release   *Release `json:"release"`
	NetworkID string   `json:"network_id"`
	Tag       string   `json:"tag"`
	// Commit is the head of main after the release.
	Commit string `json:"commit"`
}

// SwitchRequest is the body of POST /lineages/{lineage_id}/switches.
type SwitchRequest struct {
	// ToNetworkID is a network of the lineage: one a release created, or the base network.
	ToNetworkID string `json:"to_network_id"`
}

// SwitchPlan is what switching the lineage to ToNetworkID would do to the
// schedules. Agents, MCP configurations, workshop, eval and permissions on the
// target are switched by their owners and are not listed.
type SwitchPlan struct {
	LineageID     string `json:"lineage_id"`
	FromNetworkID string `json:"from_network_id"`
	ToNetworkID   string `json:"to_network_id"`
	// Schedules are the active schedules of the network in use. A switch turns
	// each on on the target, where it runs as whoever switches, and off here.
	Schedules []*ActionSchedule `json:"schedules"`
}

// LineageService sets up release lineages, keeps project branches in step with
// main and publishes them. Errors are *httperrors.HTTPError.
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
	// AddDefinitions writes files into the definitions on a project branch, which must be kept in
	// BKN 3.0. owned are the paths an earlier write from the same source made: they may be
	// replaced, and those not in files are removed. Any other path the branch holds is not
	// replaced: it is refused, listed in the error.
	AddDefinitions(ctx context.Context, lineageID string, branch string, files map[string][]byte,
		owned []string, message string, author GitAuthor) (*CommitDefinitionsResult, error)
	// PreflightDefinitions reads what AddDefinitions at paths, owning owned, would be refused for on
	// the branch as it is now, by the same rules and without committing; the files' contents are
	// only checked when adding them.
	PreflightDefinitions(ctx context.Context, lineageID string, branch string, paths []string,
		owned []string) (*DefinitionsPreflight, error)
	// SyncBranch brings main into a project branch: no-op, fast-forward or semantic merge.
	SyncBranch(ctx context.Context, lineageID string, branch string, author GitAuthor) (*SyncBranchResult, error)
	// CheckDrift compares the live network knID with main; with commit it also commits the live set to main.
	CheckDrift(ctx context.Context, token string, lineageID string, knID string, commit bool, author GitAuthor) (*DriftResult, error)
	// PublishRelease imports a project branch with token as a new knowledge network and records the release.
	PublishRelease(ctx context.Context, token string, lineageID string, req PublishReleaseRequest,
		publisher AccountInfo, author GitAuthor) (*PublishReleaseResult, error)
	// PlanSwitch reports what switching the lineage to toNetworkID would do, without doing it.
	PlanSwitch(ctx context.Context, token string, lineageID string, toNetworkID string) (*SwitchPlan, error)
	// SwitchConsumers moves the lineage's schedules to req.ToNetworkID and records the switch. A
	// rollback is a switch back to an earlier network.
	SwitchConsumers(ctx context.Context, token string, lineageID string, req SwitchRequest,
		operator AccountInfo) (*Switch, error)
}
