package evidencestore

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type Store struct {
	mu     sync.Mutex
	traces map[string][]evidencevo.NormalizedTrace
}

func New() *Store {
	return &Store{traces: map[string][]evidencevo.NormalizedTrace{}}
}

func (s *Store) StoreEvidence(_ context.Context, trace evidencevo.NormalizedTrace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces[trace.TraceID] = append(s.traces[trace.TraceID], trace)
	return nil
}

func (s *Store) TraceCount(traceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.traces[traceID])
}
