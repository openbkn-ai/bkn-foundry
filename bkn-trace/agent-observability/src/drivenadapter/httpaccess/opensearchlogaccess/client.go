// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchlogaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

const sourceID = "otel-runtime"

type openSearchClient interface {
	Search(context.Context, string, []byte) ([]byte, error)
}

type searchResponse struct {
	Hits struct {
		Total struct {
			Value    int64  `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []searchHit `json:"hits"`
	} `json:"hits"`
}

type searchHit struct {
	ID     string          `json:"_id"`
	Source json.RawMessage `json:"_source"`
	Sort   []any           `json:"sort"`
}

type Client struct {
	backend openSearchClient
	index   string
}

func New(backend openSearchClient, index string) *Client {
	return &Client{backend: backend, index: index}
}

func (client *Client) ID() string { return sourceID }

func (client *Client) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: sourceID, Status: "healthy", Reliability: "best_effort",
		CollectionMethod: "direct_otlp", CoveredModules: []string{"openbkn"}, CountAccuracy: "exact",
		Categories: []string{
			observabilityvo.CategoryRuntimeSystem,
			observabilityvo.CategoryRuntimeBusiness,
			observabilityvo.CategoryRuntimeModel,
		},
	}
}

func (client *Client) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	body, err := json.Marshal(buildQuery(query))
	if err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("encode OpenSearch log query: %w", err)
	}
	responseBody, err := client.backend.Search(ctx, client.index, body)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	var response searchResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode OpenSearch log response: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		record, err := mapDocument(hit.ID, hit.Source)
		if err != nil {
			return observabilityvo.SourcePage{}, err
		}
		if len(hit.Sort) > 0 {
			record.CursorPosition = &observabilityvo.SourcePosition{
				EventTimestamp: record.EventTimestamp, SourceID: record.SourceID,
				LogID: record.SourceLogID, SearchAfter: append([]any(nil), hit.Sort...),
			}
		}
		records = append(records, record)
	}
	accuracy := "exact"
	if response.Hits.Total.Relation != "" && response.Hits.Total.Relation != "eq" {
		accuracy = "partial"
	}
	return observabilityvo.SourcePage{Records: records, Count: response.Hits.Total.Value, CountAccuracy: accuracy}, nil
}

func (client *Client) Get(ctx context.Context, logID string) (observabilityvo.LogRecord, bool, error) {
	scope := observabilityvo.SourceAccessScopeFromContext(ctx)
	if strings.TrimSpace(scope.TenantID) == "" {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("trusted tenant scope is required for OpenSearch log detail")
	}
	body, err := json.Marshal(buildDetailQuery(logID, scope))
	if err != nil {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("encode OpenSearch log detail query: %w", err)
	}
	responseBody, err := client.backend.Search(ctx, client.index, body)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	var response searchResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("decode OpenSearch log detail response: %w", err)
	}
	if len(response.Hits.Hits) == 0 {
		return observabilityvo.LogRecord{}, false, nil
	}
	hit := response.Hits.Hits[0]
	record, err := mapDocument(hit.ID, hit.Source)
	if err == nil && record.LogID != logID {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("OpenSearch log detail returned mismatched log_id")
	}
	return record, err == nil, err
}

func buildDetailQuery(logID string, scope observabilityvo.SourceAccessScope) map[string]any {
	filters := []any{
		map[string]any{"term": map[string]any{"attributes.log_id.keyword": logID}},
		map[string]any{"term": map[string]any{"attributes.tenant_id.keyword": scope.TenantID}},
		map[string]any{"term": map[string]any{"attributes.trust_level.keyword": "trusted"}},
	}
	return map[string]any{
		"size": 1, "track_total_hits": false,
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
	}
}

func buildQuery(query observabilityvo.LogQuery) map[string]any {
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	filters := make([]any, 0, 16)
	for _, field := range []string{"attributes.log_id", "attributes.source_log_id"} {
		filters = append(filters, map[string]any{"exists": map[string]any{"field": field}})
	}
	addTerm := func(field, value string) {
		if value != "" {
			filters = append(filters, map[string]any{"term": map[string]any{field: value}})
		}
	}
	addTerms := func(field string, values []string) {
		if len(values) > 0 {
			filters = append(filters, map[string]any{"terms": map[string]any{field: values}})
		}
	}
	addTerm("attributes.tenant_id.keyword", query.AuthorizedTenantID)
	addTerm("attributes.trust_level.keyword", "trusted")
	authorizedCategories := intersectValues(query.Categories, query.AuthorizedCategories)
	if len(query.Categories) == 0 {
		authorizedCategories = query.AuthorizedCategories
	}
	addTerms("attributes.log_category.keyword", authorizedCategories)
	if query.RequireRecordScope {
		candidates := make([]any, 0, 3)
		if query.AuthorizedSubjectID != "" {
			candidates = append(candidates, map[string]any{"term": map[string]any{
				"attributes.effective_subject_id.keyword": query.AuthorizedSubjectID,
			}})
		}
		if query.AuthorizedApplicationID != "" {
			candidates = append(candidates, map[string]any{"term": map[string]any{
				"attributes.application_id.keyword": query.AuthorizedApplicationID,
			}})
		}
		if len(query.AuthorizedKnowledgeNetworkIDs) > 0 {
			candidates = append(candidates, map[string]any{"terms": map[string]any{
				"attributes.knowledge_network_ids.keyword": query.AuthorizedKnowledgeNetworkIDs,
			}})
		}
		filters = append(filters, map[string]any{"bool": map[string]any{
			"should": candidates, "minimum_should_match": 1,
		}})
	}
	addTerm("traceId.keyword", query.TraceID)
	addTerm("spanId.keyword", query.SpanID)
	addTerm("attributes.request_id.keyword", query.RequestID)
	addTerm("attributes.conversation_id.keyword", query.ConversationID)
	addTerm("attributes.interaction_id.keyword", query.InteractionID)
	addTerm("attributes.operation_id.keyword", query.OperationID)
	addTerm("attributes.actor_id.keyword", query.ActorID)
	addTerm("attributes.application_id.keyword", query.ApplicationID)
	addTerm("attributes.resource_type.keyword", query.ResourceType)
	addTerm("attributes.resource_id.keyword", query.ResourceID)
	addTerms("resource.service.name.keyword", query.Services)
	addTerms("resource.deployment.environment.keyword", query.Environments)
	authorizedEventNames := observabilityvo.RegisteredEventNames(authorizedCategories)
	if len(query.EventNames) > 0 {
		authorizedEventNames = intersectValues(query.EventNames, authorizedEventNames)
	}
	addTerms("attributes.event_name.keyword", authorizedEventNames)
	if query.TimeFrom != nil || query.TimeTo != nil {
		bounds := make(map[string]any)
		if query.TimeFrom != nil {
			bounds["gte"] = query.TimeFrom.Format(time.RFC3339Nano)
		}
		if query.TimeTo != nil {
			bounds["lt"] = query.TimeTo.Format(time.RFC3339Nano)
		}
		filters = append(filters, map[string]any{"range": map[string]any{"@timestamp": bounds}})
	}
	if query.ObservedBefore != nil {
		filters = append(filters, map[string]any{"range": map[string]any{
			"observedTimestamp": map[string]any{"lte": query.ObservedBefore.Format(time.RFC3339Nano)},
		}})
	}
	if query.SeverityMinimum > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{"severity.number": map[string]any{"gte": query.SeverityMinimum}}})
	}
	if query.FailedOnly {
		addTerms("attributes.outcome.keyword", []string{"failure", "denied"})
	}
	boolQuery := map[string]any{"filter": filters}
	if strings.TrimSpace(query.Query) != "" {
		boolQuery["must"] = []any{map[string]any{"match_phrase": map[string]any{"attributes.safe_summary": query.Query}}}
	}
	result := map[string]any{
		"size": limit, "track_total_hits": true,
		"sort": []any{
			map[string]any{"@timestamp": map[string]any{"order": "desc"}},
			map[string]any{"attributes.source_id.keyword": map[string]any{"order": "asc", "unmapped_type": "keyword"}},
			map[string]any{"attributes.source_log_id.keyword": map[string]any{"order": "asc", "unmapped_type": "keyword"}},
		},
		"query": map[string]any{"bool": boolQuery},
	}
	if query.PageBefore != nil && (len(query.PageBefore.SearchAfter) > 0 ||
		(!query.PageBefore.EventTimestamp.IsZero() && query.PageBefore.LogID != "")) {
		if len(query.PageBefore.SearchAfter) > 0 {
			result["search_after"] = append([]any(nil), query.PageBefore.SearchAfter...)
		} else {
			result["search_after"] = []any{
				query.PageBefore.EventTimestamp.Format(time.RFC3339Nano), query.PageBefore.SourceID, query.PageBefore.LogID,
			}
		}
	}
	return result
}

func mapDocument(id string, payload []byte) (observabilityvo.LogRecord, error) {
	var document struct {
		Attributes        map[string]any `json:"attributes"`
		ObservedTimestamp string         `json:"observedTimestamp"`
		Timestamp         string         `json:"@timestamp"`
		Resource          map[string]any `json:"resource"`
		Severity          struct {
			Text   string `json:"text"`
			Number int    `json:"number"`
		} `json:"severity"`
		TraceID string `json:"traceId"`
		SpanID  string `json:"spanId"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		return observabilityvo.LogRecord{}, fmt.Errorf("decode SS4O log document: %w", err)
	}
	eventTimestamp, _ := time.Parse(time.RFC3339Nano, document.Timestamp)
	observedTimestamp, _ := time.Parse(time.RFC3339Nano, document.ObservedTimestamp)
	record := observabilityvo.LogRecord{
		SchemaVersion:  stringAttribute(document.Attributes, "schema_version", "1.0.0"),
		LogID:          stringAttribute(document.Attributes, "log_id", id),
		SourceID:       stringAttribute(document.Attributes, "source_id", ""),
		SourceLogID:    stringAttribute(document.Attributes, "source_log_id", id),
		Category:       stringAttribute(document.Attributes, "log_category", ""),
		EventName:      stringAttribute(document.Attributes, "event_name", ""),
		EventTimestamp: eventTimestamp, ObservedTimestamp: observedTimestamp,
		SeverityNumber: document.Severity.Number, SeverityText: document.Severity.Text,
		Outcome:             stringAttribute(document.Attributes, "outcome", ""),
		SafeSummary:         stringAttribute(document.Attributes, "safe_summary", ""),
		ServiceName:         stringAttribute(document.Resource, "service.name", stringAttribute(document.Attributes, "service.name", "")),
		Environment:         stringAttribute(document.Resource, "deployment.environment", ""),
		TenantID:            stringAttribute(document.Attributes, "tenant_id", ""),
		ActorID:             stringAttribute(document.Attributes, "actor_id", ""),
		EffectiveSubjectID:  stringAttribute(document.Attributes, "effective_subject_id", ""),
		ApplicationID:       stringAttribute(document.Attributes, "application_id", ""),
		IngressPrincipal:    stringAttribute(document.Attributes, "ingress_principal", ""),
		TrustLevel:          stringAttribute(document.Attributes, "trust_level", ""),
		RequestID:           stringAttribute(document.Attributes, "request_id", ""),
		TraceID:             firstNonEmpty(document.TraceID, stringAttribute(document.Attributes, "trace_id", "")),
		SpanID:              firstNonEmpty(document.SpanID, stringAttribute(document.Attributes, "span_id", "")),
		ConversationID:      stringAttribute(document.Attributes, "conversation_id", ""),
		InteractionID:       stringAttribute(document.Attributes, "interaction_id", ""),
		OperationID:         stringAttribute(document.Attributes, "operation_id", ""),
		ToolName:            stringAttribute(document.Attributes, "tool_name", ""),
		ArtifactRef:         stringAttribute(document.Attributes, "artifact_ref", ""),
		KnowledgeNetworkIDs: stringSliceAttribute(document.Attributes, "knowledge_network_ids"),
		Attributes:          projectedAttributes(document.Attributes),
	}
	resourceType := stringAttribute(document.Attributes, "resource_type", "")
	resourceID := stringAttribute(document.Attributes, "resource_id", "")
	if resourceType != "" && resourceID != "" {
		record.ResourceRef = &observabilityvo.ResourceRef{ResourceType: resourceType, ResourceID: resourceID, Version: stringAttribute(document.Attributes, "resource_version", "")}
	}
	return record, nil
}

func stringAttribute(attributes map[string]any, key, fallback string) string {
	value, ok := attributes[key]
	if !ok {
		return fallback
	}
	text, ok := value.(string)
	if !ok {
		return fallback
	}
	return text
}

func stringSliceAttribute(attributes map[string]any, key string) []string {
	values, ok := attributes[key].([]any)
	if !ok {
		if typed, ok := attributes[key].([]string); ok {
			return append([]string(nil), typed...)
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func projectedAttributes(attributes map[string]any) map[string]any {
	allowed := []string{"business_context", "task_id", "causation_request_id", "linked_trace_id", "linked_span_id"}
	result := make(map[string]any)
	for _, key := range allowed {
		if value, ok := attributes[key]; ok {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func intersectValues(requested, allowed []string) []string {
	result := make([]string, 0)
	for _, value := range requested {
		for _, candidate := range allowed {
			if value == candidate {
				result = append(result, value)
				break
			}
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
