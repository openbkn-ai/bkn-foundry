package opensearchevidencestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
)

const (
	maxEvidenceSearchResults = 1000
	evidenceSearchPageSize   = 500
	maxCASRetries            = 16
)

type Store struct {
	client               *opensearch.Client
	index                string
	now                  func() time.Time
	ensureMu             sync.Mutex
	indexEnsured         bool
	artifactEnsureMu     sync.Mutex
	artifactIndexEnsured bool
}

type document struct {
	DocumentID             string                     `json:"document_id"`
	TraceID                string                     `json:"trace_id"`
	RequestID              string                     `json:"bkn.request.id"`
	ConversationID         string                     `json:"bkn.conversation.id,omitempty"`
	TenantID               string                     `json:"bkn.tenant.id,omitempty"`
	BusinessDomain         string                     `json:"business_domain,omitempty"`
	AccountID              string                     `json:"bkn.account.id"`
	AccountType            string                     `json:"bkn.account.type"`
	EffectiveSubjectID     string                     `json:"effective_subject_id,omitempty"`
	ApplicationPrincipalID string                     `json:"application_principal_id,omitempty"`
	KnowledgeNetworkIDs    []string                   `json:"knowledge_network_ids,omitempty"`
	SchemaVersion          string                     `json:"bkn.trace.schema.version"`
	Events                 []evidencevo.EvidenceEvent `json:"events"`
	ClaimIDs               []string                   `json:"claim_ids,omitempty"`
	AcceptedEvents         int                        `json:"accepted_event_count"`
	ClaimCount             int                        `json:"claim_count"`
	EvidenceRefCount       int                        `json:"evidence_ref_count"`
	BusinessRefCount       int                        `json:"business_ref_count"`
	ObservedStart          string                     `json:"observed_start,omitempty"`
	IngestedAt             string                     `json:"ingested_at"`
	Aggregate              bool                       `json:"aggregate,omitempty"`
}

