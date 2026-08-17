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
	"io"
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
const maxCoreErrorBodyBytes = 4 << 10

type Event map[string]any

type CoreHTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *CoreHTTPError) Error() string {
	parts := make([]string, 0, 2)
	if code := strings.TrimSpace(e.Code); code != "" {
		parts = append(parts, code)
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("BKN Trace Core HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("BKN Trace Core HTTP %d: %s", e.StatusCode, strings.Join(parts, ": "))
}

func (e *CoreHTTPError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
}

type InteractionArtifactType string

const (
	InteractionArtifactQuestion InteractionArtifactType = "question"
	InteractionArtifactResult   InteractionArtifactType = "result"
)

type evidenceOutcomeContextKey struct{}

type evidenceOutcome struct {
	mu           sync.Mutex
	attempted    bool
	durable      bool
	eventIDs     []string
	businessRefs []BusinessRef
}

var (
	evidenceHTTPClient = &http.Client{}
	evidenceInFlight   = make(chan struct{}, maxInFlightEvidenceBatches)
)

type batch struct {
	ContractVersion      string         `json:"bkn.trace.schema.version"`
	Trace                map[string]any `json:"trace"`
	Events               []Event        `json:"events"`
	DeclaredBusinessRefs []BusinessRef  `json:"-"`
}

type eventContext struct {
	traceID          string
	spanID           string
	traceparent      string
	requestID        string
	accountID        string
	accountType      string
	applicationID    string
	applicationName  string
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

func hashArtifactContent(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	raw := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
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
	if !EvidenceEnabled() {
		return "", nil
	}
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
	contentHash, err := hashArtifactContent(content)
	if err != nil {
		return "", fmt.Errorf("canonicalize interaction artifact: %w", err)
	}
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
		"initiator":                "account:" + ec.accountID, "agent_or_app": agentOrApp(ec),
	}
	eventPayload := map[string]any{artifactField: artifactRef, "content_hash": contentHash}
	if ec.applicationName != "" {
		eventPayload["app_ref"] = ec.applicationName
	}
	if err := postArtifactWithRetry(evidenceArtifactURL(), evidenceTimeout(), traceBlock, artifact); err != nil {
		return "", err
	}
	event := buildEvent(
		ec, eventType, "interaction."+string(artifactType),
		eventPayload, "", "",
	)
	event["event_id"] = stableEventID(ec.traceID, ec.interactionID, eventType, 1)
	if err := postBatchWithRetry(evidenceIngestURL(), evidenceTimeout(), batch{
		ContractVersion: ContractVersion, Trace: traceBlock, Events: []Event{event},
	}); err != nil {
		return "", err
	}
	return artifactRef, nil
}

func agentOrApp(ec eventContext) string {
	if ec.applicationName != "" {
		return ec.applicationName
	}
	return ec.applicationID
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
			if resp.StatusCode < http.StatusBadRequest {
				resp.Body.Close()
				cancel()
				return nil
			}
			requestErr = coreHTTPError(resp)
		}
		cancel()
		err = requestErr
		var coreErr *CoreHTTPError
		if errors.As(err, &coreErr) && !coreErr.Retryable() {
			return err
		}
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

func EmitSearchInstanceEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.SearchInstanceReq, resp *interfaces.SearchInstanceResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, req, BuildSearchInstanceEvents(ctx, req, resp))
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

func EmitExploreSubgraphEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.ExploreSubgraphReq, resp *interfaces.ExploreSubgraphResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, req, BuildExploreSubgraphEvents(ctx, req, resp))
}

func EmitRunSQLEvents(ctx context.Context, logger interfaces.Logger, sql string, resourceIDs []string, resp *interfaces.VegaRawQueryResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, nil, BuildRunSQLEvents(ctx, sql, resourceIDs, resp))
}

type RunSQLFailure struct {
	Stage   string
	Code    string
	Summary string
}

func EmitRunSQLFailure(ctx context.Context, logger interfaces.Logger, sql string, resourceIDs []string, failure RunSQLFailure) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, nil, BuildRunSQLFailureEvents(ctx, sql, resourceIDs, failure))
}

func EmitSchemaDefinitionEvents(ctx context.Context, logger interfaces.Logger, kind, knID string, ids []string, matched int) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, nil, BuildSchemaDefinitionEvents(ctx, kind, knID, ids, matched))
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

