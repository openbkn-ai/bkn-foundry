package opensearchevidencestore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

func (s *Store) ListEvidence(ctx context.Context, scope evidencevo.QueryScope) ([]evidencevo.NormalizedTrace, error) {
	traces, _, err := s.listEvidenceProjection(ctx, iprojectionsource.Query{Scope: scope})
	return traces, err
}

func (s *Store) LoadExecutionProjection(ctx context.Context, query iprojectionsource.Query) (iprojectionsource.Result, error) {
	traces, evidenceTruncated, err := s.listEvidenceProjection(ctx, query)
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	artifacts, artifactTruncated, err := s.listArtifactProjection(ctx, query)
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	return iprojectionsource.Result{
		Traces: traces, Artifacts: artifacts, Truncated: evidenceTruncated || artifactTruncated,
	}, nil
}

func (s *Store) listEvidenceProjection(ctx context.Context, query iprojectionsource.Query) ([]evidencevo.NormalizedTrace, bool, error) {
	if err := s.ensureIndex(ctx); err != nil {
		return nil, false, err
	}
	allHits := make([]evidenceHit, 0)
	var searchAfter []any
	truncated := false
	for {
		size := projectionPageSize(query.Limit, len(allHits))
		hits, err := s.listEvidenceProjectionPage(ctx, query, searchAfter, size)
		if err != nil {
			return nil, false, err
		}
		allHits = append(allHits, hits...)
		if query.Limit > 0 && len(allHits) > query.Limit {
			allHits = allHits[:query.Limit]
			truncated = true
			break
		}
		if len(hits) < size {
			break
		}
		if len(hits[len(hits)-1].Sort) == 0 {
			return nil, false, fmt.Errorf("opensearch evidence projection pagination response omitted sort values")
		}
		searchAfter = hits[len(hits)-1].Sort
	}
	traces := projectionTracesFromHits(allHits, query.Scope)
	filtered := make([]evidencevo.NormalizedTrace, 0, len(traces))
	for _, trace := range traces {
		if matchesProjectionTrace(trace, query) {
			filtered = append(filtered, trace)
		}
	}
	return filtered, truncated, nil
}

func (s *Store) listEvidenceProjectionPage(ctx context.Context, query iprojectionsource.Query, searchAfter []any, size int) ([]evidenceHit, error) {
	must := ownershipMust(query.Scope)
	must = append(must, map[string]any{
		"bool": map[string]any{
			"should": []map[string]any{
				{"term": map[string]any{"aggregate": true}},
				{"bool": map[string]any{"must_not": map[string]any{"exists": map[string]any{"field": "aggregate"}}}},
			},
			"minimum_should_match": 1,
		},
	})
	must = appendProjectionIdentityFilters(must, query)
	must = appendEvidenceTimeFilter(must, query)
	queryBody := map[string]any{
		"size":  size,
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"sort": []map[string]any{
			{"document_id": map[string]any{"order": "asc"}},
		},
	}
	if len(searchAfter) > 0 {
		queryBody["search_after"] = searchAfter
	}
	queryJSON, err := json.Marshal(queryBody)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence projection query: %w", err)
	}
	body, err := s.client.Search(ctx, s.index, queryJSON)
	if err != nil {
		return nil, err
	}
	var response searchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode evidence projection response: %w", err)
	}
	hits := make([]evidenceHit, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		hits = append(hits, evidenceHit{ID: hit.ID, Source: hit.Source, Sort: hit.Sort})
	}
	return hits, nil
}

func (s *Store) ListArtifacts(ctx context.Context, scope evidencevo.QueryScope) ([]evidencevo.EvidenceArtifact, error) {
	artifacts, _, err := s.listArtifactProjection(ctx, iprojectionsource.Query{Scope: scope})
	return artifacts, err
}

