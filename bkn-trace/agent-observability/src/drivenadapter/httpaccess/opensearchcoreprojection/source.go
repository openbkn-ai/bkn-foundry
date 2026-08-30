// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchcoreprojection

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

const maxProjectionDocuments = 10000
const maxReceiptsPerSelectedIdentity = 20

type Source struct {
	client    *opensearch.Client
	index     string
	artifacts iprojectionsource.ProjectionSourcePort
}

func New(client *opensearch.Client, index string, artifacts iprojectionsource.ProjectionSourcePort) *Source {
	return &Source{client: client, index: index, artifacts: artifacts}
}

type receiptDocument struct {
	ReceiptID           string                  `json:"receipt_id"`
	SchemaVersion       string                  `json:"schema_version"`
	Owner               sessionvo.Owner         `json:"owner"`
	ConversationID      string                  `json:"conversation_id"`
	InteractionID       string                  `json:"interaction_id"`
	OperationID         string                  `json:"operation_id"`
	OperationKey        string                  `json:"operation_key"`
	ToolName            string                  `json:"tool_name"`
	Status              string                  `json:"receipt_status"`
	EvidenceDurability  string                  `json:"evidence_durability"`
	RequestID           string                  `json:"request_id"`
	TraceID             string                  `json:"trace_id"`
	BusinessRefs        []sessionvo.BusinessRef `json:"business_refs"`
	ArtifactRefs        []string                `json:"artifact_refs"`
	PartialReasons      []string                `json:"partial_reasons"`
	KnowledgeNetworkIDs []string                `json:"knowledge_network_ids"`
	IssuedAt            string                  `json:"issued_at"`
	TerminalAt          string                  `json:"terminal_at"`
}

