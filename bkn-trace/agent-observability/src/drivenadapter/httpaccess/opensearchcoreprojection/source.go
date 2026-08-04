package opensearchcoreprojection

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

const maxProjectionDocuments = 10000

type Source struct {
	client    *opensearch.Client
	index     string
	artifacts iprojectionsource.ProjectionSourcePort
}

func New(client *opensearch.Client, index string, artifacts iprojectionsource.ProjectionSourcePort) *Source {
	return &Source{client: client, index: index, artifacts: artifacts}
}

type receiptDocument struct {
	ReceiptID          string                  `json:"receipt_id"`
	SchemaVersion      string                  `json:"schema_version"`
	Owner              sessionvo.Owner         `json:"owner"`
	ConversationID     string                  `json:"conversation_id"`
	InteractionID      string                  `json:"interaction_id"`
	OperationID        string                  `json:"operation_id"`
	OperationKey       string                  `json:"operation_key"`
	ToolName           string                  `json:"tool_name"`
	Status             string                  `json:"receipt_status"`
	EvidenceDurability string                  `json:"evidence_durability"`
	RequestID          string                  `json:"request_id"`
	TraceID            string                  `json:"trace_id"`
	BusinessRefs       []sessionvo.BusinessRef `json:"business_refs"`
	ArtifactRefs       []string                `json:"artifact_refs"`
	PartialReasons     []string                `json:"partial_reasons"`
	IssuedAt           string                  `json:"issued_at"`
	TerminalAt         string                  `json:"terminal_at"`
}

func (s *Source) LoadExecutionProjection(ctx context.Context, query iprojectionsource.Query) (iprojectionsource.Result, error) {
	receipts, truncated, err := s.loadReceipts(ctx, query)
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	artifactResult := iprojectionsource.Result{}
	if s.artifacts != nil {
		artifactQuery := query
		if query.RequestID != "" {
			if interactionID := stableReceiptInteraction(receipts); interactionID != "" {
				artifactQuery.RequestID = ""
				artifactQuery.InteractionID = interactionID
			}
		}
		artifactResult, err = s.artifacts.LoadExecutionProjection(ctx, artifactQuery)
		if err != nil {
			return iprojectionsource.Result{}, err
		}
	}
	traces := tracesFromReceipts(receipts)
	artifacts := artifactsForTraces(artifactResult.Artifacts, traces)
	attachArtifactEvents(traces, artifacts)
	return iprojectionsource.Result{
		Traces: traces, Artifacts: artifacts,
		Truncated: truncated || artifactResult.Truncated,
	}, nil
}

func stableReceiptInteraction(receipts []receiptDocument) string {
	interactionID := ""
	for _, receipt := range receipts {
		if receipt.InteractionID == "" {
			continue
		}
		if interactionID == "" {
			interactionID = receipt.InteractionID
			continue
		}
		if interactionID != receipt.InteractionID {
			return ""
		}
	}
	return interactionID
}

