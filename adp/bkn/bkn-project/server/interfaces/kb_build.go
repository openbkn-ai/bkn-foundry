// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

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
}

// KBLinkingReceipt stores process evidence only. Claims, proposed matches,
// conflicts and resulting current values remain in owned Vega Datasets.
type KBLinkingReceipt struct {
	FusionApplied bool   `json:"fusion_applied,omitempty"`
	FusionCurrent int    `json:"fusion_current,omitempty"`
	FusionPending int    `json:"fusion_pending,omitempty"`
	OperationID   string `json:"operation_id"`
	Signature     string `json:"signature"`
	State         string `json:"state"`
	RowsWritten   int64  `json:"rows_written"`
	Eligible      int64  `json:"eligible"`
	Linked        int64  `json:"linked"`
	Pending       int64  `json:"pending"`
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
	Assessment    *KBAcceptanceCounts `json:"assessment,omitempty"`
	File          KBResourceFile      `json:"file"`
	State         string              `json:"state"`
	AgentTaskID   string              `json:"agent_task_id,omitempty"`
	AgentVersion  *int                `json:"agent_version,omitempty"`
	PromptVersion *int                `json:"prompt_version,omitempty"`
	ModelID       string              `json:"model_id,omitempty"`
	RowsWritten   int64               `json:"rows_written"`
	// RowsDigest binds crash recovery to the complete, validated write set.
	RowsDigest  string `json:"rows_digest,omitempty"`
	InputDigest string `json:"input_digest,omitempty"`
	Reason      string `json:"reason,omitempty"`
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

type KBBuildKey struct{ ProjectID, ID string }

type KBBuildQueue interface {
	KBBuildAccess
	List(context.Context, string, int) ([]*KBBuild, error)
	Runnable(context.Context, int) ([]KBBuildKey, error)
	Cancel(context.Context, string, string, AccountInfo, int64) (bool, error)
}