func (s *Source) LoadExecutionProjection(ctx context.Context, query iprojectionsource.Query) (iprojectionsource.Result, error) {
	receipts, authorizedInteractions, truncated, err := s.loadReceipts(ctx, query)
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	artifactResult := iprojectionsource.Result{}
	if s.artifacts != nil {
		artifactQuery := query
		if len(authorizedInteractions) > 0 {
			artifactQuery.RequestID = ""
			artifactQuery.TraceID = ""
			artifactQuery.InteractionID = ""
			artifactQuery.AuthorizedInteractionIDs = authorizedInteractions
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

func (s *Source) loadReceipts(ctx context.Context, query iprojectionsource.Query) ([]receiptDocument, []string, bool, error) {
	candidates, truncated, err := s.searchReceipts(ctx, query, !hasExactReceiptSelector(query))
	if err != nil {
		return nil, nil, false, err
	}
	if hasBatchIdentitySelector(query) {
		result := make([]receiptDocument, 0, len(candidates))
		interactionSet := make(map[string]struct{})
		for _, receipt := range candidates {
			if !receiptMatchesScope(receipt, query.Scope) || !matchesReceiptQuery(receipt, query) {
				continue
			}
			result = append(result, receipt)
			if receipt.InteractionID != "" {
				interactionSet[receipt.InteractionID] = struct{}{}
			}
		}
		interactions := make([]string, 0, len(interactionSet))
		for interactionID := range interactionSet {
			interactions = append(interactions, interactionID)
		}
		sort.Strings(interactions)
		return result, interactions, truncated, nil
	}
	interactionIDs := receiptInteractionIDs(candidates)
	interactionReceipts, interactionTruncated, err := s.loadInteractionReceipts(ctx, query, interactionIDs)
	if err != nil {
		return nil, nil, false, err
	}
	truncated = truncated || interactionTruncated
	byInteraction := receiptsByInteraction(interactionReceipts)
	result := make([]receiptDocument, 0, len(candidates)+len(interactionReceipts))
	authorizedInteractions := make([]string, 0, len(byInteraction))
	for interactionID, receipts := range byInteraction {
		if !interactionMatchesScope(receipts, query.Scope) {
			continue
		}
		authorizedInteractions = append(authorizedInteractions, interactionID)
		for _, receipt := range receipts {
			if matchesReceiptQuery(receipt, query) {
				result = append(result, receipt)
			}
		}
	}
	for _, receipt := range candidates {
		if receipt.InteractionID == "" && receiptMatchesScope(receipt, query.Scope) {
			result = append(result, receipt)
		}
	}
	sort.Strings(authorizedInteractions)
	sort.Slice(result, func(i, j int) bool {
		if result[i].IssuedAt == result[j].IssuedAt {
			return result[i].ReceiptID < result[j].ReceiptID
		}
		return result[i].IssuedAt < result[j].IssuedAt
	})
	return result, authorizedInteractions, truncated, nil
}

func (s *Source) searchReceipts(ctx context.Context, query iprojectionsource.Query, applyScopeCandidates bool) ([]receiptDocument, bool, error) {
	size := maxProjectionDocuments
	if query.Limit > 0 && query.Limit < size {
		// Limit is the projection caller's candidate budget. Fetch one extra
		// document only to preserve the existing truncation signal; expanding the
		// budget here defeats bounded summary scans and makes a 2,000-entry request
		// read 10,000 receipts from OpenSearch.
		size = query.Limit + 1
	}
	must := []map[string]any{{"exists": map[string]any{"field": "receipt_id"}}}
	if hasBatchIdentitySelector(query) {
		must = append(must,
			map[string]any{"exists": map[string]any{"field": "trace_id"}},
			map[string]any{"exists": map[string]any{"field": "request_id"}},
		)
	}
	for field, value := range map[string]string{
		"owner.tenant_id": query.Scope.TenantID,
		"request_id":      query.RequestID,
		"trace_id":        query.TraceID,
		"interaction_id":  query.InteractionID,
	} {
		if value != "" {
			must = append(must, exactKeywordQuery(field, value))
		}
	}
	if len(query.TraceIDs) > 0 {
		must = append(must, map[string]any{"terms": map[string]any{"trace_id.keyword": query.TraceIDs}})
	}
	if len(query.ConversationIDs) > 0 {
		must = append(must, map[string]any{"terms": map[string]any{"conversation_id.keyword": query.ConversationIDs}})
	}
	if applyScopeCandidates {
		must = append(must, receiptScopeCandidates(query.Scope)...)
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
	queryBody := map[string]any{
		"size":  size,
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"sort":  []map[string]any{{"issued_at": map[string]any{"order": "desc"}}, {"_id": map[string]any{"order": "desc"}}},
	}
	innerHitName := ""
	if field, identityCount := batchIdentityCollapse(query); identityCount > 1 {
		innerHitName = "selected_receipts"
		queryBody["size"] = identityCount
		queryBody["collapse"] = map[string]any{
			"field": field,
			"inner_hits": map[string]any{
				"name": innerHitName, "size": maxReceiptsPerSelectedIdentity,
				"sort": []map[string]any{{"issued_at": map[string]any{"order": "desc"}}, {"_id": map[string]any{"order": "desc"}}},
			},
		}
	}
	body, err := json.Marshal(queryBody)
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
				Source    receiptDocument `json:"_source"`
				InnerHits map[string]struct {
					Hits struct {
						Total struct {
							Value int `json:"value"`
						} `json:"total"`
						Hits []struct {
							Source receiptDocument `json:"_source"`
						} `json:"hits"`
					} `json:"hits"`
				} `json:"inner_hits"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, false, fmt.Errorf("decode Core receipt projection: %w", err)
	}
	result := make([]receiptDocument, 0, len(response.Hits.Hits))
	truncated := false
	for _, hit := range response.Hits.Hits {
		if innerHitName != "" {
			inner := hit.InnerHits[innerHitName].Hits
			for _, innerHit := range inner.Hits {
				result = append(result, innerHit.Source)
			}
			if inner.Total.Value > len(inner.Hits) {
				truncated = true
			}
			continue
		}
		result = append(result, hit.Source)
	}
	if innerHitName != "" {
		return result, truncated, nil
	}
	return result, len(response.Hits.Hits) >= size, nil
}

func (s *Source) loadInteractionReceipts(ctx context.Context, query iprojectionsource.Query, interactionIDs []string) ([]receiptDocument, bool, error) {
	if len(interactionIDs) == 0 {
		return nil, false, nil
	}
	interactionQuery := query
	interactionQuery.RequestID = ""
	interactionQuery.TraceID = ""
	interactionQuery.InteractionID = ""
	interactionQuery.From = time.Time{}
	interactionQuery.To = time.Time{}
	return s.searchReceiptsForInteractions(ctx, interactionQuery, interactionIDs)
}

func (s *Source) searchReceiptsForInteractions(ctx context.Context, query iprojectionsource.Query, interactionIDs []string) ([]receiptDocument, bool, error) {
	size := maxProjectionDocuments
	if query.Limit > 0 && query.Limit < size {
		// Keep the follow-up expansion within the same caller-owned candidate
		// budget as the initial receipt search.  A list request must never turn
		// a page-sized candidate query into a fixed 10k OpenSearch read.
		size = query.Limit + 1
	}
	must := []map[string]any{{"exists": map[string]any{"field": "receipt_id"}}}
	for field, value := range map[string]string{
		"owner.tenant_id": query.Scope.TenantID,
	} {
		if value != "" {
			must = append(must, exactKeywordQuery(field, value))
		}
	}
	must = append(must, map[string]any{"terms": map[string]any{"interaction_id.keyword": interactionIDs}})
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
		return nil, false, fmt.Errorf("decode Core interaction receipt projection: %w", err)
	}
	result := make([]receiptDocument, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		result = append(result, hit.Source)
	}
	return result, len(response.Hits.Hits) >= size, nil
}

func hasExactReceiptSelector(query iprojectionsource.Query) bool {
	return query.RequestID != "" || query.TraceID != "" || query.InteractionID != "" ||
		len(query.TraceIDs) > 0 || len(query.ConversationIDs) > 0
}

func hasBatchIdentitySelector(query iprojectionsource.Query) bool {
	return len(query.TraceIDs) > 0 || len(query.ConversationIDs) > 0
}

func batchIdentityCollapse(query iprojectionsource.Query) (string, int) {
	if len(query.TraceIDs) > 0 {
		return "trace_id.keyword", len(query.TraceIDs)
	}
	if len(query.ConversationIDs) > 0 {
		return "conversation_id.keyword", len(query.ConversationIDs)
	}
	return "", 0
}

func receiptInteractionIDs(receipts []receiptDocument) []string {
	seen := make(map[string]struct{})
	for _, receipt := range receipts {
		if receipt.InteractionID != "" {
			seen[receipt.InteractionID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for interactionID := range seen {
		result = append(result, interactionID)
	}
	sort.Strings(result)
	return result
}

func receiptsByInteraction(receipts []receiptDocument) map[string][]receiptDocument {
	result := make(map[string][]receiptDocument)
	for _, receipt := range receipts {
		if receipt.InteractionID != "" {
			result[receipt.InteractionID] = append(result[receipt.InteractionID], receipt)
		}
	}
	return result
}

func interactionMatchesScope(receipts []receiptDocument, scope evidencevo.QueryScope) bool {
	if len(receipts) == 0 {
		return false
	}
	networks := make(map[string]struct{})
	for _, receipt := range receipts {
		for _, networkID := range receiptKnowledgeNetworks(receipt) {
			networks[networkID] = struct{}{}
		}
	}
	knowledgeNetworkIDs := make([]string, 0, len(networks))
	for networkID := range networks {
		knowledgeNetworkIDs = append(knowledgeNetworkIDs, networkID)
	}
	record := evidencevo.RecordScope{
		TenantID:               receipts[0].Owner.TenantID,
		EffectiveSubjectID:     receipts[0].Owner.EffectiveSubjectID,
		ApplicationPrincipalID: receipts[0].Owner.ApplicationPrincipalID,
		KnowledgeNetworkIDs:    knowledgeNetworkIDs,
	}
	if scope.AccessProfile != nil {
		return evidencevo.CanReadRecord(*scope.AccessProfile, record, evidencevo.AccessViewBusiness)
	}
	return receiptMatchesScope(receipts[0], scope)
}

func matchesReceiptQuery(receipt receiptDocument, query iprojectionsource.Query) bool {
	if query.RequestID != "" && receipt.RequestID != query.RequestID ||
		query.TraceID != "" && receipt.TraceID != query.TraceID ||
		len(query.TraceIDs) > 0 && !containsProjectionID(query.TraceIDs, receipt.TraceID) ||
		len(query.ConversationIDs) > 0 && !containsProjectionID(query.ConversationIDs, receipt.ConversationID) ||
		query.InteractionID != "" && receipt.InteractionID != query.InteractionID {
		return false
	}
	if query.From.IsZero() && query.To.IsZero() {
		return true
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, receipt.IssuedAt)
	if err != nil {
		return false
	}
	return (query.From.IsZero() || !issuedAt.Before(query.From)) &&
		(query.To.IsZero() || !issuedAt.After(query.To))
}

func containsProjectionID(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func exactKeywordQuery(field string, value string) map[string]any {
	return map[string]any{"term": map[string]any{field + ".keyword": map[string]any{"value": value}}}
}

func receiptMatchesScope(receipt receiptDocument, scope evidencevo.QueryScope) bool {
	networks := receiptKnowledgeNetworks(receipt)
	record := evidencevo.RecordScope{
		TenantID:               receipt.Owner.TenantID,
		EffectiveSubjectID:     receipt.Owner.EffectiveSubjectID,
		ApplicationPrincipalID: receipt.Owner.ApplicationPrincipalID,
		KnowledgeNetworkIDs:    networks,
	}
	if scope.AccessProfile != nil {
		return evidencevo.CanReadRecord(*scope.AccessProfile, record, evidencevo.AccessViewBusiness)
	}
	return receipt.Owner.TenantID == scope.TenantID &&
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
				AccountID:              receipt.Owner.EffectiveSubjectID,
				AccountType:            string(receipt.Owner.EffectiveSubjectType),
				EffectiveSubjectID:     receipt.Owner.EffectiveSubjectID,
				ApplicationPrincipalID: receipt.Owner.ApplicationPrincipalID,
				KnowledgeNetworkIDs:    receiptKnowledgeNetworks(receipt),
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
	case evidencevo.ArtifactTypeQuery:
		return "data.query.observed", "query_artifact_ref"
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

func receiptScopeCandidates(scope evidencevo.QueryScope) []map[string]any {
	if scope.AccessProfile == nil {
		return receiptLegacyOwnerCandidate(scope)
	}
	profile := *scope.AccessProfile
	if evidencevo.HasTenantWideTraceAccess(profile) {
		return nil
	}
	if scope.View != "" && scope.View != evidencevo.AccessViewBusiness {
		if evidencevo.NeedsCrossAccountCandidates(scope) {
			return nil
		}
		return receiptProfileOwnerCandidates(profile)
	}
	should := make([]map[string]any, 0, 3)
	if profile.EffectiveSubjectID != "" {
		should = append(should, exactKeywordQuery("owner.effective_subject_id", profile.EffectiveSubjectID))
	}
	if profile.ApplicationPrincipalID != "" {
		should = append(should, exactKeywordQuery("owner.application_principal_id", profile.ApplicationPrincipalID))
	}
	if evidencevo.NeedsCrossAccountCandidates(scope) {
		should = append(should, map[string]any{"terms": map[string]any{
			"knowledge_network_ids.keyword": profile.ManagedKnowledgeNetworkIDs,
		}})
	}
	if len(should) == 0 {
		return receiptLegacyOwnerCandidate(scope)
	}
	return []map[string]any{{"bool": map[string]any{"should": should, "minimum_should_match": 1}}}
}

func receiptKnowledgeNetworks(receipt receiptDocument) []string {
	return receipt.KnowledgeNetworkIDs
}

func receiptLegacyOwnerCandidate(scope evidencevo.QueryScope) []map[string]any {
	return receiptCandidateSubjects(scope.AccountID, "")
}

func receiptProfileOwnerCandidates(profile evidencevo.AccessProfile) []map[string]any {
	return receiptCandidateSubjects(profile.EffectiveSubjectID, profile.ApplicationPrincipalID)
}

func receiptCandidateSubjects(effectiveSubjectID, applicationPrincipalID string) []map[string]any {
	should := make([]map[string]any, 0, 2)
	if effectiveSubjectID != "" {
		should = append(should, exactKeywordQuery("owner.effective_subject_id", effectiveSubjectID))
	}
	if applicationPrincipalID != "" {
		should = append(should, exactKeywordQuery("owner.application_principal_id", applicationPrincipalID))
	}
	if len(should) == 0 {
		return nil
	}
	return []map[string]any{{"bool": map[string]any{"should": should, "minimum_should_match": 1}}}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
