package iartifactstore

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

var ErrArtifactIDConflict = errors.New("BKN_TRACE_ARTIFACT_ID_CONFLICT")

const MaxArtifactQueryLimit = 1000

type QueryOptions struct {
	Scope evidencevo.QueryScope
	Limit int
}

type QueryResult struct {
	Entries   []evidencevo.EvidenceArtifact
	Truncated bool
}

type ArtifactStorePort interface {
	StoreArtifact(ctx context.Context, artifact evidencevo.EvidenceArtifact) (bool, error)
	GetArtifact(ctx context.Context, artifactID string, scope evidencevo.QueryScope) (evidencevo.EvidenceArtifact, bool, error)
	ListArtifactsByRequestID(ctx context.Context, requestID string, options QueryOptions) (QueryResult, error)
}
