package opensearchruntimeaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

const (
	sourceID  = "bkn-trace-runtime"
	logPrefix = sourceID + ":"
)

type searchClient interface {
	Search(context.Context, string, []byte) ([]byte, error)
}

type Source struct {
	backend searchClient
	index   string
}

type searchResponse struct {
	Hits struct {
		Total struct {
			Value    int64  `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []struct {
			Source sessionvo.ReceiptProjectionDocument `json:"_source"`
			Sort   []any                               `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
}

func New(backend searchClient, index string) *Source {
	return &Source{backend: backend, index: index}
}

func (*Source) ID() string { return sourceID }

func (*Source) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: sourceID, Status: "healthy", Reliability: "durable",
		CollectionMethod: "projection_outbox", CoveredModules: []string{"domain_knowledge_network"},
		CountAccuracy: "exact", Categories: []string{observabilityvo.CategoryRuntimeBusiness},
	}
}

func (source *Source) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if query.AuthorizedTenantID == "" || query.AuthorizedBusinessDomain == "" {
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	}
	statuses, compatible := receiptStatuses(query)
	if !compatible || !matchesFixedFilters(query) {
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	}
	page, err := source.search(ctx, buildQuery(query, statuses, ""))
	if err == nil && query.Query != "" {
		page.CountAccuracy = "partial"
	}
	return page, err
}

func (source *Source) Get(ctx context.Context, logID string) (observabilityvo.LogRecord, bool, error) {
	receiptID := strings.TrimPrefix(logID, logPrefix)
	if receiptID == logID || receiptID == "" {
		return observabilityvo.LogRecord{}, false, nil
	}
	scope := observabilityvo.SourceAccessScopeFromContext(ctx)
	if scope.TenantID == "" || scope.BusinessDomain == "" {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("trusted tenant and business domain scope are required for receipt log detail")
	}
	page, err := source.search(ctx, buildQuery(observabilityvo.LogQuery{
		AuthorizedTenantID: scope.TenantID, AuthorizedBusinessDomain: scope.BusinessDomain, Limit: 1,
	}, []sessionvo.ReceiptStatus{sessionvo.ReceiptCompleted, sessionvo.ReceiptFailed}, receiptID))
	if err != nil || len(page.Records) == 0 {
		return observabilityvo.LogRecord{}, false, err
	}
	if page.Records[0].LogID != logID {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("receipt log detail returned mismatched log_id")
	}
	return page.Records[0], true, nil
}

func (source *Source) search(ctx context.Context, query map[string]any) (observabilityvo.SourcePage, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("encode receipt runtime log query: %w", err)
	}
	payload, err := source.backend.Search(ctx, source.index, body)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	var response searchResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode receipt runtime log response: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		record := projectReceipt(hit.Source)
		if len(hit.Sort) > 0 {
			record.CursorPosition = &observabilityvo.SourcePosition{
				EventTimestamp: record.EventTimestamp, SourceID: sourceID, LogID: record.SourceLogID,
				SearchAfter: append([]any(nil), hit.Sort...),
			}
		}
		records = append(records, record)
	}
	accuracy := "exact"
	if response.Hits.Total.Relation != "" && response.Hits.Total.Relation != "eq" {
		accuracy = "partial"
	}
	return observabilityvo.SourcePage{
		Records: records, Count: response.Hits.Total.Value, CountAccuracy: accuracy,
	}, nil
}

func buildQuery(query observabilityvo.LogQuery, statuses []sessionvo.ReceiptStatus, receiptID string) map[string]any {
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	filters := []any{
		map[string]any{"exists": map[string]any{"field": "receipt_id"}},
		term("owner.tenant_id.keyword", query.AuthorizedTenantID),
		term("owner.business_domain_id.keyword", query.AuthorizedBusinessDomain),
		terms("receipt_status.keyword", statuses),
	}
	addTerm := func(field, value string) {
		if value != "" {
			filters = append(filters, term(field, value))
		}
	}
	addTerm("receipt_id.keyword", receiptID)
	addTerm("trace_id.keyword", query.TraceID)
	addTerm("request_id.keyword", query.RequestID)
	addTerm("conversation_id.keyword", query.ConversationID)
	addTerm("interaction_id.keyword", query.InteractionID)
	addTerm("operation_id.keyword", firstNonEmpty(query.OperationID, query.TargetID))
	addTerm("owner.effective_subject_id.keyword", query.ActorID)
	addTerm("owner.application_principal_id.keyword", query.ApplicationID)
	if query.RequireRecordScope {
		candidates := make([]any, 0, 3)
		if query.AuthorizedSubjectID != "" {
			candidates = append(candidates, term("owner.effective_subject_id.keyword", query.AuthorizedSubjectID))
		}
		if query.AuthorizedApplicationID != "" {
			candidates = append(candidates, term("owner.application_principal_id.keyword", query.AuthorizedApplicationID))
		}
		if len(query.AuthorizedKnowledgeNetworkIDs) > 0 {
			candidates = append(candidates, terms("knowledge_network_ids.keyword", query.AuthorizedKnowledgeNetworkIDs))
		}
		if len(candidates) == 0 {
			filters = append(filters, map[string]any{"match_none": map[string]any{}})
		} else {
			filters = append(filters, map[string]any{"bool": map[string]any{
				"should": candidates, "minimum_should_match": 1,
			}})
		}
	}
	if query.TimeFrom != nil || query.TimeTo != nil {
		bounds := map[string]any{}
		if query.TimeFrom != nil {
			bounds["gte"] = query.TimeFrom.Format(time.RFC3339Nano)
		}
		if query.TimeTo != nil {
			bounds["lt"] = query.TimeTo.Format(time.RFC3339Nano)
		}
		filters = append(filters, map[string]any{"range": map[string]any{"terminal_at": bounds}})
	}
	if query.ObservedBefore != nil {
		filters = append(filters, map[string]any{"range": map[string]any{
			"terminal_at": map[string]any{"lte": query.ObservedBefore.Format(time.RFC3339Nano)},
		}})
	}
	result := map[string]any{
		"size": limit, "track_total_hits": true,
		"sort": []any{
			map[string]any{"terminal_at": map[string]any{"order": "desc"}},
			map[string]any{"receipt_id.keyword": map[string]any{"order": "asc"}},
		},
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
	}
	if query.PageBefore != nil && len(query.PageBefore.SearchAfter) > 0 {
		result["search_after"] = append([]any(nil), query.PageBefore.SearchAfter...)
	}
	return result
}