// BuildSearchInstanceEvents 记录一次语义实例召回。引用按命中的对象类去重登记——
// 实例行本身没有受控标识可引，能确证的是「这些对象类被读过」。
func BuildSearchInstanceEvents(ctx context.Context, req *interfaces.SearchInstanceReq, resp *interfaces.SearchInstanceResp) []Event {
	ec, ok := contextFromRequest(ctx, req)
	if !ok {
		return nil
	}
	query := ""
	knID := ""
	if req != nil {
		query = strings.TrimSpace(req.Query)
		knID = strings.TrimSpace(req.XKnID)
		if knID == "" {
			knID = strings.TrimSpace(req.KnID)
		}
	}
	candidateCount := 0
	var refs []map[string]any
	if resp != nil {
		candidateCount = len(resp.Nodes)
		refs = searchInstanceEvidenceRefs(knID, resp.Nodes)
	}
	return buildRetrievalEvents(ec, "context.search_instance", HashValue(query), candidateCount, false, refs)
}

// searchInstanceEvidenceRefs 把命中的对象类去重成受控引用。
func searchInstanceEvidenceRefs(knID string, nodes []any) []map[string]any {
	if knID == "" || len(nodes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(nodes))
	refs := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		nodeMap, ok := asMap(node)
		if !ok {
			continue
		}
		objectTypeID, _ := nodeMap["object_type_id"].(string)
		if objectTypeID = strings.TrimSpace(objectTypeID); objectTypeID == "" {
			continue
		}
		if _, dup := seen[objectTypeID]; dup {
			continue
		}
		seen[objectTypeID] = struct{}{}
		refs = append(refs, controlledRef("object:"+knID+":"+objectTypeID, "object"))
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
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

func BuildExploreSubgraphEvents(ctx context.Context, req *interfaces.ExploreSubgraphReq, resp *interfaces.ExploreSubgraphResp) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs, refsTruncated := exploreSubgraphEvidenceRefs(req, resp)
	candidateCount := 0
	if resp != nil {
		candidateCount = len(resp.Objects) + len(resp.IsolatedObjects)
	}
	return buildRetrievalEvents(ec, "context.explore_subgraph", exploreSubgraphHash(req), candidateCount, refsTruncated, refs)
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
		key := "data_resource\x00resource:" + resourceID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, map[string]any{
			"ref_id":         "resource:" + resourceID,
			"ref_type":       "data_resource",
			"source_system":  "vega",
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
		})
	}
	resultSummary := runSQLResultSummary(resp)
	event := buildEvent(ec, "data.query.observed", "context.run_sql", map[string]any{
		"query_hash":     HashValue(strings.TrimSpace(sql)),
		"query_type":     "sql",
		"row_count":      resultSummary["row_count"],
		"truncated":      resultSummary["truncated"],
		"version_status": "unversioned",
		"resource_refs":  refs,
		"field_refs":     []map[string]any{},
	}, "", ec.causationEventID)
	event["bkn.trace.schema.version"] = "2.2.0"
	return []Event{event}
}

func BuildRunSQLFailureEvents(ctx context.Context, sql string, resourceIDs []string, failure RunSQLFailure) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs := runSQLResourceRefs(resourceIDs)
	event := buildEvent(ec, "data.query.observed", "context.run_sql", map[string]any{
		"query_hash":         HashValue(strings.TrimSpace(sql)),
		"query_type":         "sql",
		"row_count":          0,
		"truncated":          false,
		"version_status":     "unversioned",
		"resource_refs":      refs,
		"field_refs":         []map[string]any{},
		"status":             "error",
		"error_stage":        failure.Stage,
		"error_code":         failure.Code,
		"safe_error_summary": failure.Summary,
	}, "", ec.causationEventID)
	event["bkn.trace.schema.version"] = "2.2.0"
	return []Event{event}
}

func runSQLResultSummary(resp *interfaces.VegaRawQueryResp) map[string]any {
	count := 0
	truncated := false
	if resp != nil {
		count = len(resp.Entries)
		truncated = resp.Paging != nil && resp.Paging.NextCursor != nil
		if resp.TotalCount != nil && *resp.TotalCount > int64(count) {
			truncated = true
		}
	}
	return map[string]any{"row_count": count, "truncated": truncated}
}

func runSQLResourceRefs(resourceIDs []string) []map[string]any {
	refs := make([]map[string]any, 0, len(resourceIDs))
	seen := map[string]struct{}{}
	for _, resourceID := range resourceIDs {
		resourceID = strings.TrimSpace(resourceID)
		if resourceID == "" {
			continue
		}
		key := "data_resource\x00resource:" + resourceID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, map[string]any{
			"ref_id":         "resource:" + resourceID,
			"ref_type":       "data_resource",
			"source_system":  "vega",
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
		})
	}
	return refs
}

