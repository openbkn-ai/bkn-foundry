package iprojectionsource

import (
	"context"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type Query struct {
	Scope          evidencevo.QueryScope
	From           time.Time
	To             time.Time
	BusinessDomain string
	Status         string
	RequestID      string
	TraceID        string
	InteractionID  string
	// AuthorizedInteractionIDs is an internal handoff from the Core projection.
	// It carries an Interaction-level authorization decision to its artifact reader.
	AuthorizedInteractionIDs []string
	Limit                    int
}

type Result struct {
	Traces    []evidencevo.NormalizedTrace
	Artifacts []evidencevo.EvidenceArtifact
	Truncated bool
}

type ProjectionSourcePort interface {
	LoadExecutionProjection(ctx context.Context, query Query) (Result, error)
}
