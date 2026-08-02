// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"go.opentelemetry.io/otel/trace"
)

const (
	ContractVersion = "2.1.0"
	ModuleName      = "context-loader"
)

const (
	envEvidenceIngestURL       = "BKN_TRACE_EVIDENCE_INGEST_URL"
	envEvidenceIngestToken     = "BKN_TRACE_EVIDENCE_INGEST_TOKEN"
	envEvidenceIngestTimeoutMS = "BKN_TRACE_EVIDENCE_TIMEOUT_MS"
)

const maxInFlightEvidenceBatches = 64
const maxSubgraphEvidenceRefs = 100

type Event map[string]any

type InteractionArtifactType string

const (
	InteractionArtifactQuestion InteractionArtifactType = "question"
	InteractionArtifactResult   InteractionArtifactType = "result"
)

type evidenceOutcomeContextKey struct{}

type evidenceOutcome struct {
	mu           sync.Mutex
	durable      bool
	eventIDs     []string
	businessRefs []BusinessRef
}

var (
	evidenceHTTPClient = &http.Client{}
	evidenceInFlight   = make(chan struct{}, maxInFlightEvidenceBatches)
)

type batch struct {
	ContractVersion string         `json:"bkn.trace.schema.version"`
	Trace           map[string]any `json:"trace"`
	Events          []Event        `json:"events"`
}

type eventContext struct {
	traceID          string
	spanID           string
	traceparent      string
	requestID        string
	accountID        string
	accountType      string
	applicationID    string
	subjectType      string
	tenantID         string
	businessDomain   string
	conversationID   string
	interactionID    string
	operationID      string
	causationEventID string
	claimID          string
	attempt          int
	observedAt       string
}

func HashValue(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func EvidenceEnabled() bool {
	return evidenceIngestURL() != ""
}

func RecordInteractionArtifact(
	ctx context.Context,
	conversationID string,
	interactionID string,
	artifactType InteractionArtifactType,
	content any,
) (string, error) {
	ec, ok := baseEventContext(ctx)
	if !ok {
		return "", errors.New("current request, trace and trusted account context are required")
	}
	ec.conversationID = strings.TrimSpace(conversationID)
	ec.interactionID = strings.TrimSpace(interactionID)
	ec.operationID = ""
	ec.attempt = 1
	if ec.conversationID == "" || ec.interactionID == "" {
		return "", errors.New("conversation_id and interaction_id are required")
	}
	eventType := ""
	artifactField := ""
	switch artifactType {
	case InteractionArtifactQuestion:
		eventType = "agent.interaction.started"
		artifactField = "question_artifact_ref"
	case InteractionArtifactResult:
		eventType = "claim.created"
		artifactField = "result_artifact_ref"
	default:
		return "", fmt.Errorf("unsupported interaction artifact type %q", artifactType)
	}
	contentHash := HashValue(content)
	idHash := sha256.Sum256([]byte(ec.interactionID + "|" + string(artifactType) + "|" + contentHash))
	artifactID := "art_" + string(artifactType) + "_" + hex.EncodeToString(idHash[:16])
	artifactRef := "artifact:" + artifactID
	traceBlock := traceBlockFromEventContext(ec)
	artifact := map[string]any{
		"artifact_id": artifactID, "artifact_type": string(artifactType),
		"bkn.request.id": ec.requestID, "trace_id": ec.traceID,
		"interaction_id": ec.interactionID, "content_type": "application/json",
		"schema_version": "2.2.0", "observed_at": ec.observedAt,
		"content_hash": contentHash, "content": content,
		"bkn.tenant.id": ec.tenantID, "business_domain": ec.businessDomain,
		"bkn.account.id": ec.accountID, "bkn.account.type": ec.accountType,
		"effective_subject_id":     ec.accountID,
		"application_principal_id": ec.applicationID,
		"initiator":                "account:" + ec.accountID, "agent_or_app": ec.applicationID,
	}
	if err := postArtifactWithRetry(evidenceArtifactURL(), evidenceTimeout(), traceBlock, artifact); err != nil {
		return "", err
	}
	event := buildEvent(
		ec, eventType, "interaction."+string(artifactType),
		map[string]any{artifactField: artifactRef, "content_hash": contentHash}, "", "",
	)
	event["event_id"] = stableEventID(ec.traceID, ec.interactionID, eventType, 1)
	if err := postBatchWithRetry(evidenceIngestURL(), evidenceTimeout(), batch{
		ContractVersion: ContractVersion, Trace: traceBlock, Events: []Event{event},
	}); err != nil {
		return "", err
	}
	return artifactRef, nil
}

func postArtifactWithRetry(
	url string,
	timeout time.Duration,
	traceBlock map[string]any,
	artifact map[string]any,
) error {
	if url == "" {
		return errors.New("BKN Trace artifact URL is not configured")
	}
	body, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		postCtx, cancel := context.WithTimeout(context.Background(), timeout)
		req, requestErr := http.NewRequestWithContext(postCtx, http.MethodPost, url, bytes.NewReader(body))
		if requestErr != nil {
			cancel()
			return requestErr
		}
		setEvidenceIngestHeaders(req.Header, traceBlock)
		resp, requestErr := evidenceHTTPClient.Do(req)
		if requestErr == nil {
			resp.Body.Close()
			if resp.StatusCode < http.StatusBadRequest {
				cancel()
				return nil
			}
			requestErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		cancel()
		err = requestErr
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
		}
	}
	return err
}

func EmitSearchSchemaEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.SearchSchemaReq, resp *interfaces.SearchSchemaResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, req, BuildSearchSchemaEvents(ctx, req, resp))
}

func EmitQueryObjectInstanceEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, req, BuildQueryObjectInstanceEvents(ctx, req, resp))
}

func EmitQueryInstanceSubgraphEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.QueryInstanceSubgraphReq, resp *interfaces.QueryInstanceSubgraphResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, req, BuildQueryInstanceSubgraphEvents(ctx, req, resp))
}

func EmitRunSQLEvents(ctx context.Context, logger interfaces.Logger, sql string, resourceIDs []string, resp *interfaces.VegaRawQueryResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, nil, BuildRunSQLEvents(ctx, sql, resourceIDs, resp))
}

func submitAndReturnFirstEventID(ctx context.Context, logger interfaces.Logger, req any, events []Event) string {
	_ = SubmitEvents(ctx, logger, req, events)
	if len(events) == 0 {
		return ""
	}
	eventID, _ := events[0]["event_id"].(string)
	return eventID
}

func BuildSearchSchemaEvents(ctx context.Context, req *interfaces.SearchSchemaReq, resp *interfaces.SearchSchemaResp) []Event {
	ec, ok := contextFromRequest(ctx, req)
	if !ok {
		return nil
	}
	refs := schemaEvidenceRefs(resolvedKnID(req), resp)
	candidateCount := 0
	if resp != nil {
		candidateCount = len(resp.ObjectTypes) + len(resp.RelationTypes) + len(resp.ActionTypes) + len(resp.MetricTypes)
	}
	return buildRetrievalEvents(ec, "context.search_schema", HashValue(strings.TrimSpace(req.Query)), candidateCount, false, refs)
}

func BuildQueryObjectInstanceEvents(ctx context.Context, req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs := objectInstanceEvidenceRefs(req, resp)
	candidateCount := 0
	if resp != nil {
		candidateCount = len(resp.Data)
	}
	return buildRetrievalEvents(ec, "context.query_object", queryObjectConditionHash(req), candidateCount, queryObjectTruncated(req, resp), refs)
}

func BuildQueryInstanceSubgraphEvents(ctx context.Context, req *interfaces.QueryInstanceSubgraphReq, resp *interfaces.QueryInstanceSubgraphResp) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs, refsTruncated := subgraphEvidenceRefs(req, resp)
	return buildRetrievalEvents(ec, "context.query_instance_subgraph", querySubgraphPathHash(req), len(refs), refsTruncated, refs)
}

func BuildRunSQLEvents(ctx context.Context, sql string, resourceIDs []string, resp *interfaces.VegaRawQueryResp) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs := make([]map[string]any, 0, len(resourceIDs))
	seen := map[string]struct{}{}
	for _, resourceID := range resourceIDs {
		resourceID = strings.TrimSpace(resourceID)
		if resourceID == "" {
			continue
		}
		if _, exists := seen[resourceID]; exists {
			continue
		}
		seen[resourceID] = struct{}{}
		refs = append(refs, map[string]any{
			"ref_id":         "resource:" + resourceID,
			"ref_type":       "data_resource",
			"source_system":  "vega",
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
		})
	}
	count := 0
	truncated := false
	if resp != nil {
		count = len(resp.Entries)
		truncated = resp.Paging != nil && resp.Paging.NextCursor != nil
		if resp.TotalCount != nil && *resp.TotalCount > int64(count) {
			truncated = true
		}
	}
	return buildRetrievalEvents(
		ec,
		"context.run_sql",
		HashValue(strings.TrimSpace(sql)),
		count,
		truncated,
		refs,
	)
}

