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

func (s *Store) GetEvidenceByTraceID(_ context.Context, traceID string) ([]evidencevo.NormalizedTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]evidencevo.NormalizedTrace(nil), s.traces[traceID]...), nil
}

func (s *Store) GetEvidenceByRequestID(_ context.Context, requestID string) ([]evidencevo.NormalizedTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]evidencevo.NormalizedTrace(nil), s.requests[requestID]...), nil
}

func (s *Store) TraceCount(traceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.traces[traceID])
}
