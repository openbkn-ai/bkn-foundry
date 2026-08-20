package opensearchconversationaudit

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
	sourceID    = "bkn-trace-core"
	eventPrefix = "conversation.created:"
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
			ID     string                 `json:"_id"`
			Source sessionvo.Conversation `json:"_source"`
			Sort   []any                  `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
}

func New(backend searchClient, index string) *Source {
	return &Source{backend: backend, index: index}
}

func (source *Source) ID() string { return sourceID }

func (source *Source) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: sourceID, Status: "healthy", Reliability: "durable",
		CollectionMethod: "projection_outbox", CoveredModules: []string{"domain_knowledge_network"},
		CountAccuracy: "exact", Categories: []string{observabilityvo.CategoryRuntimeBusiness},
	}
}

func (source *Source) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if !matchesFixedFilters(query) || query.TraceID != "" || query.SpanID != "" || query.InteractionID != "" || query.OperationID != "" {
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	}
	body, err := json.Marshal(buildQuery(query))
	if err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("encode conversation audit query: %w", err)
	}
	payload, err := source.backend.Search(ctx, source.index, body)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	var response searchResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode conversation audit response: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		record := projectConversation(hit.Source)
		if len(hit.Sort) > 0 {
			record.CursorPosition = &observabilityvo.SourcePosition{
				EventTimestamp: record.EventTimestamp, SourceID: sourceID, LogID: record.EventID,
				SearchAfter: append([]any(nil), hit.Sort...),
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

func (source *Source) Get(ctx context.Context, eventID string) (observabilityvo.LogRecord, bool, error) {
	conversationID := strings.TrimPrefix(eventID, eventPrefix)
	if conversationID == eventID || conversationID == "" {
		return observabilityvo.LogRecord{}, false, nil
	}
	scope := observabilityvo.SourceAccessScopeFromContext(ctx)
	page, err := source.Search(ctx, observabilityvo.LogQuery{
		AuthorizedTenantID: scope.TenantID, AuthorizedBusinessDomain: scope.BusinessDomain,
		ConversationID: conversationID, Limit: 1,
	})
	if err != nil || len(page.Records) == 0 {
		return observabilityvo.LogRecord{}, false, err
	}
	return page.Records[0], true, nil
}

func buildQuery(query observabilityvo.LogQuery) map[string]any {
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	filters := []any{
		map[string]any{"exists": map[string]any{"field": "external_conversation_key"}},
		map[string]any{"exists": map[string]any{"field": "generation"}},
	}
	addTerm := func(field, value string) {
		if value != "" {
			filters = append(filters, map[string]any{"term": map[string]any{field: value}})
		}
	}
	addTerm("owner.tenant_id.keyword", query.AuthorizedTenantID)
	addTerm("owner.business_domain_id.keyword", query.AuthorizedBusinessDomain)
	addTerm("owner.effective_subject_id.keyword", query.ActorID)
	addTerm("conversation_id.keyword", firstNonEmpty(query.ConversationID, query.TargetID))
	if query.TimeFrom != nil || query.TimeTo != nil {
		bounds := map[string]any{}
		if query.TimeFrom != nil {
			bounds["gte"] = query.TimeFrom.Format(time.RFC3339Nano)
		}
		if query.TimeTo != nil {
			bounds["lt"] = query.TimeTo.Format(time.RFC3339Nano)
		}
		filters = append(filters, map[string]any{"range": map[string]any{"created_at": bounds}})
	}
	result := map[string]any{
		"size": limit, "track_total_hits": true,
		"sort":  []any{map[string]any{"created_at": map[string]any{"order": "desc"}}, map[string]any{"conversation_id.keyword": map[string]any{"order": "asc"}}},
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
	}
	if query.PageBefore != nil && len(query.PageBefore.SearchAfter) > 0 {
		result["search_after"] = append([]any(nil), query.PageBefore.SearchAfter...)
	}
	return result
}

func matchesFixedFilters(query observabilityvo.LogQuery) bool {
	return (query.BusinessModule == "" || query.BusinessModule == "domain_knowledge_network") &&
		(query.Action == "" || query.Action == "create") &&
		(query.TargetType == "" || query.TargetType == "conversation") &&
		(len(query.Outcomes) == 0 || contains(query.Outcomes, "success"))
}

func projectConversation(conversation sessionvo.Conversation) observabilityvo.LogRecord {
	eventID := eventPrefix + conversation.ID
	targetName := firstNonEmpty(conversation.AgentName, "Agent business conversation")
	return observabilityvo.LogRecord{
		EventID: eventID, EventTime: conversation.CreatedAt, RecordedAt: conversation.CreatedAt,
		ActorNameSnapshot: firstNonEmpty(conversation.ActorNameSnapshot, conversation.Owner.EffectiveSubjectID), ActorType: actorType(conversation.Owner.EffectiveSubjectType),
		AuthMethod: firstNonEmpty(conversation.CreationAuthMethod, "unknown"), SourceChannel: "api", BusinessModule: "domain_knowledge_network", Action: "create",
		TargetType: "conversation", TargetID: conversation.ID, TargetNameSnapshot: targetName,
		SchemaVersion: "1.0", LogID: sourceID + ":" + eventID, SourceID: sourceID, SourceLogID: eventID,
		Category: observabilityvo.CategoryRuntimeBusiness, EventName: "conversation.created",
		EventTimestamp: conversation.CreatedAt, ObservedTimestamp: conversation.CreatedAt,
		SeverityNumber: 9, SeverityText: "INFO", Outcome: "success", SafeSummary: "Started an Agent business conversation",
		ServiceName: "bkn-trace-core", Environment: "unknown", TenantID: conversation.Owner.TenantID,
		BusinessDomain: conversation.Owner.BusinessDomainID, ActorID: conversation.Owner.EffectiveSubjectID,
		EffectiveSubjectID: conversation.Owner.EffectiveSubjectID,
		ApplicationID:      conversation.Owner.ApplicationPrincipalID, IngressPrincipal: "bkn-trace-core", TrustLevel: "trusted",
		ConversationID: conversation.ID, RequestID: conversation.CreationRequestID,
		Attributes: map[string]any{
			"operation_type": "conversation.create", "operation_status": "completed",
			"business_context":          firstNonEmpty(conversation.BusinessContext, "managed"),
			"agent_name":                strings.TrimSpace(conversation.AgentName),
			"external_conversation_key": strings.TrimSpace(conversation.ExternalConversationKey),
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func actorType(subjectType sessionvo.SubjectType) string {
	if subjectType == sessionvo.SubjectService {
		return "service_account"
	}
	return "user"
}