func buildRetrievalEvents(ec eventContext, operation, queryHash string, candidateCount int, truncated bool, refs []map[string]any) []Event {
	fact := buildEvent(ec, "retrieval.completed", operation, map[string]any{
		"query_hash":      queryHash,
		"candidate_count": candidateCount,
		"truncated":       truncated,
		"version_status":  "unversioned",
		"source_refs":     refs,
	}, "", ec.causationEventID)
	return []Event{fact}
}

func SubmitEvents(ctx context.Context, logger interfaces.Logger, req any, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	ec, ok := contextFromRequest(ctx, req)
	if !ok || ec.accountID == "" || ec.accountType == "" {
		return nil
	}
	ingestURL := evidenceIngestURL()
	if ingestURL == "" {
		return nil
	}
	timeout := evidenceTimeout()
	traceBlock := map[string]any{
		"trace_id":                     ec.traceID,
		"traceparent":                  ec.traceparent,
		"bkn.request.id":               ec.requestID,
		"bkn.tenant.id":                ec.tenantID,
		"business_domain":              ec.businessDomain,
		"bkn.account.id":               ec.accountID,
		"bkn.account.type":             ec.accountType,
		"bkn.application.principal.id": ec.applicationID,
		"bkn.effective.subject.type":   ec.subjectType,
	}
	if ec.conversationID != "" {
		traceBlock["bkn.conversation.id"] = ec.conversationID
	}
	payload := batch{
		ContractVersion: ContractVersion,
		Trace:           traceBlock,
		Events:          events,
	}

	select {
	case evidenceInFlight <- struct{}{}:
	default:
		if logger != nil {
			logger.WithContext(ctx).Warn("BKN Trace evidence ingestion dropped: in-flight limit reached")
		} else {
			log.Printf("BKN Trace evidence ingestion dropped: in-flight limit reached")
		}
		return fmt.Errorf("evidence ingestion in-flight limit reached")
	}
	defer func() { <-evidenceInFlight }()
	if err := postBatchWithRetry(ingestURL, timeout, payload); err != nil {
		if logger != nil {
			logger.WithContext(ctx).Warnf("BKN Trace evidence ingestion unavailable: %v", err)
		} else {
			log.Printf("BKN Trace evidence ingestion unavailable: %v", err)
		}
		return err
	}
	recordDurableEvidenceOutcome(ctx, payload)
	return nil
}

func withEvidenceOutcome(ctx context.Context) context.Context {
	if evidenceOutcomeFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, evidenceOutcomeContextKey{}, &evidenceOutcome{})
}

func evidenceOutcomeFromContext(ctx context.Context) *evidenceOutcome {
	value, _ := ctx.Value(evidenceOutcomeContextKey{}).(*evidenceOutcome)
	return value
}

func recordDurableEvidenceOutcome(ctx context.Context, payload batch) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	outcome.durable = true
	seenEvents := make(map[string]struct{}, len(outcome.eventIDs))
	for _, eventID := range outcome.eventIDs {
		seenEvents[eventID] = struct{}{}
	}
	seenRefs := make(map[string]struct{}, len(outcome.businessRefs))
	for _, ref := range outcome.businessRefs {
		seenRefs[ref.RefType+"\x00"+ref.RefID+"\x00"+ref.Version] = struct{}{}
	}
	for _, event := range payload.Events {
		if eventID := stringValue(event["event_id"]); eventID != "" {
			if _, exists := seenEvents[eventID]; !exists {
				outcome.eventIDs = append(outcome.eventIDs, eventID)
				seenEvents[eventID] = struct{}{}
			}
		}
		for _, ref := range trace30BusinessRefs(event, stringValue(payload.Trace["business_domain"])) {
			key := ref.RefType + "\x00" + ref.RefID + "\x00" + ref.Version
			if _, exists := seenRefs[key]; exists {
				continue
			}
			outcome.businessRefs = append(outcome.businessRefs, BusinessRef{
				RefType: ref.RefType, RefID: ref.RefID, BusinessDomainID: ref.BusinessDomainID,
				Version: ref.Version, DisplayHint: ref.DisplayHint,
			})
			seenRefs[key] = struct{}{}
		}
	}
}