type searchResponse struct {
	Hits struct {
		Hits []struct {
			ID     string   `json:"_id"`
			Source document `json:"_source"`
			Sort   []any    `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
}

func New(client *opensearch.Client, index string) *Store {
	return &Store{
		client: client,
		index:  index,
		now:    time.Now,
	}
}

func (s *Store) StoreEvidence(ctx context.Context, trace evidencevo.NormalizedTrace) error {
	if err := s.ensureIndex(ctx); err != nil {
		return err
	}
	aggregateID := aggregateDocumentID(trace.TraceID)
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		doc, seqNo, primaryTerm, found, err := s.loadAggregate(ctx, aggregateID)
		if err != nil {
			return err
		}
		existing := []evidencevo.NormalizedTrace{}
		if found {
			existing = append(existing, fromDocument(doc))
		} else {
			existing, err = s.searchAll(ctx, "trace_id", trace.TraceID)
			if err != nil {
				return err
			}
		}
		if err := appendViolationError(evidencevo.ValidateAppend(existing, trace)); err != nil {
			return err
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
		merged := mergeTraces(existing, trace, novel)
		doc = toDocument(merged, s.now().UTC())
		doc.DocumentID = aggregateID
		doc.Aggregate = true
		body, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("marshal evidence aggregate: %w", err)
		}
		if len(merged.Events) > evidencevo.MaxTraceEvents || len(body) > evidencevo.MaxTraceSerializedBytes {
			return ievidencestore.ErrTraceCapacityExceeded
		}
		if found {
			_, err = s.client.UpdateDocument(ctx, s.index, aggregateID, body, seqNo, primaryTerm)
		} else {
			_, err = s.client.CreateDocument(ctx, s.index, aggregateID, body)
		}
		if errors.Is(err, opensearch.ErrVersionConflict) {
			continue
		}
		return err
	}
	return fmt.Errorf("evidence aggregate update exceeded %d optimistic concurrency retries", maxCASRetries)
}

func appendViolationError(violation *evidencevo.AppendViolation) error {
	if violation == nil {
		return nil
	}
	switch violation.Kind {
	case evidencevo.AppendViolationEventIDConflict:
		return fmt.Errorf("%w: event_id %s", ievidencestore.ErrEventIDConflict, violation.EventID)
	case evidencevo.AppendViolationAction:
		return fmt.Errorf("%w: event_id %s", ievidencestore.ErrActionTransitionInvalid, violation.EventID)
	case evidencevo.AppendViolationCausation:
		return fmt.Errorf("%w: event_id %s", ievidencestore.ErrCausationInvalid, violation.EventID)
	case evidencevo.AppendViolationOwnership:
		return ievidencestore.ErrOwnershipConflict
	default:
		return fmt.Errorf("unknown evidence append violation: %s", violation.Kind)
	}
}

func mergeTraces(existing []evidencevo.NormalizedTrace, incoming evidencevo.NormalizedTrace, novel []evidencevo.EvidenceEvent) evidencevo.NormalizedTrace {
	events := make([]evidencevo.EvidenceEvent, 0, len(novel))
	for _, trace := range existing {
		events = append(events, trace.Events...)
	}
	events = append(events, novel...)
	return evidencevo.WithEvents(incoming, events)
}

func (s *Store) loadAggregate(ctx context.Context, documentID string) (document, int64, int64, bool, error) {
	stored, err := s.client.GetDocument(ctx, s.index, documentID)
	if errors.Is(err, opensearch.ErrDocumentNotFound) {
		return document{}, 0, 0, false, nil
	}
	if err != nil {
		return document{}, 0, 0, false, err
	}
	var doc document
	if err := json.Unmarshal(stored.Source, &doc); err != nil {
		return document{}, 0, 0, false, fmt.Errorf("decode evidence aggregate: %w", err)
	}
	return doc, stored.SeqNo, stored.PrimaryTerm, true, nil
}

func (s *Store) GetEvidenceByTraceID(ctx context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	return s.search(ctx, "trace_id", traceID, options)
}

func (s *Store) GetEvidenceHistoryByTraceID(ctx context.Context, traceID string) ([]evidencevo.NormalizedTrace, error) {
	if err := s.ensureIndex(ctx); err != nil {
		return nil, err
	}
	doc, _, _, found, err := s.loadAggregate(ctx, aggregateDocumentID(traceID))
	if err != nil {
		return nil, err
	}
	if found {
		return []evidencevo.NormalizedTrace{fromDocument(doc)}, nil
	}
	return s.searchAll(ctx, "trace_id", traceID)
}

func (s *Store) GetEvidenceByRequestID(ctx context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	return s.search(ctx, "bkn.request.id", requestID, options)
}

func (s *Store) search(ctx context.Context, field string, value string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	if err := s.ensureIndex(ctx); err != nil {
		return evidencevo.EvidenceQueryResult{}, err
	}

	limit := options.Limit
	if limit <= 0 {
		limit = maxEvidenceSearchResults
	}
	traces, err := s.searchAllScoped(ctx, field, value, options.Scope)
	if err != nil {
		return evidencevo.EvidenceQueryResult{}, err
	}
	if options.Scope.AccountID != "" || options.Scope.AccountType != "" || options.Scope.TenantID != "" || options.Scope.BusinessDomain != "" {
		filtered := make([]evidencevo.NormalizedTrace, 0, len(traces))
		for _, trace := range traces {
			if evidencevo.MatchesScope(trace, options.Scope) {
				filtered = append(filtered, trace)
			}
		}
		traces = filtered
	}
	result := evidencevo.EvidenceQueryResult{Traces: traces}
	if len(result.Traces) > limit {
		result.Truncated = true
		result.Traces = result.Traces[:limit]
	}
	return result, nil
}

type evidenceHit struct {
	ID     string
	Source document
	Sort   []any
}

func (s *Store) searchPage(ctx context.Context, field, value string, scope evidencevo.QueryScope, size int, searchAfter []any) ([]evidenceHit, error) {
	must := []map[string]any{{"bool": exactTermQuery(field, value)}}
	must = append(must, scopeCandidateMust(scope)...)
	queryBody := map[string]any{
		"size": size,
		"query": map[string]any{
			"bool": map[string]any{"must": must},
		},
		"sort": []map[string]any{
			{"ingested_at": map[string]any{"order": "asc"}},
			{"document_id": map[string]any{"order": "asc"}},
		},
	}
	if len(searchAfter) > 0 {
		queryBody["search_after"] = searchAfter
	}
	query, err := json.Marshal(queryBody)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence search query: %w", err)
	}

	body, err := s.client.Search(ctx, s.index, query)
	if err != nil {
		return nil, err
	}

	var response searchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode evidence search response: %w", err)
	}

	hits := make([]evidenceHit, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		hits = append(hits, evidenceHit{ID: hit.ID, Source: hit.Source, Sort: hit.Sort})
	}
	return hits, nil
}

func (s *Store) searchAll(ctx context.Context, field, value string) ([]evidencevo.NormalizedTrace, error) {
	return s.searchAllScoped(ctx, field, value, evidencevo.QueryScope{})
}

func (s *Store) searchAllScoped(ctx context.Context, field, value string, scope evidencevo.QueryScope) ([]evidencevo.NormalizedTrace, error) {
	allHits := []evidenceHit{}
	var searchAfter []any
	for {
		hits, err := s.searchPage(ctx, field, value, scope, evidenceSearchPageSize, searchAfter)
		if err != nil {
			return nil, err
		}
		allHits = append(allHits, hits...)
		if len(hits) < evidenceSearchPageSize {
			break
		}
		last := hits[len(hits)-1]
		if len(last.Sort) == 0 {
			return nil, fmt.Errorf("opensearch evidence pagination response omitted sort values")
		}
		searchAfter = last.Sort
	}
	return tracesFromHits(allHits), nil
}

func tracesFromHits(hits []evidenceHit) []evidencevo.NormalizedTrace {
	aggregateTraces := map[string]struct{}{}
	for _, hit := range hits {
		if hit.Source.Aggregate {
			aggregateTraces[hit.Source.TraceID] = struct{}{}
		}
	}
	traces := make([]evidencevo.NormalizedTrace, 0, len(hits))
	for _, hit := range hits {
		if _, replacedByAggregate := aggregateTraces[hit.Source.TraceID]; replacedByAggregate && !hit.Source.Aggregate {
			continue
		}
		traces = append(traces, fromDocument(hit.Source))
	}
	return traces
}

func (s *Store) ensureIndex(ctx context.Context) error {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if s.indexEnsured {
		return nil
	}
	if err := s.client.EnsureIndex(ctx, s.index, []byte(evidenceIndexMapping)); err != nil {
		return err
	}
	s.indexEnsured = true
	return nil
}

const evidenceIndexMapping = `{"settings":{"index.mapping.total_fields.limit":200},"mappings":{"dynamic":false,"properties":{"document_id":{"type":"keyword"},"trace_id":{"type":"keyword"},"business_domain":{"type":"keyword"},"effective_subject_id":{"type":"keyword"},"application_principal_id":{"type":"keyword"},"knowledge_network_ids":{"type":"keyword"},"bkn":{"properties":{"conversation":{"properties":{"id":{"type":"keyword"}}},"tenant":{"properties":{"id":{"type":"keyword"}}},"account":{"properties":{"id":{"type":"keyword"},"type":{"type":"keyword"}}},"request":{"properties":{"id":{"type":"keyword"}}},"trace":{"properties":{"schema":{"properties":{"version":{"type":"keyword"}}}}}}},"events":{"type":"object","enabled":false},"claim_ids":{"type":"keyword"},"accepted_event_count":{"type":"integer"},"claim_count":{"type":"integer"},"evidence_ref_count":{"type":"integer"},"business_ref_count":{"type":"integer"},"observed_start":{"type":"date","format":"strict_date_optional_time_nanos||strict_date_optional_time||epoch_millis"},"ingested_at":{"type":"date","format":"strict_date_optional_time_nanos||strict_date_optional_time||epoch_millis"},"aggregate":{"type":"boolean"}}}}`

func toDocument(trace evidencevo.NormalizedTrace, ingestedAt time.Time) document {
	doc := document{
		TraceID:                trace.TraceID,
		RequestID:              trace.RequestID,
		ConversationID:         trace.ConversationID,
		TenantID:               trace.TenantID,
		BusinessDomain:         trace.BusinessDomain,
		AccountID:              trace.AccountID,
		AccountType:            trace.AccountType,
		EffectiveSubjectID:     trace.EffectiveSubjectID,
		ApplicationPrincipalID: trace.ApplicationPrincipalID,
		KnowledgeNetworkIDs:    append([]string(nil), trace.KnowledgeNetworkIDs...),
		SchemaVersion:          trace.SchemaVersion,
		Events:                 trace.Events,
		ClaimIDs:               trace.ClaimIDs,
		AcceptedEvents:         trace.AcceptedEvents,
		ClaimCount:             trace.ClaimCount,
		EvidenceRefCount:       trace.EvidenceRefCount,
		BusinessRefCount:       trace.BusinessRefCount,
		ObservedStart:          traceObservedStart(trace),
		IngestedAt:             ingestedAt.Format(time.RFC3339Nano),
	}
	doc.DocumentID = evidenceDocumentID(doc)
	return doc
}

func traceObservedStart(trace evidencevo.NormalizedTrace) string {
	var earliest time.Time
	for _, event := range trace.Events {
		observedAt, err := time.Parse(time.RFC3339Nano, event.ObservedAt)
		if err == nil && (earliest.IsZero() || observedAt.Before(earliest)) {
			earliest = observedAt
		}
	}
	if earliest.IsZero() {
		return ""
	}
	return earliest.Format(time.RFC3339Nano)
}

func fromDocument(doc document) evidencevo.NormalizedTrace {
	return evidencevo.NormalizedTrace{
		TraceID:                doc.TraceID,
		RequestID:              doc.RequestID,
		ConversationID:         doc.ConversationID,
		TenantID:               doc.TenantID,
		BusinessDomain:         doc.BusinessDomain,
		AccountID:              doc.AccountID,
		AccountType:            doc.AccountType,
		EffectiveSubjectID:     doc.EffectiveSubjectID,
		ApplicationPrincipalID: doc.ApplicationPrincipalID,
		KnowledgeNetworkIDs:    append([]string(nil), doc.KnowledgeNetworkIDs...),
		SchemaVersion:          doc.SchemaVersion,
		Events:                 doc.Events,
		ClaimIDs:               doc.ClaimIDs,
		AcceptedEvents:         doc.AcceptedEvents,
		ClaimCount:             doc.ClaimCount,
		EvidenceRefCount:       doc.EvidenceRefCount,
		BusinessRefCount:       doc.BusinessRefCount,
	}
}

func evidenceDocumentID(doc document) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(doc.TraceID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(doc.RequestID))
	for _, event := range doc.Events {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(event.EventID))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func aggregateDocumentID(traceID string) string {
	sum := sha256.Sum256([]byte("trace-aggregate\x00" + traceID))
	return "aggregate-" + hex.EncodeToString(sum[:])
}

func exactTermQuery(field string, value string) map[string]any {
	return map[string]any{
		"should": []map[string]any{
			{"term": map[string]any{field: map[string]any{"value": value}}},
			{"term": map[string]any{field + ".keyword": map[string]any{"value": value}}},
		},
		"minimum_should_match": 1,
	}
}
