package evidencestore

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type Store struct {
	mu       sync.Mutex
	traces   map[string][]evidencevo.NormalizedTrace
	requests map[string][]evidencevo.NormalizedTrace
}

func New() *Store {
	return &Store{
		traces:   map[string][]evidencevo.NormalizedTrace{},
		requests: map[string][]evidencevo.NormalizedTrace{},
	}
}

func (s *Store) StoreEvidence(_ context.Context, trace evidencevo.NormalizedTrace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces[trace.TraceID] = append(s.traces[trace.TraceID], trace)
	s.requests[trace.RequestID] = append(s.requests[trace.RequestID], trace)
	return nil
}

func (s *Store) GetEvidenceByTraceID(_ context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limitedResult(s.traces[traceID], options.Limit), nil
}

func (s *Store) GetEvidenceByRequestID(_ context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limitedResult(s.requests[requestID], options.Limit), nil
}

func (s *Store) TraceCount(traceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.traces[traceID])
}

func limitedResult(traces []evidencevo.NormalizedTrace, limit int) evidencevo.EvidenceQueryResult {
	if limit <= 0 || len(traces) <= limit {
		return evidencevo.EvidenceQueryResult{
			Traces: append([]evidencevo.NormalizedTrace(nil), traces...),
		}
	}
	return evidencevo.EvidenceQueryResult{
		Traces:    append([]evidencevo.NormalizedTrace(nil), traces[:limit]...),
		Truncated: true,
	}
}