func snapshotEvidenceOutcome(ctx context.Context) (bool, []string, []BusinessRef) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return false, nil, nil
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	return outcome.durable, append([]string(nil), outcome.eventIDs...), append([]BusinessRef(nil), outcome.businessRefs...)
}

func postBatchWithRetry(ingestURL string, timeout time.Duration, payload batch) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = postBatch(ingestURL, timeout, payload); err == nil {
			return nil
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
		}
	}
	return err
}

func postBatch(ingestURL string, timeout time.Duration, payload batch) error {
	for _, event := range payload.Events {
		requestPayload, err := trace30EvidenceEvent(payload.Trace, event)
		if err != nil {
			return err
		}
		body, err := json.Marshal(requestPayload)
		if err != nil {
			return err
		}
		postCtx, cancel := context.WithTimeout(context.Background(), timeout)
		req, err := http.NewRequestWithContext(postCtx, http.MethodPost, ingestURL, bytes.NewReader(body))
		if err != nil {
			cancel()
			return err
		}
		setEvidenceIngestHeaders(req.Header, payload.Trace)

		resp, err := evidenceHTTPClient.Do(req)
		if err != nil {
			cancel()
			return err
		}
		resp.Body.Close()
		cancel()
		if resp.StatusCode >= http.StatusBadRequest {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return nil
}

type trace30Event struct {
	SchemaVersion          string                 `json:"bkn.trace.schema.version"`
	EventID                string                 `json:"event_id"`
	EventType              string                 `json:"event_type"`
	PayloadHash            string                 `json:"payload_hash"`
	ConversationID         string                 `json:"conversation_id"`
	InteractionID          string                 `json:"interaction_id"`
	OperationID            string                 `json:"operation_id,omitempty"`
	Attempt                uint32                 `json:"attempt,omitempty"`
	RequestID              string                 `json:"request_id,omitempty"`
	TraceID                string                 `json:"trace_id,omitempty"`
	SpanID                 string                 `json:"span_id,omitempty"`
	ProducerID             string                 `json:"producer_id"`
	ProducerStreamID       string                 `json:"producer_stream_id"`
	ProducerEpoch          uint64                 `json:"producer_epoch"`
	ProducerSequence       uint64                 `json:"producer_sequence"`
	CausationEventIDs      []string               `json:"causation_event_ids,omitempty"`
	StartedAt              string                 `json:"started_at"`
	ObservedAt             string                 `json:"observed_at"`
	EmittedAt              string                 `json:"emitted_at"`
	Envelope               Event                  `json:"envelope"`
	BusinessRefs           []trace30BusinessRef   `json:"business_refs,omitempty"`
	OperationBusinessEdges []trace30OperationEdge `json:"operation_business_edges,omitempty"`
}

type trace30BusinessRef struct {
	RefType          string `json:"ref_type"`
	RefID            string `json:"ref_id"`
	BusinessDomainID string `json:"business_domain_id"`
	Version          string `json:"version"`
	DisplayHint      string `json:"display_hint,omitempty"`
}

type trace30OperationEdge struct {
	OperationID string             `json:"operation_id"`
	BusinessRef trace30BusinessRef `json:"business_ref"`
	Role        string             `json:"role"`
	ObservedAt  string             `json:"observed_at"`
}

func trace30EvidenceEvent(traceBlock map[string]any, event Event) (trace30Event, error) {
	envelope, err := json.Marshal(event)
	if err != nil {
		return trace30Event{}, err
	}
	sum := sha256.Sum256(envelope)
	operationID := stringValue(event["operation_id"])
	attempt := uint32(intValue(event["attempt"], 1))
	observedAt := stringValue(event["observed_at"])
	emittedAt := stringValue(event["emitted_at"])
	if emittedAt == "" {
		emittedAt = observedAt
	}
	refs := trace30BusinessRefs(event, stringValue(traceBlock["business_domain"]))
	edges := make([]trace30OperationEdge, 0, len(refs))
	for _, ref := range refs {
		edges = append(edges, trace30OperationEdge{
			OperationID: operationID, BusinessRef: ref, Role: "read", ObservedAt: observedAt,
		})
	}
	causationIDs := []string{}
	if value := stringValue(event["causation_event_id"]); value != "" {
		causationIDs = append(causationIDs, value)
	}
	producerStreamKey := operationID
	if producerStreamKey == "" {
		producerStreamKey = "interaction:" + stringValue(event["interaction_id"]) + ":" + stringValue(event["event_type"])
	}
	return trace30Event{
		SchemaVersion: CoreSchemaVersion, EventID: stringValue(event["event_id"]),
		EventType: stringValue(event["event_type"]), PayloadHash: hex.EncodeToString(sum[:]),
		ConversationID: stringValue(traceBlock["bkn.conversation.id"]),
		InteractionID:  stringValue(event["interaction_id"]), OperationID: operationID,
		Attempt: attempt, RequestID: stringValue(traceBlock["bkn.request.id"]),
		TraceID: stringValue(traceBlock["trace_id"]), SpanID: stringValue(event["span_id"]),
		ProducerID: ModuleName, ProducerStreamID: ModuleName + ":" + producerStreamKey,
		ProducerEpoch: 1, ProducerSequence: uint64(attempt), CausationEventIDs: causationIDs,
		StartedAt: observedAt, ObservedAt: observedAt, EmittedAt: emittedAt,
		Envelope: event, BusinessRefs: refs, OperationBusinessEdges: edges,
	}, nil
}

func trace30BusinessRefs(event Event, businessDomain string) []trace30BusinessRef {
	payload, _ := event["payload"].(map[string]any)
	items, _ := payload["source_refs"].([]map[string]any)
	refs := make([]trace30BusinessRef, 0, len(items))
	for _, item := range items {
		refID := stringValue(item["ref_id"])
		refType := trace30RefType(stringValue(item["ref_type"]))
		if refID == "" || refType == "" || businessDomain == "" {
			continue
		}
		version := stringValue(item["version_status"])
		if version == "" {
			version = "observed"
		}
		refs = append(refs, trace30BusinessRef{
			RefType: refType, RefID: refID, BusinessDomainID: businessDomain,
			Version: version, DisplayHint: stringValue(item["display_hint"]),
		})
	}
	return refs
}

func trace30RefType(value string) string {
	switch value {
	case "object":
		return "object_type"
	case "relation":
		return "relation_type"
	case "action":
		return "action_type"
	case "knowledge_network", "object_type", "object_instance", "property", "relation_type",
		"data_resource", "metric", "logic", "function", "action_type", "action_instance":
		return value
	default:
		return ""
	}
}

func setEvidenceIngestHeaders(headers http.Header, traceBlock map[string]any) {
	headers.Set("Content-Type", "application/json")
	headers.Set("X-BKN-Trace-Ingest-Token", strings.TrimSpace(os.Getenv(envEvidenceIngestToken)))
	headers.Set(coreGatewayTokenHeader, strings.TrimSpace(os.Getenv(envCoreGatewayToken)))
	accountID := stringValue(traceBlock["bkn.account.id"])
	accountType := stringValue(traceBlock["bkn.account.type"])
	tenantID := stringValue(traceBlock["bkn.tenant.id"])
	businessDomain := stringValue(traceBlock["business_domain"])
	applicationID := stringValue(traceBlock["bkn.application.principal.id"])
	if applicationID == "" {
		applicationID = ModuleName
	}
	subjectType := stringValue(traceBlock["bkn.effective.subject.type"])
	if subjectType == "" {
		subjectType = "user"
	}
	headers.Set("x-account-id", accountID)
	headers.Set("x-account-type", accountType)
	headers.Set("x-tenant-id", tenantID)
	headers.Set("x-business-domain", businessDomain)
	headers.Set("X-BKN-Tenant-ID", tenantID)
	headers.Set("X-Business-Domain-ID", businessDomain)
	headers.Set("X-BKN-Application-Principal-ID", applicationID)
	headers.Set("X-BKN-Effective-Subject-Type", subjectType)
	headers.Set("X-BKN-Effective-Subject-ID", accountID)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

func evidenceIngestURL() string {
	return strings.TrimSpace(os.Getenv(envEvidenceIngestURL))
}

func evidenceArtifactURL() string {
	value := strings.TrimRight(evidenceIngestURL(), "/")
	if strings.HasSuffix(value, "/events") {
		return strings.TrimSuffix(value, "/events") + "/artifacts"
	}
	return ""
}

func evidenceTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv(envEvidenceIngestTimeoutMS))
	if value == "" {
		return 2 * time.Second
	}
	var ms int
	if _, err := fmt.Sscanf(value, "%d", &ms); err != nil || ms <= 0 {
		return 2 * time.Second
	}
	return time.Duration(ms) * time.Millisecond
}

func contextFromRequest(ctx context.Context, req any) (eventContext, bool) {
	ec, ok := baseEventContext(ctx)
	if !ok {
		return eventContext{}, false
	}
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	if schemaReq, ok := req.(*interfaces.SearchSchemaReq); ok && schemaReq != nil {
		if ec.accountID == "" {
			ec.accountID = strings.TrimSpace(schemaReq.XAccountID)
		}
		if ec.accountType == "" {
			ec.accountType = strings.TrimSpace(schemaReq.XAccountType)
		}
	}
	ec.interactionID = strings.TrimSpace(traceContext.InteractionID)
	ec.operationID = strings.TrimSpace(traceContext.OperationID)
	if ec.interactionID == "" || ec.operationID == "" {
		return eventContext{}, false
	}
	ec.causationEventID = strings.TrimSpace(traceContext.CausationEventID)
	ec.claimID = strings.TrimSpace(traceContext.ClaimID)
	ec.attempt = traceContext.Attempt
	if ec.attempt < 1 || ec.attempt > 1000 {
		ec.attempt = 1
	}
	return ec, true
}

func baseEventContext(ctx context.Context) (eventContext, bool) {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return eventContext{}, false
	}
	traceContext, ok := common.GetTraceContextFromCtx(ctx)
	if !ok || !common.IsValidBKNRequestID(traceContext.RequestID) {
		return eventContext{}, false
	}
	if !traceContext.ObservedAtProvided {
		return eventContext{}, false
	}
	observedAt := strings.TrimSpace(traceContext.ObservedAt)
	if _, err := time.Parse(time.RFC3339Nano, observedAt); err != nil {
		return eventContext{}, false
	}
	authContext, _ := common.GetAccountAuthContextFromCtx(ctx)
	accountID := ""
	accountType := ""
	applicationID := ""
	subjectType := "user"
	if authContext != nil {
		accountID = strings.TrimSpace(authContext.AccountID)
		accountType = strings.TrimSpace(string(authContext.AccountType))
		applicationID = accountID
		if authContext.TokenInfo != nil && strings.TrimSpace(authContext.TokenInfo.ClientID) != "" {
			applicationID = strings.TrimSpace(authContext.TokenInfo.ClientID)
		}
		if authContext.AccountType == interfaces.AccessorTypeApp {
			subjectType = "service"
		}
	}
	if accountID == "" || accountType == "" {
		return eventContext{}, false
	}
	flags := "00"
	if spanContext.TraceFlags().IsSampled() {
		flags = "01"
	}
	return eventContext{
		traceID:          spanContext.TraceID().String(),
		spanID:           spanContext.SpanID().String(),
		traceparent:      fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:        traceContext.RequestID,
		accountID:        accountID,
		accountType:      accountType,
		applicationID:    applicationID,
		subjectType:      subjectType,
		tenantID:         strings.TrimSpace(traceContext.TenantID),
		businessDomain:   strings.TrimSpace(traceContext.BusinessDomain),
		conversationID:   strings.TrimSpace(traceContext.ConversationID),
		interactionID:    strings.TrimSpace(traceContext.InteractionID),
		operationID:      strings.TrimSpace(traceContext.OperationID),
		causationEventID: strings.TrimSpace(traceContext.CausationEventID),
		claimID:          strings.TrimSpace(traceContext.ClaimID),
		attempt:          traceContext.Attempt,
		observedAt:       observedAt,
	}, true
}

