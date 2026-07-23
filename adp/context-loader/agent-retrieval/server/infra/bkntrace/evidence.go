// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"go.opentelemetry.io/otel/trace"
)

const (
	ContractVersion = "2.0.0"
	ModuleName      = "context-loader"
)

const (
	envEvidenceIngestURL       = "BKN_TRACE_EVIDENCE_INGEST_URL"
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
	traceID     string
	spanID      string
	traceparent string
	requestID   string
	accountID   string
	accountType string
}

func HashValue(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ClaimID(kind, subjectID string, value any) string {
	sum := sha256.Sum256([]byte(HashValue(map[string]any{
		"kind":       kind,
		"subject_id": subjectID,
		"value":      value,
	})))
	return "claim_" + hex.EncodeToString(sum[:])[:24]
}

func EvidenceEnabled() bool {
	return evidenceIngestURL() != ""
}

func EmitSearchSchemaEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.SearchSchemaReq, resp *interfaces.SearchSchemaResp) {
	if !EvidenceEnabled() {
		return
	}
	SubmitEvents(ctx, logger, req, BuildSearchSchemaEvents(ctx, req, resp))
}

func EmitQueryObjectInstanceEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) {
	if !EvidenceEnabled() {
		return
	}
	SubmitEvents(ctx, logger, req, BuildQueryObjectInstanceEvents(ctx, req, resp))
}

func EmitQueryInstanceSubgraphEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.QueryInstanceSubgraphReq, resp *interfaces.QueryInstanceSubgraphResp) {
	if !EvidenceEnabled() {
		return
	}
	SubmitEvents(ctx, logger, req, BuildQueryInstanceSubgraphEvents(ctx, req, resp))
}

func BuildSearchSchemaEvents(ctx context.Context, req *interfaces.SearchSchemaReq, resp *interfaces.SearchSchemaResp) []Event {
	ec, ok := contextFromRequest(ctx, req)
	if !ok {
		return nil
	}
	refs := schemaEvidenceRefs(resp)
	if len(refs) == 0 {
		return nil
	}

	resultSummary := map[string]any{
		"kn_id":               resolvedKnID(req),
		"query_hash":          HashValue(strings.TrimSpace(req.Query)),
		"object_type_count":   len(resp.ObjectTypes),
		"relation_type_count": len(resp.RelationTypes),
		"action_type_count":   len(resp.ActionTypes),
		"metric_type_count":   len(resp.MetricTypes),
	}
	claimID := ClaimID("context_loader.search_schema", resolvedKnID(req), resultSummary)

	return []Event{
		buildEvent(ec, "claim.created", "context.search_schema", map[string]any{
			"claim_id":       claimID,
			"claim_type":     "finding",
			"claim_hash":     HashValue(resultSummary),
			"visibility":     "visible",
			"version_status": "unversioned",
			"partial_reason": []string{"schema_refs_unversioned"},
			"subject_refs": map[string]any{
				"kn_id":               resolvedKnID(req),
				"query_hash":          resultSummary["query_hash"],
				"max_concepts":        maxConcepts(req),
				"include_columns":     boolValue(req.IncludeColumns),
				"schema_brief":        boolValue(req.SchemaBrief),
				"returned_ref_count":  len(refs),
				"data.classification": "internal",
			},
		}),
		buildEvent(ec, "evidence.refs.created", "context.search_schema", map[string]any{
			"claim_id":      claimID,
			"evidence_refs": refs,
		}),
	}
}

func BuildQueryObjectInstanceEvents(ctx context.Context, req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs := objectInstanceEvidenceRefs(req, resp)
	if len(refs) == 0 {
		return nil
	}

	resultSummary := map[string]any{
		"kn_id":          queryObjectKnID(req),
		"object_type_id": queryObjectTypeID(req),
		"condition_hash": queryObjectConditionHash(req),
		"result_count":   len(resp.Data),
		"truncated":      queryObjectTruncated(req, resp),
	}
	claimID := ClaimID("context_loader.query_object_instance", queryObjectTypeID(req), resultSummary)

	partialReason := []string{"row_refs_unversioned"}
	if queryObjectTruncated(req, resp) {
		partialReason = append(partialReason, "result_truncated")
	}

	return []Event{
		buildEvent(ec, "claim.created", "context.query_object", map[string]any{
			"claim_id":       claimID,
			"claim_type":     "finding",
			"claim_hash":     HashValue(resultSummary),
			"visibility":     "visible",
			"version_status": "unversioned",
			"partial_reason": partialReason,
			"subject_refs": map[string]any{
				"kn_id":               queryObjectKnID(req),
				"object_type_id":      queryObjectTypeID(req),
				"condition_hash":      resultSummary["condition_hash"],
				"properties_hash":     queryObjectPropertiesHash(req),
				"limit":               queryObjectLimit(req),
				"returned_ref_count":  len(refs),
				"truncated":           queryObjectTruncated(req, resp),
				"data.classification": "internal",
			},
		}),
		buildEvent(ec, "evidence.refs.created", "context.query_object", map[string]any{
			"claim_id":      claimID,
			"evidence_refs": refs,
		}),
	}
}