func matchesFixedFilters(query observabilityvo.LogQuery) bool {
	return query.SpanID == "" && query.ResourceType == "" && query.ResourceID == "" &&
		(query.OperationID == "" || query.TargetID == "" || query.OperationID == query.TargetID) &&
		(query.BusinessDomain == "" || query.BusinessDomain == query.AuthorizedBusinessDomain) &&
		(query.BusinessModule == "" || query.BusinessModule == "domain_knowledge_network") &&
		(query.Action == "" || query.Action == "execute") &&
		(query.TargetType == "" || query.TargetType == "operation") &&
		matchesOnly(query.Categories, observabilityvo.CategoryRuntimeBusiness) &&
		matchesOnly(query.Services, "bkn-trace-core") && matchesOnly(query.Environments, "unknown") &&
		matchesOnly(query.EventNames, "operation.executed") && query.SeverityMinimum <= 17
}

func receiptStatuses(query observabilityvo.LogQuery) ([]sessionvo.ReceiptStatus, bool) {
	wantSuccess := len(query.Outcomes) == 0 || contains(query.Outcomes, "success")
	wantFailure := len(query.Outcomes) == 0 || contains(query.Outcomes, "failure")
	if query.FailedOnly || query.SeverityMinimum > 9 {
		wantSuccess = false
	}
	statuses := make([]sessionvo.ReceiptStatus, 0, 2)
	if wantSuccess {
		statuses = append(statuses, sessionvo.ReceiptCompleted)
	}
	if wantFailure {
		statuses = append(statuses, sessionvo.ReceiptFailed)
	}
	return statuses, len(statuses) > 0
}

func projectReceipt(document sessionvo.ReceiptProjectionDocument) observabilityvo.LogRecord {
	timestamp := document.IssuedAt
	if document.TerminalAt != nil {
		timestamp = *document.TerminalAt
	}
	outcome, severityNumber, severityText := "success", 9, "INFO"
	if document.Status == sessionvo.ReceiptFailed {
		outcome, severityNumber, severityText = "failure", 17, "ERROR"
	}
	targetName := firstNonEmpty(document.ToolName, document.OperationID)
	return observabilityvo.LogRecord{
		EventID: "operation.executed:" + document.ID, EventTime: timestamp, RecordedAt: timestamp,
		ActorNameSnapshot: document.Owner.EffectiveSubjectID, ActorType: actorType(document.Owner.EffectiveSubjectType),
		AuthMethod: "unknown", SourceChannel: "api", BusinessModule: "domain_knowledge_network", Action: "execute",
		TargetType: "operation", TargetID: document.OperationID, TargetNameSnapshot: targetName,
		SchemaVersion: "1.0", LogID: logPrefix + document.ID, SourceID: sourceID, SourceLogID: document.ID,
		Category: observabilityvo.CategoryRuntimeBusiness, EventName: "operation.executed",
		EventTimestamp: timestamp, ObservedTimestamp: timestamp,
		SeverityNumber: severityNumber, SeverityText: severityText, Outcome: outcome,
		SafeSummary: "Executed " + targetName, ServiceName: "bkn-trace-core", Environment: "unknown",
		TenantID: document.Owner.TenantID, BusinessDomain: document.Owner.BusinessDomainID,
		ActorID: document.Owner.EffectiveSubjectID, EffectiveSubjectID: document.Owner.EffectiveSubjectID,
		ApplicationID: document.Owner.ApplicationPrincipalID, IngressPrincipal: "bkn-trace-core", TrustLevel: "trusted",
		RequestID: document.RequestID, TraceID: document.TraceID, ConversationID: document.ConversationID,
		InteractionID: document.InteractionID, OperationID: document.OperationID, ToolName: document.ToolName,
		KnowledgeNetworkIDs: append([]string(nil), document.KnowledgeNetworkIDs...),
		Attributes: map[string]any{
			"operation_type": "tool.execute", "operation_status": string(document.Status),
			"receipt_id": document.ID, "attempt": document.Attempt,
		},
	}
}

func term(field string, value any) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

func terms(field string, values any) map[string]any {
	return map[string]any{"terms": map[string]any{field: values}}
}

func matchesOnly(values []string, supported string) bool {
	return len(values) == 0 || contains(values, supported)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func actorType(subjectType sessionvo.SubjectType) string {
	if subjectType == sessionvo.SubjectService {
		return "service_account"
	}
	return "user"
}
