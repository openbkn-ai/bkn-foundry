package evidencestore

import (
	"context"
	"fmt"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
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
	existing := s.traces[trace.TraceID]
	if violation := evidencevo.ValidateAppend(existing, trace.Events); violation != nil {
		switch violation.Kind {
		case evidencevo.AppendViolationEventIDConflict:
			return fmt.Errorf("%w: event_id %s", ievidencestore.ErrEventIDConflict, violation.EventID)
		case evidencevo.AppendViolationAction:
			return fmt.Errorf("%w: event_id %s", ievidencestore.ErrActionTransitionInvalid, violation.EventID)
		case evidencevo.AppendViolationCausation:
			return fmt.Errorf("%w: event_id %s", ievidencestore.ErrCausationInvalid, violation.EventID)
		}
	}
	novel, conflictID, err := evidencevo.NovelEvents(existing, trace.Events)
	if err != nil {
		return err
	}
	if conflictID != "" {
		return fmt.Errorf("%w: event_id %s", ievidencestore.ErrEventIDConflict, conflictID)
	}
	if len(novel) == 0 {
		return nil
	}
	trace = evidencevo.WithEvents(trace, novel)
	s.traces[trace.TraceID] = append(s.traces[trace.TraceID], trace)
	s.requests[trace.RequestID] = append(s.requests[trace.RequestID], trace)
	return nil
}

func (s *Store) GetEvidenceByTraceID(_ context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limitedResult(s.traces[traceID], options.Limit), nil
}

func (s *Store) GetEvidenceHistoryByTraceID(_ context.Context, traceID string) ([]evidencevo.NormalizedTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]evidencevo.NormalizedTrace(nil), s.traces[traceID]...), nil
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