func BuildSchemaDefinitionEvents(ctx context.Context, kind, knID string, ids []string, matched int) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok || strings.TrimSpace(knID) == "" {
		return nil
	}
	refType := ""
	switch kind {
	case "object":
		refType = "object"
	case "relation":
		refType = "relation"
	default:
		return nil
	}
	refs := []map[string]any{{
		"ref_id": "kn:" + knID, "ref_type": "knowledge_network",
		"source_system": ModuleName, "validity": "observed",
		"version_status": "unversioned", "visibility": "visible",
	}}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		refs = append(refs, map[string]any{
			"ref_id": kind + ":" + knID + ":" + id, "ref_type": refType,
			"source_system": ModuleName, "validity": "observed",
			"version_status": "unversioned", "visibility": "visible",
		})
	}
	return buildRetrievalEvents(ec, "context.get_"+kind+"_types", HashValue(strings.Join(ids, "\x00")), matched, false, refs)
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
	recordEvidenceAttempt(ctx)
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
		ContractVersion:      ContractVersion,
		Trace:                traceBlock,
		Events:               events,
		DeclaredBusinessRefs: declaredBusinessRefsFromContext(ctx),
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

type declaredBusinessRefsContextKey struct{}

func withDeclaredBusinessRefs(ctx context.Context, refs []BusinessRef) context.Context {
	if len(refs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, declaredBusinessRefsContextKey{}, append([]BusinessRef(nil), refs...))
}

func declaredBusinessRefsFromContext(ctx context.Context) []BusinessRef {
	refs, _ := ctx.Value(declaredBusinessRefsContextKey{}).([]BusinessRef)
	return refs
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
		for _, ref := range trace30BusinessRefs(event, stringValue(payload.Trace["business_domain"]), payload.DeclaredBusinessRefs) {
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

func recordEvidenceAttempt(ctx context.Context) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return
	}
	outcome.mu.Lock()
	outcome.attempted = true
	outcome.mu.Unlock()
}

func snapshotEvidenceOutcome(ctx context.Context) (bool, bool, []string, []BusinessRef) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return false, false, nil, nil
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	return outcome.attempted, outcome.durable, append([]string(nil), outcome.eventIDs...), append([]BusinessRef(nil), outcome.businessRefs...)
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
		requestPayload, err := trace30EvidenceEvent(payload.Trace, event, payload.DeclaredBusinessRefs)
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
		if resp.StatusCode >= http.StatusBadRequest {
			err = coreHTTPError(resp)
			cancel()
			return err
		}
		resp.Body.Close()
		cancel()
	}
	return nil
}

func coreHTTPError(resp *http.Response) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCoreErrorBodyBytes+1))
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	if payload.Error.Code != "" || payload.Error.Message != "" {
		payload.Code = payload.Error.Code
		payload.Message = payload.Error.Message
	}
	return &CoreHTTPError{
		StatusCode: resp.StatusCode,
		Code:       strings.TrimSpace(payload.Code),
		Message:    strings.TrimSpace(payload.Message),
	}
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

