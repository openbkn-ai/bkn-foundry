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
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
	SubmitEvents(ctx, logger, req, events)
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
	return buildRetrievalEvents(ec, "context.search_schema", HashValue(strings.TrimSpace(req.Query)), len(refs), false, refs)
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

func SubmitEvents(ctx context.Context, logger interfaces.Logger, req any, events []Event) {
	if len(events) == 0 {
		return
	}
	ec, ok := contextFromRequest(ctx, req)
	if !ok || ec.accountID == "" || ec.accountType == "" {
		return
	}
	ingestURL := evidenceIngestURL()
	if ingestURL == "" {
		return
	}
	timeout := evidenceTimeout()
	traceBlock := map[string]any{
		"trace_id":         ec.traceID,
		"traceparent":      ec.traceparent,
		"bkn.request.id":   ec.requestID,
		"bkn.tenant.id":    ec.tenantID,
		"business_domain":  ec.businessDomain,
		"bkn.account.id":   ec.accountID,
		"bkn.account.type": ec.accountType,
	}
	if ec.conversationID != "" {
		traceBlock["bkn.conversation.id"] = ec.conversationID
	}
	if ec.interactionID != "" {
		traceBlock["bkn.interaction.id"] = ec.interactionID
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
		return
	}

	go func() {
		defer func() { <-evidenceInFlight }()
		if err := postBatchWithRetry(ingestURL, timeout, payload); err != nil {
			if logger != nil {
				logger.WithContext(ctx).Warnf("BKN Trace evidence ingestion unavailable: %v", err)
			} else {
				log.Printf("BKN Trace evidence ingestion unavailable: %v", err)
			}
		}
	}()
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
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	postCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, ingestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(os.Getenv(envEvidenceIngestToken)); token != "" {
		req.Header.Set("X-BKN-Trace-Ingest-Token", token)
	}

	resp, err := evidenceHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func evidenceIngestURL() string {
	return strings.TrimSpace(os.Getenv(envEvidenceIngestURL))
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
	if authContext != nil {
		accountID = strings.TrimSpace(authContext.AccountID)
		accountType = strings.TrimSpace(string(authContext.AccountType))
	}
	if schemaReq, ok := req.(*interfaces.SearchSchemaReq); ok && schemaReq != nil {
		if accountID == "" {
			accountID = strings.TrimSpace(schemaReq.XAccountID)
		}
		if accountType == "" {
			accountType = strings.TrimSpace(schemaReq.XAccountType)
		}
	}
	interactionID := strings.TrimSpace(traceContext.InteractionID)
	operationID := strings.TrimSpace(traceContext.OperationID)
	if interactionID == "" || operationID == "" {
		return eventContext{}, false
	}
	flags := "00"
	if spanContext.TraceFlags().IsSampled() {
		flags = "01"
	}
	attempt := traceContext.Attempt
	if attempt < 1 || attempt > 1000 {
		attempt = 1
	}
	return eventContext{
		traceID:          spanContext.TraceID().String(),
		spanID:           spanContext.SpanID().String(),
		traceparent:      fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:        traceContext.RequestID,
		accountID:        accountID,
		accountType:      accountType,
		tenantID:         strings.TrimSpace(traceContext.TenantID),
		businessDomain:   strings.TrimSpace(traceContext.BusinessDomain),
		conversationID:   strings.TrimSpace(traceContext.ConversationID),
		interactionID:    interactionID,
		operationID:      operationID,
		causationEventID: strings.TrimSpace(traceContext.CausationEventID),
		claimID:          strings.TrimSpace(traceContext.ClaimID),
		attempt:          attempt,
		observedAt:       observedAt,
	}, true
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
	refs := make([]map[string]any, 0, len(resp.ObjectTypes)+len(resp.RelationTypes)+len(resp.ActionTypes)+len(resp.MetricTypes))
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
