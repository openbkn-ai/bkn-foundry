package opensearchlogaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

const sourceID = "otel-runtime"

type openSearchClient interface {
	Search(context.Context, string, []byte) ([]byte, error)
	GetDocument(context.Context, string, string) (opensearch.Document, error)
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
		SourceID: sourceID, Status: "available", Reliability: "best_effort",
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
	var response struct {
		Hits struct {
			Total struct {
				Value    int64  `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				ID     string          `json:"_id"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode OpenSearch log response: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		record, err := mapDocument(hit.ID, hit.Source)
		if err != nil {
			return observabilityvo.SourcePage{}, err
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
	document, err := client.backend.GetDocument(ctx, client.index, logID)
	if errors.Is(err, opensearch.ErrDocumentNotFound) {
		return observabilityvo.LogRecord{}, false, nil
	}
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	record, err := mapDocument(logID, document.Source)
	return record, err == nil, err
}

func buildQuery(query observabilityvo.LogQuery) map[string]any {
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	filters := make([]any, 0, 16)
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
	addTerm("attributes.business_domain_id.keyword", query.AuthorizedBusinessDomain)
	addTerm("attributes.effective_subject_id.keyword", query.AuthorizedSubjectID)
	authorizedCategories := intersectValues(query.Categories, query.AuthorizedCategories)
	if len(query.Categories) == 0 {
		authorizedCategories = query.AuthorizedCategories
	}
	addTerms("attributes.log_category.keyword", authorizedCategories)
	for _, networkID := range query.AuthorizedKnowledgeNetworkIDs {
		addTerm("attributes.knowledge_network_ids.keyword", networkID)
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
	addTerms("attributes.event_name.keyword", query.EventNames)
	if query.TimeFrom != nil || query.TimeTo != nil {
		bounds := make(map[string]any)
		if query.TimeFrom != nil {
			bounds["gte"] = query.TimeFrom.Format(time.RFC3339Nano)
		}
		if query.TimeTo != nil {
			bounds["lte"] = query.TimeTo.Format(time.RFC3339Nano)
		}
		filters = append(filters, map[string]any{"range": map[string]any{"@timestamp": bounds}})
	}
	if query.SeverityMinimum > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{"severity.number": map[string]any{"gte": query.SeverityMinimum}}})
	}
	if query.FailedOnly {
		addTerm("attributes.outcome.keyword", "failure")
	}
	boolQuery := map[string]any{"filter": filters}
	if strings.TrimSpace(query.Query) != "" {
		boolQuery["must"] = []any{map[string]any{"match_phrase": map[string]any{"attributes.safe_summary": query.Query}}}
	}
	return map[string]any{
		"size": limit, "track_total_hits": true,
		"sort":  []any{map[string]any{"observedTimestamp": map[string]any{"order": "desc"}}, map[string]any{"_id": map[string]any{"order": "desc"}}},
		"query": map[string]any{"bool": boolQuery},
	}
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
		SchemaVersion: stringAttribute(document.Attributes, "schema_version", "1.0.0"),
		LogID:         id, SourceID: stringAttribute(document.Attributes, "source_id", ""), SourceLogID: id,
		Category:       stringAttribute(document.Attributes, "log_category", ""),
		EventName:      stringAttribute(document.Attributes, "event_name", ""),
		EventTimestamp: eventTimestamp, ObservedTimestamp: observedTimestamp,
		SeverityNumber: document.Severity.Number, SeverityText: document.Severity.Text,
		Outcome:             stringAttribute(document.Attributes, "outcome", ""),
		SafeSummary:         stringAttribute(document.Attributes, "safe_summary", ""),
		ServiceName:         stringAttribute(document.Resource, "service.name", stringAttribute(document.Attributes, "service.name", "")),
		Environment:         stringAttribute(document.Resource, "deployment.environment", ""),
		TenantID:            stringAttribute(document.Attributes, "tenant_id", ""),
		BusinessDomain:      stringAttribute(document.Attributes, "business_domain_id", ""),
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
