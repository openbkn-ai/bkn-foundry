package ievidencestore

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

var (
	ErrEventIDConflict         = errors.New("BKN_TRACE_EVENT_ID_CONFLICT")
	ErrActionTransitionInvalid = errors.New("BKN_TRACE_ACTION_TRANSITION_INVALID")
	ErrCausationInvalid        = errors.New("BKN_TRACE_CAUSATION_INVALID")
)

type EvidenceStorePort interface {
	StoreEvidence(ctx context.Context, trace evidencevo.NormalizedTrace) error
	GetEvidenceHistoryByTraceID(ctx context.Context, traceID string) ([]evidencevo.NormalizedTrace, error)
	GetEvidenceByTraceID(ctx context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error)
	GetEvidenceByRequestID(ctx context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error)
}
