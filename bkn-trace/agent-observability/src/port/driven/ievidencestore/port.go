package ievidencestore

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type EvidenceStorePort interface {
	StoreEvidence(ctx context.Context, trace evidencevo.NormalizedTrace) error
	GetEvidenceByTraceID(ctx context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error)
	GetEvidenceByRequestID(ctx context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error)
}