func traceBlockFromEventContext(ec eventContext) map[string]any {
	return map[string]any{
		"trace_id": ec.traceID, "traceparent": ec.traceparent,
		"bkn.request.id": ec.requestID, "bkn.tenant.id": ec.tenantID,
		"business_domain": ec.businessDomain, "bkn.account.id": ec.accountID,
		"bkn.account.type":             ec.accountType,
		"bkn.application.principal.id": ec.applicationID,
		"bkn.effective.subject.type":   ec.subjectType,
		"bkn.conversation.id":          ec.conversationID,
	}
}

func buildEvent(ec eventContext, eventType, operationName string, payload map[string]any, claimID, causationEventID string) Event {
	now := ec.observedAt
	event := Event{
		"event_id":                 stableEventID(ec.traceID, ec.operationID, eventType, ec.attempt),
		"event_type":               eventType,
		"bkn.trace.schema.version": ContractVersion,
		"observed_at":              now,
		"emitted_at":               now,
		"producer_module":          ModuleName,
		"trace_id":                 ec.traceID,
		"span_id":                  ec.spanID,
		"bkn.request.id":           ec.requestID,
		"bkn.operation.name":       operationName,
		"interaction_id":           ec.interactionID,
		"operation_id":             ec.operationID,
		"attempt":                  ec.attempt,
		"payload":                  payload,
	}
	if causationEventID != "" {
		event["causation_event_id"] = causationEventID
	}
	if claimID != "" {
		event["claim_id"] = claimID
	}
	return event
}

