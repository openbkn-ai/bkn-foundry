// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"
	"hash/fnv"
)

// KBBuild and KBFileBuild contain process metadata only. Extracted properties,
// source text, Agent outputs and user credentials must never be stored here.
type KBBuild struct {
	Linking           *KBLinkingReceipt   `json:"linking,omitempty"`
	ReferenceID       string              `json:"reference_id,omitempty"`
	ReferenceRevision int64               `json:"reference_revision,omitempty"`
	Assessment        *KBAcceptanceCounts `json:"assessment,omitempty"`
	ID                string              `json:"id"`
	ProjectID         string              `json:"project_id"`
	NetworkID         string              `json:"network_id"`
	Owner             AccountInfo         `json:"owner"`
	Mode              string              `json:"mode"`
	State             string              `json:"state"`
	Revision          int64               `json:"revision"`
	SelectionID       string              `json:"selection_id"`
	SelectionRevision int64               `json:"selection_revision"`
	ModelFingerprint  string              `json:"model_fingerprint"`
	GenerationID      string              `json:"generation_id"`
	DraftUpdateTime   int64               `json:"draft_update_time"`
	DefinitionCommit  string              `json:"definition_commit"`
	AgentVersion      int                 `json:"agent_version"`
	PromptVersion     *int                `json:"prompt_version,omitempty"`
	ModelID           string              `json:"model_id,omitempty"`
	Model             string              `json:"model,omitempty"`
	Files             []KBFileBuild       `json:"files"`
	CreateTime        int64               `json:"create_time"`
	UpdateTime        int64               `json:"update_time"`
	// ProgressTime is when the work last moved. UpdateTime moves on every poll,
	// so only this can tell operators how long a build has been stuck.
	ProgressTime int64  `json:"progress_time,omitempty"`
	ProgressMark uint64 `json:"progress_mark,omitempty"`
	// Skipped lists selected files that could not enter the build at all. They are
	// outside Files because nothing can be delegated or held for them.
	Skipped []KBFileBuild `json:"skipped,omitempty"`
	// Acceptance is a person's decision to release without the files it lists.
	Acceptance *KBFileAcceptance `json:"acceptance,omitempty"`
	// SubmitHoldUntil and SubmitFailures space out submissions during an outage.
	SubmitHoldUntil int64 `json:"submit_hold_until,omitempty"`
	SubmitFailures  int   `json:"submit_failures,omitempty"`
}

// KBFileAcceptance is who accepted leaving which files out of a release, and when.
type KBFileAcceptance struct {
	By    AccountInfo      `json:"by"`
	Time  int64            `json:"time"`
	Files []KBAcceptedFile `json:"files"`
}

// KBAcceptedFile is one file left out of a release and why.
type KBAcceptedFile struct {
	File   KBResourceFile `json:"file"`
	Reason string         `json:"reason"`
}

// KBFileExcluded is the state of a file a person accepted leaving out.
const KBFileExcluded = "excluded"

// Completed reports a file with nothing left to do before release: extracted,
// evidence only (nothing to extract), or left out by a person's decision.
func (f *KBFileBuild) Completed() bool {
	return f.State == "succeeded" || f.State == "evidence_only" || f.State == KBFileExcluded
}

// HasClaims reports a file whose rows include extracted facts for linking.
func (f *KBFileBuild) HasClaims() bool {
	return f.State != "evidence_only" && f.State != KBFileExcluded
}

// MarkProgress moves ProgressTime when the state, a file's state or a written
// row count changed since the last call.
func (b *KBBuild) MarkProgress(now int64) {
	if mark := b.progressMark(); mark != b.ProgressMark || b.ProgressTime == 0 {
		b.ProgressMark, b.ProgressTime = mark, now
	}
}

func (b *KBBuild) progressMark() uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(b.State))
	for i := range b.Files {
		_, _ = fmt.Fprintf(hash, "|%s/%d", b.Files[i].State, b.Files[i].RowsWritten)
	}
	return hash.Sum64()
}

// KBWorkItem is one unfinished piece of background work: no names or identities.
type KBWorkItem struct {
	State        string
	ProgressTime int64
}

// KBWorkItems reports unfinished background work for operators.
type KBWorkItems interface {
	WorkItems(context.Context) ([]KBWorkItem, error)
}