func BuildQueryInstanceSubgraphEvents(ctx context.Context, req *interfaces.QueryInstanceSubgraphReq, resp *interfaces.QueryInstanceSubgraphResp) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs, refsTruncated := subgraphEvidenceRefs(resp)
	if len(refs) == 0 {
		return nil
	}

	resultSummary := map[string]any{
		"kn_id":                querySubgraphKnID(req),
		"path_hash":            querySubgraphPathHash(req),
		"entry_count":          subgraphEntryCount(resp),
		"include_logic_params": querySubgraphIncludeLogicParams(req),
	}
	claimID := ClaimID("context_loader.query_instance_subgraph", querySubgraphKnID(req), resultSummary)

	partialReason := []string{"row_refs_unversioned", "schema_refs_unversioned"}
	if refsTruncated {
		partialReason = append(partialReason, "refs_truncated")
	}

	return []Event{
		buildEvent(ec, "claim.created", "context.query_instance_subgraph", map[string]any{
			"claim_id":       claimID,
			"claim_type":     "finding",
			"claim_hash":     HashValue(resultSummary),
			"visibility":     "visible",
			"version_status": "unversioned",
			"partial_reason": partialReason,
			"subject_refs": map[string]any{
				"kn_id":                querySubgraphKnID(req),
				"path_hash":            resultSummary["path_hash"],
				"returned_ref_count":   len(refs),
				"include_logic_params": querySubgraphIncludeLogicParams(req),
				"refs_truncated":       refsTruncated,
				"data.classification":  "internal",
			},
		}),
		buildEvent(ec, "evidence.refs.created", "context.query_instance_subgraph", map[string]any{
			"claim_id":      claimID,
			"evidence_refs": refs,
		}),
	}
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
	payload := batch{
		ContractVersion: ContractVersion,
		Trace: map[string]any{
			"trace_id":         ec.traceID,
			"traceparent":      ec.traceparent,
			"bkn.request.id":   ec.requestID,
			"business_domain":  ec.accountID,
			"bkn.account.id":   ec.accountID,
			"bkn.account.type": ec.accountType,
		},
		Events: events,
	}

	select {
	case evidenceInFlight <- struct{}{}:
	default:
		if logger != nil {
			logger.WithContext(ctx).Warn("BKN Trace evidence ingestion dropped: in-flight limit reached")
		}
		return
	}

	go func() {
		defer func() { <-evidenceInFlight }()
		if err := postBatch(ingestURL, timeout, payload); err != nil && logger != nil {
			logger.WithContext(ctx).Warnf("BKN Trace evidence ingestion unavailable: %v", err)
		}
	}()
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
	flags := "00"
	if spanContext.TraceFlags().IsSampled() {
		flags = "01"
	}
	return eventContext{
		traceID:     spanContext.TraceID().String(),
		spanID:      spanContext.SpanID().String(),
		traceparent: fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:   traceContext.RequestID,
		accountID:   accountID,
		accountType: accountType,
	}, true
}

func buildEvent(ec eventContext, eventType, operationName string, payload map[string]any) Event {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return Event{
		"event_id":                 "evt_" + randomHex(16),
		"event_type":               eventType,
		"bkn.trace.schema.version": ContractVersion,
		"observed_at":              now,
		"emitted_at":               now,
		"producer_module":          ModuleName,
		"trace_id":                 ec.traceID,
		"span_id":                  ec.spanID,
		"bkn.request.id":           ec.requestID,
		"bkn.operation.name":       operationName,
		"payload":                  payload,
	}
}

func randomHex(length int) string {
	if length <= 0 {
		return ""
	}
	buf := make([]byte, (length+1)/2)
	if _, err := rand.Read(buf); err != nil {
		sum := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		return hex.EncodeToString(sum[:])[:length]
	}
	return hex.EncodeToString(buf)[:length]
}

func schemaEvidenceRefs(resp *interfaces.SearchSchemaResp) []map[string]any {
	if resp == nil {
		return nil
	}
	refs := make([]map[string]any, 0, len(resp.ObjectTypes)+len(resp.RelationTypes)+len(resp.ActionTypes)+len(resp.MetricTypes))
	refs = append(refs, conceptRefs("object_type", "schema_ref", resp.ObjectTypes)...)
	refs = append(refs, conceptRefs("relation_type", "schema_ref", resp.RelationTypes)...)
	refs = append(refs, conceptRefs("action_type", "action_ref", resp.ActionTypes)...)
	refs = append(refs, conceptRefs("metric_type", "metric_ref", resp.MetricTypes)...)
	return refs
}