func stableEventID(traceID, operationID, eventType string, attempt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", traceID, operationID, eventType, attempt)))
	return "evt_" + hex.EncodeToString(sum[:])
}

func schemaEvidenceRefs(knID string, resp *interfaces.SearchSchemaResp) []map[string]any {
	if resp == nil || strings.TrimSpace(knID) == "" {
		return nil
	}
	refs := make([]map[string]any, 0, 1+len(resp.ObjectTypes)+len(resp.RelationTypes)+len(resp.ActionTypes)+len(resp.MetricTypes))
	refs = append(refs, map[string]any{
		"ref_id":         "kn:" + knID,
		"ref_type":       "knowledge_network",
		"source_system":  "context-loader",
		"validity":       "observed",
		"version_status": "unversioned",
		"visibility":     "visible",
	})
	refs = append(refs, conceptRefs("object", "object", knID, resp.ObjectTypes)...)
	refs = append(refs, conceptRefs("relation", "relation", knID, resp.RelationTypes)...)
	refs = append(refs, conceptRefs("action_type", "action", knID, resp.ActionTypes)...)
	refs = append(refs, conceptRefs("metric", "metric", knID, resp.MetricTypes)...)
	return refs
}

func conceptRefs(kind, refType, knID string, items []any) []map[string]any {
	refs := make([]map[string]any, 0, len(items))
	for _, item := range items {
		itemMap, ok := asMap(item)
		if !ok {
			continue
		}
		id := firstString(itemMap, "concept_id", "id")
		if id == "" {
			continue
		}
		refs = append(refs, map[string]any{
			"ref_id":         kind + ":" + knID + ":" + id,
			"ref_type":       refType,
			"source_system":  ModuleName,
			"summary_hash":   HashValue(safeConceptSummary(kind, itemMap)),
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
		})
	}
	return refs
}

