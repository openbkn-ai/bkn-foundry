package opensearchevidencestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

const maxEvidenceSearchResults = 1000

type Store struct {
	client *opensearch.Client
	index  string
	now    func() time.Time
}

type document struct {
	DocumentID       string                     `json:"document_id"`
	TraceID          string                     `json:"trace_id"`
	RequestID        string                     `json:"bkn.request.id"`
	SchemaVersion    string                     `json:"bkn.trace.schema.version"`
	Events           []evidencevo.EvidenceEvent `json:"events"`
	ClaimIDs         []string                   `json:"claim_ids,omitempty"`
	AcceptedEvents   int                        `json:"accepted_event_count"`
	ClaimCount       int                        `json:"claim_count"`
	EvidenceRefCount int                        `json:"evidence_ref_count"`
	BusinessRefCount int                        `json:"business_ref_count"`
	IngestedAt       string                     `json:"ingested_at"`
}

type searchResponse struct {
	Hits struct {
		Hits []struct {
			Source document `json:"_source"`
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
	doc := toDocument(trace, s.now().UTC())
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal evidence document: %w", err)
	}
	if _, err := s.client.IndexDocument(ctx, s.index, doc.DocumentID, body); err != nil {
		return err
	}
	return nil
}

func (s *Store) GetEvidenceByTraceID(ctx context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	return s.search(ctx, "trace_id", traceID, options)
}

func (s *Store) GetEvidenceByRequestID(ctx context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	return s.search(ctx, "bkn.request.id", requestID, options)
}

func (s *Store) search(ctx context.Context, field string, value string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = maxEvidenceSearchResults
	}
	query, err := json.Marshal(map[string]any{
		"size": limit + 1,
		"query": map[string]any{
			"bool": exactTermQuery(field, value),
		},
		"sort": []map[string]any{
			{"ingested_at": map[string]any{"order": "asc"}},
		},
	})
	if err != nil {
		return evidencevo.EvidenceQueryResult{}, fmt.Errorf("marshal evidence search query: %w", err)
	}

	body, err := s.client.Search(ctx, s.index, query)
	if err != nil {
		return evidencevo.EvidenceQueryResult{}, err
	}

	var response searchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return evidencevo.EvidenceQueryResult{}, fmt.Errorf("decode evidence search response: %w", err)
	}

	traces := make([]evidencevo.NormalizedTrace, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		traces = append(traces, fromDocument(hit.Source))
	}
	result := evidencevo.EvidenceQueryResult{Traces: traces}
	if len(result.Traces) > limit {
		result.Truncated = true
		result.Traces = result.Traces[:limit]
	}
	return result, nil
}

func toDocument(trace evidencevo.NormalizedTrace, ingestedAt time.Time) document {
	doc := document{
		TraceID:          trace.TraceID,
		RequestID:        trace.RequestID,
		SchemaVersion:    trace.SchemaVersion,
		Events:           trace.Events,
		ClaimIDs:         trace.ClaimIDs,
		AcceptedEvents:   trace.AcceptedEvents,
		ClaimCount:       trace.ClaimCount,
		EvidenceRefCount: trace.EvidenceRefCount,
		BusinessRefCount: trace.BusinessRefCount,
		IngestedAt:       ingestedAt.Format(time.RFC3339Nano),
	}
	doc.DocumentID = evidenceDocumentID(doc)
	return doc
}

func fromDocument(doc document) evidencevo.NormalizedTrace {
	return evidencevo.NormalizedTrace{
		TraceID:          doc.TraceID,
		RequestID:        doc.RequestID,
		SchemaVersion:    doc.SchemaVersion,
		Events:           doc.Events,
		ClaimIDs:         doc.ClaimIDs,
		AcceptedEvents:   doc.AcceptedEvents,
		ClaimCount:       doc.ClaimCount,
		EvidenceRefCount: doc.EvidenceRefCount,
		BusinessRefCount: doc.BusinessRefCount,
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

func exactTermQuery(field string, value string) map[string]any {
	return map[string]any{
		"should": []map[string]any{
			{"term": map[string]any{field: map[string]any{"value": value}}},
			{"term": map[string]any{field + ".keyword": map[string]any{"value": value}}},
		},
		"minimum_should_match": 1,
	}
}