func trace30EvidenceEvent(traceBlock map[string]any, event Event, declaredRefs []BusinessRef) (trace30Event, error) {
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
	refs := trace30BusinessRefs(event, stringValue(traceBlock["business_domain"]), declaredRefs)
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

func trace30BusinessRefs(event Event, businessDomain string, declaredRefs []BusinessRef) []trace30BusinessRef {
	payload, _ := event["payload"].(map[string]any)
	items := make([]map[string]any, 0)
	for _, field := range []string{"source_refs", "resource_refs", "field_refs"} {
		if values, ok := payload[field].([]map[string]any); ok {
			items = append(items, values...)
		}
	}
	refs := make([]trace30BusinessRef, 0)
	seen := map[string]struct{}{}
	declaredVersions := make(map[string]string, len(declaredRefs))
	for _, ref := range declaredRefs {
		declaredVersions[ref.RefType+"\x00"+ref.RefID] = ref.Version
	}
	for _, item := range items {
		refID := stringValue(item["ref_id"])
		refType := trace30RefType(stringValue(item["ref_type"]))
		if refID == "" || refType == "" || businessDomain == "" {
			continue
		}
		version := strings.TrimSpace(declaredVersions[refType+"\x00"+refID])
		if version == "" {
			version = stringValue(item["version_status"])
		}
		if version == "" {
			version = "unversioned"
		}
		key := refType + "\x00" + refID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, trace30BusinessRef{
			RefType: refType, RefID: refID, BusinessDomainID: businessDomain,
			Version: version, DisplayHint: stringValue(item["display_hint"]),
		})
	}
	for _, ref := range declaredRefs {
		refType := trace30RefType(ref.RefType)
		refID := strings.TrimSpace(ref.RefID)
		if refType == "" || refID == "" || businessDomain == "" {
			continue
		}
		key := refType + "\x00" + refID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		version := strings.TrimSpace(ref.Version)
		if version == "" {
			version = "unversioned"
		}
		refs = append(refs, trace30BusinessRef{
			RefType: refType, RefID: refID, BusinessDomainID: businessDomain,
			Version: version, DisplayHint: ref.DisplayHint,
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
	if token := strings.TrimSpace(os.Getenv(envEvidenceIngestToken)); token != "" {
		headers.Set("X-BKN-Trace-Ingest-Token", token)
	}
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
	// 账户身份可能只在请求体/头上，没进 ctx（REST 面就是这样）。缺了它
	// SubmitEvents 会静默不发事件，所以每个带身份字段的请求类型都要在这里回填。
	switch typed := req.(type) {
	case *interfaces.SearchSchemaReq:
		if typed != nil {
			if ec.accountID == "" {
				ec.accountID = strings.TrimSpace(typed.XAccountID)
			}
			if ec.accountType == "" {
				ec.accountType = strings.TrimSpace(typed.XAccountType)
			}
		}
	case *interfaces.SearchInstanceReq:
		if typed != nil {
			if ec.accountID == "" {
				ec.accountID = strings.TrimSpace(typed.XAccountID)
			}
			if ec.accountType == "" {
				ec.accountType = strings.TrimSpace(typed.XAccountType)
			}
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
	applicationName, _ := common.GetApplicationDisplayNameFromCtx(ctx)
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
		applicationName:  applicationName,
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
	// TotalCount 缺失表示下游没算总数（游标翻页第二页起如此），不是零命中；
	// 这条路上游已被 SearchAfter 分支接走，这里只需不把 nil 当 0。
	if req == nil || resp.TotalCount == nil || *resp.TotalCount <= 0 {
		return false
	}
	return int64(req.Offset+len(resp.Data)) < *resp.TotalCount
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

// exploreSubgraphEvidenceRefs 从**响应**取证据引用，而不是像路径模板模式那样从请求取。
// 探索模式的请求里只有一个起点对象类，命中哪些对象类与关系类是引擎跑完才知道的，
// 照着请求提取等于只记一个起点，证据链就废了。
func exploreSubgraphEvidenceRefs(req *interfaces.ExploreSubgraphReq, resp *interfaces.ExploreSubgraphResp) ([]map[string]any, bool) {
	if req == nil || resp == nil {
		return nil, false
	}
	knID := strings.TrimSpace(req.KnID)
	refs := make([]map[string]any, 0)
	if knID == "" {
		return refs, false
	}
	seen := make(map[string]struct{})
	truncated := false

	collectObjects := func(objects map[string]any) {
		for _, value := range objects {
			if truncated {
				return
			}
			item, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if id := firstString(item, "object_type_id"); id != "" &&
				!appendEvidenceRef(&refs, seen, controlledRef("object:"+knID+":"+id, "object")) {
				truncated = true
				return
			}
		}
	}
	collectObjects(resp.Objects)
	collectObjects(resp.IsolatedObjects)

	if !truncated {
		walkSubgraphValue(resp.RelationPaths, func(item map[string]any) bool {
			if id := firstString(item, "relation_type_id"); id != "" &&
				!appendEvidenceRef(&refs, seen, controlledRef("relation:"+knID+":"+id, "relation")) {
				truncated = true
				return false
			}
			return true
		})
	}
	return refs, truncated
}

// exploreSubgraphHash 摘要的是「这次探索问的是什么」——起点、方向、跳数、起点过滤与
// 概念分组。分页字段不进摘要：翻页不改变问题本身。
func exploreSubgraphHash(req *interfaces.ExploreSubgraphReq) string {
	if req == nil {
		return HashValue(nil)
	}
	return HashValue(map[string]any{
		"source_object_type_id":   req.SourceObjectTypeID,
		"direction":               req.Direction,
		"path_length":             req.PathLength,
		"concept_groups":          req.ConceptGroups,
		"condition":               req.Cond,
		"include_incomplete_path": req.IncludeIncompletePath,
	})
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
