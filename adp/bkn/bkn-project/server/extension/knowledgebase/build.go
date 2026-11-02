// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const DocumentExtractionAgentID = "bkn-project-document-extraction"

var ErrInputNotExtractable = errors.New("file has no supported extractable content")

// DatasetWrite exists only in memory. Process storage retains its digest and
// completed count, never rows, source text, Agent output or credentials.
type DatasetWrite struct {
	Spec interfaces.DraftDatasetSpec
	Row  map[string]any
}

// FileInput is bounded, version-verified source content in memory only.
// Text is indexed by native block id. Its digest, never this value, is journaled.
type FileInput struct {
	Blocks []EvidenceBlock   `json:"blocks"`
	Media  []EvidenceMedia   `json:"media"`
	Text   map[string]string `json:"text"`
}

// BuildRuntime uses the dedicated project/build identity. Each source read and
// Check revalidates the delegator's current query_data permission. Start is a
// single submission; a lost acknowledgement must never cause an implicit retry.
// Materialize validates the entire file before returning any Dataset write.
type BuildRuntime interface {
	Version(context.Context, interfaces.AccountInfo) (int, error)
	Delegate(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) (interfaces.KBBuildIdentity, error)
	Hold(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error
	Check(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity, interfaces.KBResourceFile) error
	ReadFile(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity, int) (*FileInput, error)
	Start(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity, int, *interfaces.Draft, *FileInput) (*interfaces.BknAgentTask, error)
	Poll(context.Context, interfaces.KBBuildIdentity, string) (*interfaces.BknAgentTask, error)
	Materialize(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity, int, *interfaces.Draft, *FileInput, *interfaces.BknAgentTask) ([]DatasetWrite, error)
	// Core checkpoints these separately: completing cleanup disables the job
	// identity, so a restart must not try source cleanup after completion.
	Revoke(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error
	ReleaseHolds(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error
	Complete(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error
}
