// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencestore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

type Store struct {
	mu        sync.Mutex
	traces    map[string][]evidencevo.NormalizedTrace
	requests  map[string][]evidencevo.NormalizedTrace
	artifacts map[string]evidencevo.EvidenceArtifact
}

func New() *Store {
	return &Store{
		traces:    map[string][]evidencevo.NormalizedTrace{},
		requests:  map[string][]evidencevo.NormalizedTrace{},
		artifacts: map[string]evidencevo.EvidenceArtifact{},
	}
}

func (s *Store) StoreArtifact(_ context.Context, artifact evidencevo.EvidenceArtifact) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, found := s.artifacts[artifact.ArtifactID]
	if found {
		existingFingerprint, err := evidencevo.ArtifactFingerprint(existing)
		if err != nil {
			return false, err
		}
		incomingFingerprint, err := evidencevo.ArtifactFingerprint(artifact)
		if err != nil {
			return false, err
		}
		if existingFingerprint != incomingFingerprint {
			return false, iartifactstore.ErrArtifactIDConflict
		}
		return false, nil
	}
	s.artifacts[artifact.ArtifactID] = artifact
	return true, nil
}

func (s *Store) GetArtifact(_ context.Context, artifactID string, scope evidencevo.QueryScope) (evidencevo.EvidenceArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, found := s.artifacts[artifactID]
	if !found || !evidencevo.MatchesArtifactScope(artifact, scope) {
		return evidencevo.EvidenceArtifact{}, false, nil
	}
	return artifact, true, nil
}

func (s *Store) ListArtifactsByRequestID(_ context.Context, requestID string, options iartifactstore.QueryOptions) (iartifactstore.QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]evidencevo.EvidenceArtifact, 0)
	for _, artifact := range s.artifacts {
		if artifact.RequestID == requestID && evidencevo.MatchesArtifactScope(artifact, options.Scope) {
			result = append(result, artifact)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ObservedAt == result[j].ObservedAt {
			return result[i].ArtifactID < result[j].ArtifactID
		}
		return result[i].ObservedAt < result[j].ObservedAt
	})
	limit := options.Limit
	if limit <= 0 || limit > iartifactstore.MaxArtifactQueryLimit {
		limit = iartifactstore.MaxArtifactQueryLimit
	}
	truncated := len(result) > limit
	if truncated {
		result = result[:limit]
	}
	return iartifactstore.QueryResult{Entries: result, Truncated: truncated}, nil
}

func (s *Store) ListEvidence(_ context.Context, scope evidencevo.QueryScope) ([]evidencevo.NormalizedTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]evidencevo.NormalizedTrace, 0, len(s.traces))
	for _, batches := range s.traces {
		authorized := filterByScope(batches, scope)
		if len(authorized) == 0 {
			continue
		}
		events := make([]evidencevo.EvidenceEvent, 0)
		for _, batch := range authorized {
			events = append(events, batch.Events...)
		}
		result = append(result, evidencevo.WithEvents(authorized[len(authorized)-1], events))
	}
	return result, nil
}

func (s *Store) ListArtifacts(_ context.Context, scope evidencevo.QueryScope) ([]evidencevo.EvidenceArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]evidencevo.EvidenceArtifact, 0, len(s.artifacts))
	for _, artifact := range s.artifacts {
		if evidencevo.MatchesArtifactScope(artifact, scope) {
			result = append(result, artifact)
		}
	}
	return result, nil
}

func (s *Store) LoadExecutionProjection(ctx context.Context, query iprojectionsource.Query) (iprojectionsource.Result, error) {
	traces, err := s.ListEvidence(ctx, query.Scope)
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	artifacts, err := s.ListArtifacts(ctx, query.Scope)
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	filteredTraces := make([]evidencevo.NormalizedTrace, 0, len(traces))
	for _, trace := range traces {
		if query.RequestID != "" && trace.RequestID != query.RequestID ||
			query.TraceID != "" && trace.TraceID != query.TraceID ||
			query.InteractionID != "" && !memoryTraceHasInteraction(trace, query.InteractionID) ||
			!memoryTraceInRange(trace, query.From, query.To) {
			continue
		}
		filteredTraces = append(filteredTraces, trace)
	}
	filteredArtifacts := make([]evidencevo.EvidenceArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if query.RequestID != "" && artifact.RequestID != query.RequestID ||
			query.TraceID != "" && artifact.TraceID != query.TraceID ||
			query.InteractionID != "" && artifact.InteractionID != query.InteractionID ||
			!memoryTimeInRange(artifact.ObservedAt, query.From, query.To) {
			continue
		}
		filteredArtifacts = append(filteredArtifacts, artifact)
	}
	sort.Slice(filteredTraces, func(i, j int) bool { return filteredTraces[i].TraceID < filteredTraces[j].TraceID })
	sort.Slice(filteredArtifacts, func(i, j int) bool { return filteredArtifacts[i].ArtifactID < filteredArtifacts[j].ArtifactID })
	truncated := false
	if query.Limit > 0 && len(filteredTraces) > query.Limit {
		filteredTraces = filteredTraces[:query.Limit]
		truncated = true
	}
	if query.Limit > 0 && len(filteredArtifacts) > query.Limit {
		filteredArtifacts = filteredArtifacts[:query.Limit]
		truncated = true
	}
	return iprojectionsource.Result{Traces: filteredTraces, Artifacts: filteredArtifacts, Truncated: truncated}, nil
}

func memoryTraceHasInteraction(trace evidencevo.NormalizedTrace, interactionID string) bool {
	for _, event := range trace.Events {
		if event.InteractionID == interactionID {
			return true
		}
	}
	return false
}

func memoryTraceInRange(trace evidencevo.NormalizedTrace, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	var earliest time.Time
	for _, event := range trace.Events {
		parsed, err := time.Parse(time.RFC3339Nano, event.ObservedAt)
		if err == nil && (earliest.IsZero() || parsed.Before(earliest)) {
			earliest = parsed
		}
	}
	if earliest.IsZero() {
		return false
	}
	return (from.IsZero() || !earliest.Before(from)) && (to.IsZero() || !earliest.After(to))
}

func memoryTimeInRange(value string, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return false
	}
	return (from.IsZero() || !parsed.Before(from)) && (to.IsZero() || !parsed.After(to))
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
	if scope.AccountID == "" && scope.AccountType == "" {
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
