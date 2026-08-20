package opensearchruntimeaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

const sourceID = "bkn-trace-runtime"

type searchClient interface {
	Search(context.Context, string, []byte) ([]byte, error)
}
type Source struct {
	backend searchClient
	index   string
}

func New(backend searchClient, index string) *Source { return &Source{backend: backend, index: index} }
func (*Source) ID() string                           { return sourceID }
func (*Source) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{SourceID: sourceID, Status: "healthy", Reliability: "durable", CollectionMethod: "projection_outbox", CoveredModules: []string{"bkn_trace"}, CountAccuracy: "exact", Categories: []string{observabilityvo.CategoryRuntimeBusiness}}
}

type response struct {
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

func (s *Source) Search(ctx context.Context, q observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if q.AuthorizedTenantID == "" || q.AuthorizedBusinessDomain == "" {
		return observabilityvo.SourcePage{}, nil
	}
	if q.SpanID != "" {
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	}
	filters := []any{map[string]any{"exists": map[string]any{"field": "receipt_id"}}, term("owner.tenant_id.keyword", q.AuthorizedTenantID), term("owner.business_domain_id.keyword", q.AuthorizedBusinessDomain)}
	for _, pair := range [][2]string{{"trace_id", q.TraceID}, {"request_id", q.RequestID}, {"conversation_id", q.ConversationID}, {"interaction_id", q.InteractionID}, {"operation_id", q.OperationID}} {
		if pair[1] != "" {
			filters = append(filters, term(pair[0], pair[1]))
		}
	}
	if q.TimeFrom != nil || q.TimeTo != nil {
		bounds := map[string]any{}
		if q.TimeFrom != nil {
			bounds["gte"] = q.TimeFrom.Format(time.RFC3339Nano)
		}
		if q.TimeTo != nil {
			bounds["lt"] = q.TimeTo.Format(time.RFC3339Nano)
		}
		filters = append(filters, map[string]any{"range": map[string]any{"issued_at": bounds}})
	}
	body, _ := json.Marshal(map[string]any{"size": 200, "track_total_hits": true, "sort": []any{map[string]any{"issued_at": map[string]any{"order": "desc"}}}, "query": map[string]any{"bool": map[string]any{"filter": filters}}})
	payload, err := s.backend.Search(ctx, s.index, body)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	var r response
	if err := json.Unmarshal(payload, &r); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode runtime log projection: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(r.Hits.Hits))
	for _, hit := range r.Hits.Hits {
		d := hit.Source
		outcome := "success"
		if d.Status == sessionvo.ReceiptFailed {
			outcome = "failure"
		}
		record := observabilityvo.LogRecord{EventID: "operation.executed:" + d.ID, SchemaVersion: "1.0", LogID: sourceID + ":" + d.ID, SourceID: sourceID, SourceLogID: d.ID, Category: observabilityvo.CategoryRuntimeBusiness, EventName: "operation.executed", EventTimestamp: d.IssuedAt, ObservedTimestamp: d.IssuedAt, SeverityNumber: 9, SeverityText: "INFO", Outcome: outcome, SafeSummary: "executed " + d.ToolName, ServiceName: "bkn-trace-core", Environment: "unknown", TenantID: d.Owner.TenantID, BusinessDomain: d.Owner.BusinessDomainID, ActorID: d.Owner.EffectiveSubjectID, EffectiveSubjectID: d.Owner.EffectiveSubjectID, ApplicationID: d.Owner.ApplicationPrincipalID, IngressPrincipal: "bkn-trace-core", TrustLevel: "trusted", RequestID: d.RequestID, TraceID: d.TraceID, ConversationID: d.ConversationID, InteractionID: d.InteractionID, OperationID: d.OperationID, ToolName: d.ToolName, KnowledgeNetworkIDs: d.KnowledgeNetworkIDs}
		records = append(records, record)
	}
	return observabilityvo.SourcePage{Records: records, Count: r.Hits.Total.Value, CountAccuracy: "exact"}, nil
}
func term(field, value string) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}