func (s *Source) loadReceipts(ctx context.Context, query iprojectionsource.Query) ([]receiptDocument, bool, error) {
	size := maxProjectionDocuments
	if query.Limit > 0 && query.Limit*20+1 < size {
		size = query.Limit*20 + 1
	}
	must := []map[string]any{{"exists": map[string]any{"field": "receipt_id"}}}
	for field, value := range map[string]string{
		"owner.tenant_id":          query.Scope.TenantID,
		"owner.business_domain_id": firstNonEmpty(query.BusinessDomain, query.Scope.BusinessDomain),
		"request_id":               query.RequestID,
		"trace_id":                 query.TraceID,
		"interaction_id":           query.InteractionID,
	} {
		if value != "" {
			must = append(must, exactKeywordQuery(field, value))
		}
	}
	if !query.From.IsZero() || !query.To.IsZero() {
		bounds := map[string]any{}
		if !query.From.IsZero() {
			bounds["gte"] = query.From
		}
		if !query.To.IsZero() {
			bounds["lte"] = query.To
		}
		must = append(must, map[string]any{"range": map[string]any{"issued_at": bounds}})
	}
	body, err := json.Marshal(map[string]any{
		"size":  size,
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"sort":  []map[string]any{{"issued_at": map[string]any{"order": "desc"}}, {"_id": map[string]any{"order": "desc"}}},
	})
	if err != nil {
		return nil, false, err
	}
	responseBody, err := s.client.Search(ctx, s.index, body)
	if err != nil {
		return nil, false, err
	}
	var response struct {
		Hits struct {
			Hits []struct {
				Source receiptDocument `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, false, fmt.Errorf("decode Core receipt projection: %w", err)
	}
	result := make([]receiptDocument, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		if receiptMatchesScope(hit.Source, query.Scope) {
			result = append(result, hit.Source)
		}
	}
	return result, len(response.Hits.Hits) >= size, nil
}

func exactKeywordQuery(field string, value string) map[string]any {
	return map[string]any{"term": map[string]any{field + ".keyword": map[string]any{"value": value}}}
}

func receiptMatchesScope(receipt receiptDocument, scope evidencevo.QueryScope) bool {
	networks := knowledgeNetworks(receipt.BusinessRefs)
	record := evidencevo.RecordScope{
		TenantID: receipt.Owner.TenantID, BusinessDomain: receipt.Owner.BusinessDomainID,
		EffectiveSubjectID:     receipt.Owner.EffectiveSubjectID,
		ApplicationPrincipalID: receipt.Owner.ApplicationPrincipalID,
		KnowledgeNetworkIDs:    networks,
	}
	if scope.AccessProfile != nil {
		return evidencevo.CanReadRecord(*scope.AccessProfile, record, evidencevo.AccessViewBusiness)
	}
	return receipt.Owner.TenantID == scope.TenantID &&
		receipt.Owner.BusinessDomainID == scope.BusinessDomain &&
		string(receipt.Owner.EffectiveSubjectType) == scope.AccountType &&
		receipt.Owner.EffectiveSubjectID == scope.AccountID
}

func tracesFromReceipts(receipts []receiptDocument) []evidencevo.NormalizedTrace {
	byTrace := map[string]*evidencevo.NormalizedTrace{}
	for _, receipt := range receipts {
		if receipt.RequestID == "" || receipt.TraceID == "" {
			continue
		}
		trace := byTrace[receipt.TraceID]
		if trace == nil {
			value := evidencevo.NormalizedTrace{
				TraceID: receipt.TraceID, RequestID: receipt.RequestID,
				ConversationID: receipt.ConversationID, TenantID: receipt.Owner.TenantID,
				BusinessDomain:         receipt.Owner.BusinessDomainID,
				AccountID:              receipt.Owner.EffectiveSubjectID,
				AccountType:            string(receipt.Owner.EffectiveSubjectType),
				EffectiveSubjectID:     receipt.Owner.EffectiveSubjectID,
				ApplicationPrincipalID: receipt.Owner.ApplicationPrincipalID,
				KnowledgeNetworkIDs:    knowledgeNetworks(receipt.BusinessRefs),
				SchemaVersion:          receipt.SchemaVersion,
			}
			trace = &value
			byTrace[receipt.TraceID] = trace
		}
		businessRefs := make([]any, 0, len(receipt.BusinessRefs))
		for _, ref := range receipt.BusinessRefs {
			businessRefs = append(businessRefs, map[string]any{
				"ref_id": ref.RefID, "ref_type": ref.RefType, "visibility": "visible",
			})
		}
		terminalAt := firstNonEmpty(receipt.TerminalAt, receipt.IssuedAt)
		trace.Events = append(trace.Events, evidencevo.EvidenceEvent{
			EventID: "receipt:" + receipt.ReceiptID, EventType: "retrieval.completed",
			SchemaVersion: receipt.SchemaVersion, ObservedAt: receipt.IssuedAt, EmittedAt: terminalAt,
			Producer: receipt.Owner.ApplicationPrincipalID, TraceID: receipt.TraceID,
			RequestID: receipt.RequestID, OperationName: receipt.ToolName,
			InteractionID: receipt.InteractionID, OperationID: receipt.OperationID,
			Payload: map[string]any{
				"status": receipt.Status, "evidence_durability": receipt.EvidenceDurability,
				"operation_key": receipt.OperationKey,
				"business_refs": businessRefs, "partial_reasons": receipt.PartialReasons,
			},
		})
	}
	result := make([]evidencevo.NormalizedTrace, 0, len(byTrace))
	for _, trace := range byTrace {
		sort.Slice(trace.Events, func(i, j int) bool { return trace.Events[i].ObservedAt < trace.Events[j].ObservedAt })
		result = append(result, *trace)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TraceID < result[j].TraceID })
	return result
}

func artifactsForTraces(artifacts []evidencevo.EvidenceArtifact, traces []evidencevo.NormalizedTrace) []evidencevo.EvidenceArtifact {
	requests := map[string]struct{}{}
	interactions := map[string][]evidencevo.NormalizedTrace{}
	for _, trace := range traces {
		requests[trace.RequestID] = struct{}{}
		for _, event := range trace.Events {
			if event.InteractionID != "" {
				interactions[event.InteractionID] = append(interactions[event.InteractionID], trace)
			}
		}
	}
	result := make([]evidencevo.EvidenceArtifact, 0)
	for _, artifact := range artifacts {
		if _, ok := requests[artifact.RequestID]; ok {
			result = append(result, artifact)
			continue
		}
		if artifact.OperationID != "" {
			continue
		}
		for _, trace := range interactions[artifact.InteractionID] {
			projected := artifact
			projected.RequestID = trace.RequestID
			projected.TraceID = trace.TraceID
			result = append(result, projected)
		}
	}
	return result
}

func attachArtifactEvents(traces []evidencevo.NormalizedTrace, artifacts []evidencevo.EvidenceArtifact) {
	byTrace := map[string]*evidencevo.NormalizedTrace{}
	for index := range traces {
		byTrace[traces[index].TraceID] = &traces[index]
	}
	for _, artifact := range artifacts {
		trace := byTrace[artifact.TraceID]
		if trace == nil || artifact.InteractionID == "" {
			continue
		}
		eventType, field := artifactLink(artifact.ArtifactType)
		if eventType == "" {
			continue
		}
		trace.Events = append(trace.Events, evidencevo.EvidenceEvent{
			EventID: "artifact-link:" + artifact.ArtifactID, EventType: eventType,
			SchemaVersion: artifact.SchemaVersion, ObservedAt: artifact.ObservedAt,
			EmittedAt: artifact.ObservedAt, Producer: artifact.AgentOrApp,
			TraceID: artifact.TraceID, RequestID: artifact.RequestID,
			InteractionID: artifact.InteractionID, OperationID: artifact.OperationID,
			Payload: map[string]any{field: "artifact:" + artifact.ArtifactID},
		})
	}
}

func artifactLink(artifactType evidencevo.ArtifactType) (string, string) {
	switch artifactType {
	case evidencevo.ArtifactTypeQuestion:
		return "agent.interaction.started", "question_artifact_ref"
	case evidencevo.ArtifactTypeResult:
		return "claim.created", "result_artifact_ref"
	case evidencevo.ArtifactTypeDataResult:
		return "data.query.observed", "result_artifact_ref"
	case evidencevo.ArtifactTypeLogicExecution:
		return "logic.execution.observed", "result_artifact_ref"
	case evidencevo.ArtifactTypeActionResult:
		return "action.result_recorded", "result_artifact_ref"
	default:
		return "", ""
	}
}

func knowledgeNetworks(refs []sessionvo.BusinessRef) []string {
	set := map[string]struct{}{}
	for _, ref := range refs {
		parts := strings.Split(ref.RefID, ":")
		if len(parts) >= 2 && parts[1] != "" && parts[0] != "resource" {
			set[parts[1]] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
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