func conceptRefs(kind, refType string, items []any) []map[string]any {
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
			"ref_id":         kind + ":" + id,
			"ref_type":       refType,
			"source_system":  ModuleName,
			"summary_hash":   HashValue(safeConceptSummary(kind, itemMap)),
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
			"partial_reason": []string{"schema_ref_unversioned"},
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

func maxConcepts(req *interfaces.SearchSchemaReq) int {
	if req == nil || req.MaxConcepts == nil {
		return 0
	}
	return *req.MaxConcepts
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func objectInstanceEvidenceRefs(req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) []map[string]any {
	if req == nil || resp == nil || len(resp.Data) == 0 {
		return nil
	}
	refs := make([]map[string]any, 0, len(resp.Data))
	for index, item := range resp.Data {
		identity, ok := objectInstanceIdentity(item)
		if !ok {
			identity = map[string]any{
				"row_index": index,
				"row_hash":  HashValue(item),
			}
		}
		refs = append(refs, map[string]any{
			"ref_id":         "object_instance:" + queryObjectTypeID(req) + ":" + hashSuffix(identity),
			"ref_type":       "row_ref",
			"source_system":  ModuleName,
			"summary_hash":   HashValue(map[string]any{"identity_hash": HashValue(identity), "object_type_id": queryObjectTypeID(req)}),
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
			"partial_reason": []string{"row_ref_unversioned"},
		})
	}
	return refs
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

func queryObjectPropertiesHash(req *interfaces.QueryObjectInstancesReq) string {
	if req == nil {
		return HashValue(nil)
	}
	return HashValue(req.Properties)
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

func queryObjectLimit(req *interfaces.QueryObjectInstancesReq) int {
	if req == nil {
		return 0
	}
	return req.Limit
}

func subgraphEvidenceRefs(resp *interfaces.QueryInstanceSubgraphResp) ([]map[string]any, bool) {
	if resp == nil || resp.Entries == nil {
		return nil, false
	}
	refs := make([]map[string]any, 0)
	seen := make(map[string]struct{})
	truncated := false
	walkSubgraphValue(resp.Entries, func(item map[string]any) bool {
		if identity, ok := objectInstanceIdentity(item); ok {
			ref := map[string]any{
				"ref_id":         "subgraph_instance:" + hashSuffix(identity),
				"ref_type":       "row_ref",
				"source_system":  ModuleName,
				"summary_hash":   HashValue(map[string]any{"identity_hash": HashValue(identity)}),
				"validity":       "observed",
				"version_status": "unversioned",
				"visibility":     "visible",
				"partial_reason": []string{"row_ref_unversioned"},
			}
			if !appendEvidenceRef(&refs, seen, ref) {
				truncated = true
				return false
			}
		}
		return true
	})
	if truncated {
		return refs, true
	}
	walkRelationContainers(resp.Entries, func(item map[string]any) bool {
		if relationID := firstString(item, "relation_type_id", "relation_type"); relationID != "" {
			ref := map[string]any{
				"ref_id":         "relation_type:" + relationID,
				"ref_type":       "schema_ref",
				"source_system":  ModuleName,
				"summary_hash":   HashValue(map[string]any{"relation_id": relationID}),
				"validity":       "observed",
				"version_status": "unversioned",
				"visibility":     "visible",
				"partial_reason": []string{"schema_ref_unversioned"},
			}
			if !appendEvidenceRef(&refs, seen, ref) {
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

func querySubgraphKnID(req *interfaces.QueryInstanceSubgraphReq) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.KnID)
}

func querySubgraphPathHash(req *interfaces.QueryInstanceSubgraphReq) string {
	if req == nil {
		return HashValue(nil)
	}
	return HashValue(req.RelationTypePaths)
}

func querySubgraphIncludeLogicParams(req *interfaces.QueryInstanceSubgraphReq) bool {
	return req != nil && req.IncludeLogicParams
}

func subgraphEntryCount(resp *interfaces.QueryInstanceSubgraphResp) int {
	if resp == nil || resp.Entries == nil {
		return 0
	}
	switch entries := resp.Entries.(type) {
	case []any:
		return len(entries)
	default:
		return 1
	}
}

func hashSuffix(value any) string {
	hash := strings.TrimPrefix(HashValue(value), "sha256:")
	if len(hash) > 24 {
		return hash[:24]
	}
	return hash
}