func safeConceptSummary(kind string, item map[string]any) map[string]any {
	return map[string]any{
		"kind":                  kind,
		"id":                    firstString(item, "concept_id", "id"),
		"module_type":           firstString(item, "module_type"),
		"source_object_type_id": firstString(item, "source_object_type_id"),
		"target_object_type_id": firstString(item, "target_object_type_id"),
		"object_type_id":        firstString(item, "object_type_id"),
		"score_bucket":          scoreBucket(item),
	}
}

func asMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}
	if itemMap, ok := value.(map[string]any); ok {
		return itemMap, true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var itemMap map[string]any
	if err := json.Unmarshal(raw, &itemMap); err != nil {
		return nil, false
	}
	return itemMap, true
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func scoreBucket(item map[string]any) string {
	score, ok := item["_score"].(float64)
	if !ok {
		score, ok = item["score"].(float64)
	}
	if !ok {
		return "unknown"
	}
	switch {
	case score >= 0.8:
		return "high"
	case score >= 0.5:
		return "medium"
	default:
		return "low"
	}
}

func resolvedKnID(req *interfaces.SearchSchemaReq) string {
	if req == nil {
		return ""
	}
	if strings.TrimSpace(req.XKnID) != "" {
		return strings.TrimSpace(req.XKnID)
	}
	return strings.TrimSpace(req.KnID)
}

func objectInstanceEvidenceRefs(req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) []map[string]any {
	if req == nil {
		return nil
	}
	knID := queryObjectKnID(req)
	objectTypeID := queryObjectTypeID(req)
	if knID == "" || objectTypeID == "" {
		return nil
	}
	refs := []map[string]any{controlledRef("object:"+knID+":"+objectTypeID, "object")}
	for _, propertyID := range req.Properties {
		if propertyID = strings.TrimSpace(propertyID); propertyID != "" {
			refs = append(refs, controlledRef("property:"+knID+":"+objectTypeID+":"+propertyID, "property"))
		}
	}
	return refs
}

func controlledRef(refID, refType string) map[string]any {
	return map[string]any{
		"ref_id": refID, "ref_type": refType, "source_system": "bkn",
		"validity": "observed", "version_status": "unversioned", "visibility": "visible",
	}
}

func objectInstanceIdentity(item any) (map[string]any, bool) {
	itemMap, ok := asMap(item)
	if !ok {
		return nil, false
	}
	identity, ok := itemMap["_instance_identity"]
	if !ok {
		return nil, false
	}
	return asMap(identity)
}

func queryObjectConditionHash(req *interfaces.QueryObjectInstancesReq) string {
	if req == nil {
		return HashValue(nil)
	}
	return HashValue(map[string]any{
		"condition":    req.Cond,
		"filters":      req.Filters,
		"offset":       req.Offset,
		"search_after": req.SearchAfter,
	})
}

func queryObjectTruncated(req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) bool {
	if resp == nil {
		return false
	}
	if len(resp.SearchAfter) > 0 {
		return true
	}
	if req == nil || resp.TotalCount <= 0 {
		return false
	}
	return int64(req.Offset+len(resp.Data)) < resp.TotalCount
}

func queryObjectKnID(req *interfaces.QueryObjectInstancesReq) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.KnID)
}