func (s *Store) listArtifactProjection(ctx context.Context, query iprojectionsource.Query) ([]evidencevo.EvidenceArtifact, bool, error) {
	if err := s.ensureArtifactIndex(ctx); err != nil {
		return nil, false, err
	}
	allHits := make([]artifactProjectionHit, 0)
	var searchAfter []any
	truncated := false
	for {
		size := projectionPageSize(query.Limit, len(allHits))
		hits, err := s.listArtifactProjectionPage(ctx, query, searchAfter, size)
		if err != nil {
			return nil, false, err
		}
		allHits = append(allHits, hits...)
		if query.Limit > 0 && len(allHits) > query.Limit {
			allHits = allHits[:query.Limit]
			truncated = true
			break
		}
		if len(hits) < size {
			break
		}
		if len(hits[len(hits)-1].Sort) == 0 {
			return nil, false, fmt.Errorf("opensearch artifact projection pagination response omitted sort values")
		}
		searchAfter = hits[len(hits)-1].Sort
	}
	artifacts := make([]evidencevo.EvidenceArtifact, 0, len(allHits))
	for _, hit := range allHits {
		if evidencevo.MatchesArtifactScope(hit.Source, query.Scope) && matchesProjectionArtifact(hit.Source, query) {
			artifacts = append(artifacts, hit.Source)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].ObservedAt == artifacts[j].ObservedAt {
			return artifacts[i].ArtifactID < artifacts[j].ArtifactID
		}
		return artifacts[i].ObservedAt < artifacts[j].ObservedAt
	})
	return artifacts, truncated, nil
}

func matchesProjectionTrace(trace evidencevo.NormalizedTrace, query iprojectionsource.Query) bool {
	if query.RequestID != "" && trace.RequestID != query.RequestID ||
		query.TraceID != "" && trace.TraceID != query.TraceID ||
		query.BusinessDomain != "" && trace.BusinessDomain != query.BusinessDomain {
		return false
	}
	if query.From.IsZero() && query.To.IsZero() {
		return true
	}
	observedStart := traceObservedStart(trace)
	parsed, err := time.Parse(time.RFC3339Nano, observedStart)
	if err != nil {
		return false
	}
	return (query.From.IsZero() || !parsed.Before(query.From)) &&
		(query.To.IsZero() || !parsed.After(query.To))
}

func matchesProjectionArtifact(artifact evidencevo.EvidenceArtifact, query iprojectionsource.Query) bool {
	if query.RequestID != "" && artifact.RequestID != query.RequestID ||
		query.TraceID != "" && artifact.TraceID != query.TraceID ||
		query.BusinessDomain != "" && artifact.BusinessDomain != query.BusinessDomain {
		return false
	}
	if query.From.IsZero() && query.To.IsZero() {
		return true
	}
	observedAt, err := time.Parse(time.RFC3339Nano, artifact.ObservedAt)
	if err != nil {
		return false
	}
	return (query.From.IsZero() || !observedAt.Before(query.From)) &&
		(query.To.IsZero() || !observedAt.After(query.To))
}

type artifactProjectionHit struct {
	Source evidencevo.EvidenceArtifact
	Sort   []any
}

