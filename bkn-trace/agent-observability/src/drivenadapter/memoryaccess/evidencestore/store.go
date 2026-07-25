package evidencestore

import (
	"context"
	"encoding/json"
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
	if violation := evidencevo.ValidateAppend(existing, trace); violation != nil {
		switch violation.Kind {
		case evidencevo.AppendViolationEventIDConflict:
			return fmt.Errorf("%w: event_id %s", ievidencestore.ErrEventIDConflict, violation.EventID)
		case evidencevo.AppendViolationAction:
			return fmt.Errorf("%w: event_id %s", ievidencestore.ErrActionTransitionInvalid, violation.EventID)
		case evidencevo.AppendViolationCausation:
			return fmt.Errorf("%w: event_id %s", ievidencestore.ErrCausationInvalid, violation.EventID)
		case evidencevo.AppendViolationOwnership:
			return ievidencestore.ErrOwnershipConflict
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
	allEvents := make([]evidencevo.EvidenceEvent, 0, len(novel))
	for _, item := range existing {
		allEvents = append(allEvents, item.Events...)
	}
	allEvents = append(allEvents, novel...)
	serialized, err := json.Marshal(allEvents)
	if err != nil {
		return err
	}
	if len(allEvents) > evidencevo.MaxTraceEvents || len(serialized) > evidencevo.MaxTraceSerializedBytes {
		return ievidencestore.ErrTraceCapacityExceeded
	}
	s.traces[trace.TraceID] = append(s.traces[trace.TraceID], trace)
	s.requests[trace.RequestID] = append(s.requests[trace.RequestID], trace)
	return nil
}

func (s *Store) GetEvidenceByTraceID(_ context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limitedResult(filterByScope(s.traces[traceID], options.Scope), options.Limit), nil
}

func (s *Store) GetEvidenceHistoryByTraceID(_ context.Context, traceID string) ([]evidencevo.NormalizedTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]evidencevo.NormalizedTrace(nil), s.traces[traceID]...), nil
}

func (s *Store) GetEvidenceByRequestID(_ context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limitedResult(filterByScope(s.requests[requestID], options.Scope), options.Limit), nil
}

func filterByScope(traces []evidencevo.NormalizedTrace, scope evidencevo.QueryScope) []evidencevo.NormalizedTrace {
	if scope.AccountID == "" && scope.AccountType == "" && scope.TenantID == "" && scope.BusinessDomain == "" {
		return traces
	}
	filtered := make([]evidencevo.NormalizedTrace, 0, len(traces))
	for _, trace := range traces {
		if evidencevo.MatchesScope(trace, scope) {
			filtered = append(filtered, trace)
		}
	}
	return filtered
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