func queryObjectTypeID(req *interfaces.QueryObjectInstancesReq) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.OtID)
}

func subgraphEvidenceRefs(req *interfaces.QueryInstanceSubgraphReq, resp *interfaces.QueryInstanceSubgraphResp) ([]map[string]any, bool) {
	if resp == nil || resp.Entries == nil {
		return nil, false
	}
	refs := make([]map[string]any, 0)
	seen := make(map[string]struct{})
	truncated := false
	if req == nil {
		return refs, false
	}
	walkSubgraphValue(req.RelationTypePaths, func(item map[string]any) bool {
		knID := strings.TrimSpace(req.KnID)
		for _, candidate := range []struct{ key, prefix, refType string }{
			{"source_ot_id", "object:", "object"},
			{"target_ot_id", "object:", "object"},
			{"relation_type_id", "relation:", "relation"},
		} {
			if id := firstString(item, candidate.key); knID != "" && id != "" && !appendEvidenceRef(&refs, seen, controlledRef(candidate.prefix+knID+":"+id, candidate.refType)) {
				truncated = true
				return false
			}
		}
		return true
	})
	return refs, truncated
}

func appendEvidenceRef(refs *[]map[string]any, seen map[string]struct{}, ref map[string]any) bool {
	key := firstString(ref, "ref_type") + ":" + firstString(ref, "ref_id")
	if _, ok := seen[key]; ok {
		return true
	}
	if len(*refs) >= maxSubgraphEvidenceRefs {
		return false
	}
	seen[key] = struct{}{}
	*refs = append(*refs, ref)
	return true
}

func walkSubgraphValue(value any, visit func(map[string]any) bool) bool {
	switch typed := value.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	case []any:
		for _, item := range typed {
			if !walkSubgraphValue(item, visit) {
				return false
			}
		}
	case map[string]any:
		if !visit(typed) {
			return false
		}
		for _, nested := range typed {
			if !walkSubgraphValue(nested, visit) {
				return false
			}
		}
	default:
		if item, ok := asMap(value); ok {
			if !visit(item) {
				return false
			}
			for _, nested := range item {
				if !walkSubgraphValue(nested, visit) {
					return false
				}
			}
		}
	}
	return true
}

func walkRelationContainers(value any, visit func(map[string]any) bool) bool {
	return walkSubgraphValue(value, func(item map[string]any) bool {
		for key, nested := range item {
			if !isRelationContainerKey(key) {
				continue
			}
			if !walkSubgraphValue(nested, visit) {
				return false
			}
		}
		return true
	})
}

func isRelationContainerKey(key string) bool {
	switch key {
	case "relation", "relations", "relation_path", "relation_paths", "relation_type", "relation_types":
		return true
	default:
		return false
	}
}

func querySubgraphPathHash(req *interfaces.QueryInstanceSubgraphReq) string {
	if req == nil {
		return HashValue(nil)
	}
	return HashValue(req.RelationTypePaths)
}

func hashSuffix(value any) string {
	hash := strings.TrimPrefix(HashValue(value), "sha256:")
	if len(hash) > 24 {
		return hash[:24]
	}
	return hash
}