func (s *Store) listArtifactProjectionPage(ctx context.Context, query iprojectionsource.Query, searchAfter []any, size int) ([]artifactProjectionHit, error) {
	must := appendProjectionIdentityFilters(ownershipMust(query.Scope), query)
	if !query.From.IsZero() || !query.To.IsZero() {
		bounds := map[string]any{}
		if !query.From.IsZero() {
			bounds["gte"] = query.From.Format("2006-01-02T15:04:05.999999999Z07:00")
		}
		if !query.To.IsZero() {
			bounds["lte"] = query.To.Format("2006-01-02T15:04:05.999999999Z07:00")
		}
		must = append(must, map[string]any{"range": map[string]any{"observed_at": bounds}})
	}
	queryBody := map[string]any{
		"size":  size,
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"sort": []map[string]any{
			{"observed_at": map[string]any{"order": "asc"}},
			{"artifact_id": map[string]any{"order": "asc"}},
		},
	}
	if len(searchAfter) > 0 {
		queryBody["search_after"] = searchAfter
	}
	queryJSON, err := json.Marshal(queryBody)
	if err != nil {
		return nil, fmt.Errorf("marshal artifact projection query: %w", err)
	}
	body, err := s.client.Search(ctx, s.artifactIndex(), queryJSON)
	if err != nil {
		return nil, err
	}
	var response struct {
		Hits struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
				Sort   []any           `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode artifact projection response: %w", err)
	}
	hits := make([]artifactProjectionHit, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		artifact, err := decodeArtifactDocument(hit.Source)
		if err != nil {
			return nil, fmt.Errorf("decode artifact projection hit: %w", err)
		}
		hits = append(hits, artifactProjectionHit{Source: artifact, Sort: hit.Sort})
	}
	return hits, nil
}

func projectionPageSize(limit, loaded int) int {
	if limit <= 0 {
		return evidenceSearchPageSize
	}
	remainingWithSentinel := limit - loaded + 1
	if remainingWithSentinel < evidenceSearchPageSize {
		return remainingWithSentinel
	}
	return evidenceSearchPageSize
}

func appendProjectionIdentityFilters(must []map[string]any, query iprojectionsource.Query) []map[string]any {
	for _, item := range []struct {
		field string
		value string
	}{
		{"bkn.request.id", query.RequestID},
		{"trace_id", query.TraceID},
		{"business_domain", query.BusinessDomain},
	} {
		if item.value != "" {
			must = append(must, map[string]any{"bool": exactTermQuery(item.field, item.value)})
		}
	}
	return must
}

func appendEvidenceTimeFilter(must []map[string]any, query iprojectionsource.Query) []map[string]any {
	if query.From.IsZero() && query.To.IsZero() {
		return must
	}
	bounds := map[string]any{}
	if !query.From.IsZero() {
		bounds["gte"] = query.From.Format(time.RFC3339Nano)
	}
	if !query.To.IsZero() {
		bounds["lte"] = query.To.Format(time.RFC3339Nano)
	}
	return append(must, map[string]any{
		"bool": map[string]any{
			"should": []map[string]any{
				{"range": map[string]any{"observed_start": bounds}},
				{"bool": map[string]any{"must_not": map[string]any{"exists": map[string]any{"field": "observed_start"}}}},
			},
			"minimum_should_match": 1,
		},
	})
}

func ownershipMust(scope evidencevo.QueryScope) []map[string]any {
	must := make([]map[string]any, 0, 4)
	for _, item := range []struct {
		field string
		value string
	}{
		{"bkn.tenant.id", scope.TenantID},
		{"business_domain", scope.BusinessDomain},
		{"bkn.account.id", scope.AccountID},
		{"bkn.account.type", scope.AccountType},
	} {
		if evidencevo.NeedsCrossAccountCandidates(scope) && (item.field == "bkn.account.id" || item.field == "bkn.account.type") {
			continue
		}
		if item.value != "" {
			must = append(must, map[string]any{"bool": exactTermQuery(item.field, item.value)})
		}
	}
	return must
}

func projectionTracesFromHits(hits []evidenceHit, scope evidencevo.QueryScope) []evidencevo.NormalizedTrace {
	aggregates := map[string]evidencevo.NormalizedTrace{}
	legacy := map[string][]evidencevo.NormalizedTrace{}
	for _, hit := range hits {
		trace := fromDocument(hit.Source)
		if !evidencevo.MatchesScope(trace, scope) {
			continue
		}
		if hit.Source.Aggregate {
			aggregates[trace.TraceID] = trace
			continue
		}
		legacy[trace.TraceID] = append(legacy[trace.TraceID], trace)
	}
	for traceID, batches := range legacy {
		if _, exists := aggregates[traceID]; exists || len(batches) == 0 {
			continue
		}
		events := make([]evidencevo.EvidenceEvent, 0)
		for _, batch := range batches {
			events = append(events, batch.Events...)
		}
		aggregates[traceID] = evidencevo.WithEvents(batches[len(batches)-1], events)
	}
	traces := make([]evidencevo.NormalizedTrace, 0, len(aggregates))
	for _, trace := range aggregates {
		traces = append(traces, trace)
	}
	sort.Slice(traces, func(i, j int) bool {
		left := traceObservedStart(traces[i])
		right := traceObservedStart(traces[j])
		leftTime, leftErr := time.Parse(time.RFC3339Nano, left)
		rightTime, rightErr := time.Parse(time.RFC3339Nano, right)
		if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if left != right {
			return left < right
		}
		return traces[i].TraceID < traces[j].TraceID
	})
	return traces
}