// KBLinkingReceipt stores process evidence only. Claims, proposed matches,
// conflicts and resulting current values remain in owned Vega Datasets.
type KBLinkingReceipt struct {
	FactCurrent    int    `json:"fact_current,omitempty"`
	FactSuperseded int    `json:"fact_superseded,omitempty"`
	FactPending    int    `json:"fact_pending,omitempty"`
	FusionApplied  bool   `json:"fusion_applied,omitempty"`
	FusionCurrent  int    `json:"fusion_current,omitempty"`
	FusionPending  int    `json:"fusion_pending,omitempty"`
	OperationID    string `json:"operation_id"`
	Signature      string `json:"signature"`
	State          string `json:"state"`
	RowsWritten    int64  `json:"rows_written"`
	Eligible       int64  `json:"eligible"`
	Linked         int64  `json:"linked"`
	Pending        int64  `json:"pending"`
}

// KBLinkingAccess is an interactive continuation after background extraction.
// The caller supplies current operator permissions; no user token is persisted
// or borrowed by the knowledge-base-only background identity.
type KBLinkingAccess interface {
	ClaimLinking(context.Context, string, string, AccountInfo, string, string, int64, int64) (*KBBuildRecord, error)
	// CheckLinking renews a still-live lease after checking its current scope.
	CheckLinking(context.Context, string, string, string, int64, int64) (bool, error)
	SaveLinking(context.Context, *KBBuildRecord, int64, int64) (bool, error)
}

type KBFileBuild struct {
	Model          string              `json:"model,omitempty"`
	DurationMillis *int64              `json:"duration_millis,omitempty"`
	Assessment     *KBAcceptanceCounts `json:"assessment,omitempty"`
	File           KBResourceFile      `json:"file"`
	State          string              `json:"state"`
	AgentTaskID    string              `json:"agent_task_id,omitempty"`
	AgentVersion   *int                `json:"agent_version,omitempty"`
	PromptVersion  *int                `json:"prompt_version,omitempty"`
	ModelID        string              `json:"model_id,omitempty"`
	RowsWritten    int64               `json:"rows_written"`
	// RowsDigest binds crash recovery to the complete, validated write set.
	RowsDigest  string `json:"rows_digest,omitempty"`
	InputDigest string `json:"input_digest,omitempty"`
	Reason      string `json:"reason,omitempty"`
	// SubmittedAt starts the Agent task's deadline.
	SubmittedAt int64 `json:"submitted_at,omitempty"`
	// Attempts counts retries of known failures; NextAttempt is when the next may run.
	Attempts    int   `json:"attempts,omitempty"`
	NextAttempt int64 `json:"next_attempt,omitempty"`
	// ReusedFrom is the earlier build whose completed result this file carries.
	ReusedFrom string `json:"reused_from,omitempty"`
	// Cleared means a failed file's rows from any earlier attempt were removed.
	Cleared bool `json:"cleared,omitempty"`
}

// KBBuildIdentity is private delegation metadata, never part of a business DTO,
// Agent prompt or credential store. Safe validates it on every source read.
type KBBuildIdentity struct {
	GrantID     string
	PrincipalID string
	Revision    uint64
	// Outcome survives a restart during grant/hold cleanup.
	Outcome      string
	Cleaned      bool
	CleanupPhase string
}

// KBBuildRecord is private process storage. A handler returns Build only;
// delegation and lease metadata never enter business responses or Agent inputs.
type KBBuildRecord struct {
	Build      *KBBuild        `json:"-"`
	InputHash  string          `json:"-"`
	Identity   KBBuildIdentity `json:"-"`
	LeaseID    string          `json:"-"`
	LeaseUntil int64           `json:"-"`
}

// KBBuildAccess serializes process changes under project/source/draft guards.
// The coordinator never holds database locks across source, Agent or Vega calls.
type KBBuildAccess interface {
	Create(context.Context, *KBBuildRecord) (bool, error)
	Get(context.Context, string, string) (*KBBuildRecord, error)
	Claim(context.Context, string, string, string, int64, int64) (*KBBuildRecord, error)
	Save(context.Context, *KBBuildRecord, int64, int64) (bool, error)
}

// KBFileAcceptanceAccess applies a person's acceptance to a build that has ended,
// at the revision they were shown, under the current model and owner.
type KBFileAcceptanceAccess interface {
	AcceptFiles(ctx context.Context, project, id string, revision int64, apply func(*KBBuild) error) (*KBBuild, error)
}

type KBBuildKey struct{ ProjectID, ID string }

type KBBuildQueue interface {
	KBBuildAccess
	List(context.Context, string, int) ([]*KBBuild, error)
	Runnable(context.Context, int) ([]KBBuildKey, error)
	Cancel(context.Context, string, string, AccountInfo, int64) (bool, error)
}
