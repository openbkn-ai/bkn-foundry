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
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"go.opentelemetry.io/otel/trace"
)

const (
	ContractVersion = "2.1.0"
	ModuleName      = "context-loader"
)

const (
	envArtifactEndpoint  = "BKN_TRACE_ARTIFACT_ENDPOINT"
	envArtifactToken     = "BKN_TRACE_ARTIFACT_TOKEN"
	envArtifactTimeoutMS = "BKN_TRACE_ARTIFACT_TIMEOUT_MS"
)

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
	mu        sync.Mutex
	attempted bool
	accepted  bool
}

var artifactHTTPClient = &http.Client{}

type eventContext struct {
	traceID           string
	spanID            string
	traceparent       string
	requestID         string
	accountID         string
	accountType       string
	applicationID     string
	applicationName   string
	subjectType       string
	conversationID    string
	interactionID     string
	operationID       string
	parentOperationID string
	causationEventID  string
	claimID           string
	attempt           int
	observedAt        string
}

// HashValue is ConfigStd, not the default sonic config, and the difference is not cosmetic: the
// default config neither sorts map keys nor escapes HTML, so hashing a map returns a different
// digest on every call - Go randomises map iteration order. ConfigStd reproduces encoding/json
// byte for byte, which is what every peer that recomputes one of these digests still uses.
func HashValue(value any) string {
	raw, err := sonic.ConfigStd.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hashArtifactContent(value any) (string, error) {
	raw, err := canonicalArtifactContent(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalArtifactContent mirrors Core's artifact-content normalization so the
// digest remains valid after the artifact crosses the HTTP JSON boundary.
func canonicalArtifactContent(value any) ([]byte, error) {
	// Content first crosses the HTTP JSON encoder before Core decodes it. Round
	// trip here as well so replacement characters for malformed UTF-8 and JSON
	// number precision have exactly the representation Core will canonicalize.
	transport, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(transport))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func EvidenceEnabled() bool {
	return currentEvidencePublisher() != nil
}

func artifactEnabled() bool { return strings.TrimSpace(os.Getenv(envArtifactEndpoint)) != "" }

func RecordInteractionArtifact(
	ctx context.Context,
	conversationID string,
	interactionID string,
	artifactType InteractionArtifactType,
	content any,
) (string, error) {
	if !artifactEnabled() {
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
		"artifact_id":              artifactID,
		"artifact_type":            string(artifactType),
		"bkn.request.id":           ec.requestID,
		"trace_id":                 ec.traceID,
		"interaction_id":           ec.interactionID,
		"content_type":             "application/json",
		"schema_version":           "2.2.0",
		"observed_at":              ec.observedAt,
		"content_hash":             contentHash,
		"content":                  content,
		"bkn.account.id":           ec.accountID,
		"bkn.account.type":         ec.accountType,
		"effective_subject_id":     ec.accountID,
		"application_principal_id": ec.applicationID,
		"initiator":                "account:" + ec.accountID,
		"agent_or_app":             agentOrApp(ec),
	}
	eventPayload := map[string]any{artifactField: artifactRef, "content_hash": contentHash}
	if ec.applicationName != "" {
		eventPayload["app_ref"] = ec.applicationName
	}
	if err := postArtifactWithRetry(evidenceArtifactURL(), artifactTimeout(), traceBlock, artifact); err != nil {
		return "", err
	}
	event := buildEvent(
		ec, eventType, "interaction."+string(artifactType),
		eventPayload, "", "",
	)
	event["event_id"] = stableEventID(ec.traceID, ec.interactionID, eventType, 1)
	// Artifact persistence is the governed operation. The supplementary Kafka
	// event is best effort and must not turn a committed artifact into a failure.
	if currentEvidencePublisher() != nil {
		if result := publishEvidenceEvent(event); result.Disposition != evidencepublisher.Accepted {
			log.Printf("BKN Trace Kafka artifact event dropped: %s", result.Reason)
		}
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
		return ErrEvidenceArtifactURLNotConfigured
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
		setArtifactHeaders(req.Header, traceBlock)
		resp, requestErr := artifactHTTPClient.Do(req)
		if requestErr == nil {
			if resp.StatusCode < http.StatusBadRequest {
				_ = resp.Body.Close()
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

func EmitQueryMetricEvents(ctx context.Context, logger interfaces.Logger, req *interfaces.QueryMetricReq, resp *interfaces.QueryMetricResp) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, req, BuildQueryMetricEvents(ctx, req, resp))
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

// EmitRunCypherEvents records one compiled Cypher query as observed data
// access. The generated SQL and the resources it touched stay inside
// bkn-backend, so the evidence names the knowledge network the query ran
// against and hashes the query text -- enough to tie an answer back to what
// was asked, without re-exposing the physical model.
func EmitRunCypherEvents(ctx context.Context, logger interfaces.Logger, knID, query string, rowCount int, descriptor json.RawMessage) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, nil, BuildRunCypherEvents(ctx, knID, query, rowCount, descriptor))
}

// RunCypherFailure describes why one Cypher query produced no rows.
type RunCypherFailure struct {
	Stage   string
	Code    string
	Summary string
}

// EmitRunCypherFailure records a refused or failed Cypher query. A refusal is
// evidence too: it says the answer was not supported by data.
func EmitRunCypherFailure(ctx context.Context, logger interfaces.Logger, knID, query string, failure RunCypherFailure) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, nil, BuildRunCypherFailureEvents(ctx, knID, query, failure))
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

func EmitSchemaSnapshotEvents(ctx context.Context, logger interfaces.Logger, kind, knID string, ids []string, definition any, complete bool) string {
	if !EvidenceEnabled() {
		return ""
	}
	return submitAndReturnFirstEventID(ctx, logger, nil, BuildSchemaSnapshotEvents(ctx, kind, knID, ids, definition, complete))
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

// BuildSearchInstanceEvents records a semantic instance recall. References are deduplicated and registered by hit object type.
// The instance rows themselves have no controlled identifiers to reference; all that can be confirmed is that "these object types were read.".
func BuildSearchInstanceEvents(ctx context.Context, req *interfaces.SearchInstanceReq, resp *interfaces.SearchInstanceResp) []Event {
	ec, ok := contextFromRequest(ctx, req)
	if !ok {
		return nil
	}
	query := ""
	knID := ""
	if req != nil {
		query = strings.TrimSpace(req.Query)
		knID = req.ResolvedKnID()
	}
	candidateCount := 0
	var refs []map[string]any
	if resp != nil {
		candidateCount = len(resp.Nodes)
		refs = searchInstanceEvidenceRefs(knID, resp.Nodes)
	}
	return buildRetrievalEvents(ec, "context.search_instance", HashValue(query), candidateCount, false, refs)
}

// searchInstanceEvidenceRefs converts the hit object type into a controlled reference.
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

func BuildQueryMetricEvents(ctx context.Context, req *interfaces.QueryMetricReq, resp *interfaces.QueryMetricResp) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok || req == nil || strings.TrimSpace(req.KnID) == "" || strings.TrimSpace(req.MetricID) == "" {
		return nil
	}
	rows := []map[string]any{}
	if resp != nil {
		for _, series := range resp.Datas {
			if series == nil {
				continue
			}
			for index, value := range series.Values {
				row := map[string]any{"dimensions": series.Labels, "value": value}
				if index < len(series.Times) {
					row["time"] = series.Times[index]
				}
				if index < len(series.TimeStrs) {
					row["time_text"] = series.TimeStrs[index]
				}
				rows = append(rows, row)
			}
		}
	}
	metricRef := "metric:" + strings.TrimSpace(req.KnID) + ":" + strings.TrimSpace(req.MetricID)
	payload := map[string]any{
		"metric_ref": metricRef, "network_ref": "kn:" + strings.TrimSpace(req.KnID),
		"dimensions": append([]string(nil), req.AnalysisDimensions...), "conditions": req.Cond,
		"time": req.Time, "having": req.Having, "order_by": req.OrderBy,
		"aggregation_definition": "defined_by_metric", "rows": rows, "row_count": len(rows),
		"complete": resp != nil, "cardinality": "metric_series_rows",
		"source_refs": []map[string]any{
			controlledRef("kn:"+strings.TrimSpace(req.KnID), "knowledge_network"),
			controlledRef(metricRef, "metric"),
		},
	}
	event := buildEvent(ec, "metric.query.completed", "context.query_metric", payload, "", ec.causationEventID)
	event["bkn.trace.schema.version"] = "2.2.0"
	return []Event{event}
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

// BuildRunCypherEvents builds the observed-data event for one Cypher query.
func BuildRunCypherEvents(ctx context.Context, knID, query string, rowCount int, descriptor json.RawMessage) []Event {
	descriptorPayload := descriptorValue(descriptor)
	truncated := false
	truncationReason := ""
	if descriptorMap, ok := descriptorPayload.(map[string]any); ok {
		limitSource, _ := descriptorMap["limit_source"].(string)
		effectiveLimit, hasLimit := descriptorMap["effective_limit"].(float64)
		if limitSource == "default" && hasLimit && effectiveLimit > 0 && rowCount >= int(effectiveLimit) {
			truncated = true
			truncationReason = "default_limit_reached"
		}
	}
	return buildRunCypherEvent(ctx, knID, query, map[string]any{
		"row_count": rowCount, "truncated": truncated,
		"semantic_query_descriptor":  descriptorPayload,
		"semantic_descriptor_status": descriptorStatus(descriptor),
		"truncation_reason":          truncationReason,
	})
}

// BuildRunCypherFailureEvents builds the observed-data event for a Cypher
// query that was refused or failed.
func BuildRunCypherFailureEvents(ctx context.Context, knID, query string, failure RunCypherFailure) []Event {
	return buildRunCypherEvent(ctx, knID, query, map[string]any{
		"row_count":          0,
		"truncated":          false,
		"status":             "error",
		"error_stage":        failure.Stage,
		"error_code":         failure.Code,
		"safe_error_summary": failure.Summary,
	})
}

func buildRunCypherEvent(ctx context.Context, knID, query string, extra map[string]any) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok {
		return nil
	}
	refs := []map[string]any{}
	if knID = strings.TrimSpace(knID); knID != "" {
		refs = append(refs, map[string]any{
			"ref_id": "kn:" + knID, "ref_type": "knowledge_network",
			"source_system": ModuleName, "validity": "observed",
			"version_status": "unversioned", "visibility": "visible",
		})
	}
	payload := map[string]any{
		"query_hash":     HashValue(strings.TrimSpace(query)),
		"query_type":     "cypher",
		"version_status": "unversioned",
		"resource_refs":  refs,
		"field_refs":     []map[string]any{},
	}
	if descriptor, ok := extra["semantic_query_descriptor"].(map[string]any); ok {
		resources, fields := semanticDescriptorRefs(descriptor)
		payload["resource_refs"] = append(refs, resources...)
		payload["field_refs"] = fields
	}
	for key, value := range extra {
		payload[key] = value
	}
	event := buildEvent(ec, "data.query.observed", "context.run_cypher", payload, "", ec.causationEventID)
	event["bkn.trace.schema.version"] = "2.2.0"
	return []Event{event}
}

func descriptorValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]any
	if sonic.ConfigStd.Unmarshal(raw, &value) != nil ||
		stringValue(value["version"]) != "semantic-query-descriptor/v1" ||
		stringValue(value["producer_profile"]) == "" {
		return nil
	}
	return value
}

func descriptorStatus(raw json.RawMessage) string {
	if descriptorValue(raw) == nil {
		return "missing"
	}
	return "available"
}

func semanticDescriptorRefs(descriptor map[string]any) ([]map[string]any, []map[string]any) {
	resources := []map[string]any{}
	fields := []map[string]any{}
	seen := map[string]struct{}{}
	appendRef := func(target *[]map[string]any, refID, refType string) {
		if refID == "" {
			return
		}
		key := refType + "\x00" + refID
		if _, found := seen[key]; found {
			return
		}
		seen[key] = struct{}{}
		*target = append(*target, map[string]any{
			"ref_id": refID, "ref_type": refType, "source_system": "bkn",
			"validity": "observed", "version_status": "0.1.5", "visibility": "visible",
		})
	}
	for _, item := range objectArray(descriptor["objects"]) {
		appendRef(&resources, stringValue(item["object_ref"]), "object")
	}
	for _, item := range objectArray(descriptor["relations"]) {
		appendRef(&resources, stringValue(item["relation_ref"]), "relation")
	}
	for _, collection := range []string{"predicates", "projections", "grouping", "ordering"} {
		for _, item := range objectArray(descriptor[collection]) {
			appendRef(&fields, stringValue(item["property_ref"]), "property")
		}
	}
	if values, ok := descriptor["grouping"].([]any); ok {
		for _, value := range values {
			appendRef(&fields, stringValue(value), "property")
		}
	}
	return resources, fields
}

func objectArray(value any) []map[string]any {
	values, _ := value.([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
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

func BuildSchemaSnapshotEvents(ctx context.Context, kind, knID string, ids []string, definition any, complete bool) []Event {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok || strings.TrimSpace(knID) == "" {
		return nil
	}
	refs := []map[string]any{controlledRef("kn:"+strings.TrimSpace(knID), "knowledge_network")}
	refType := kind
	if kind == "network" {
		refType = "knowledge_network"
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		refs = append(refs, controlledRef(kind+":"+strings.TrimSpace(knID)+":"+id, refType))
	}
	payload := map[string]any{
		"network_ref": "kn:" + strings.TrimSpace(knID), "schema_kind": kind,
		"definition_refs": refs, "definition_count": schemaDefinitionCount(kind, definition), "complete": schemaSnapshotComplete(kind, definition, complete),
		"definition": definition, "source_refs": refs,
	}
	event := buildEvent(ec, "ontology.schema.snapshot", "context.get_"+kind+"_schema", payload, "", ec.causationEventID)
	event["bkn.trace.schema.version"] = "2.2.0"
	return []Event{event}
}

func schemaDefinitionCount(kind string, definition any) int {
	if definition == nil {
		return 0
	}
	if kind == "network" {
		count, _ := mountedCapabilityTotal(definition)
		return count
	}
	value := reflect.ValueOf(definition)
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map:
		return value.Len()
	default:
		return 1
	}
}

func schemaSnapshotComplete(kind string, definition any, complete bool) bool {
	if kind != "network" {
		return complete
	}
	_, known := mountedCapabilityTotal(definition)
	return complete && known
}

func mountedCapabilityTotal(definition any) (int, bool) {
	raw, err := json.Marshal(definition)
	if err != nil {
		return 0, false
	}
	var snapshot struct {
		MountedCapabilities *struct {
			Total int `json:"total"`
		} `json:"mounted_capabilities"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.MountedCapabilities == nil || snapshot.MountedCapabilities.Total < 0 {
		return 0, false
	}
	return snapshot.MountedCapabilities.Total, true
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
	if currentEvidencePublisher() == nil {
		return nil
	}
	recordEvidenceAttempt(ctx)
	for _, event := range events {
		if stringValue(event["conversation_id"]) == "" && ec.conversationID != "" {
			event["conversation_id"] = ec.conversationID
		}
		if result := publishEvidenceEvent(event); result.Disposition != evidencepublisher.Accepted {
			if logger != nil {
				logger.WithContext(ctx).Warnf("BKN Trace Kafka evidence dropped: %s", result.Reason)
			} else {
				log.Printf("BKN Trace Kafka evidence dropped: %s", result.Reason)
			}
			continue
		}
		recordQueuedEvidenceOutcome(ctx)
	}
	return nil
}

func withEvidenceOutcome(ctx context.Context) context.Context {
	if evidenceOutcomeFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, evidenceOutcomeContextKey{}, &evidenceOutcome{})
}

type requestDerivedBusinessRefsContextKey struct{}

func withRequestDerivedBusinessRefs(ctx context.Context, refs []BusinessRef) context.Context {
	if len(refs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, requestDerivedBusinessRefsContextKey{}, append([]BusinessRef(nil), refs...))
}

func requestDerivedBusinessRefsFromContext(ctx context.Context) []BusinessRef {
	refs, _ := ctx.Value(requestDerivedBusinessRefsContextKey{}).([]BusinessRef)
	return refs
}

func evidenceOutcomeFromContext(ctx context.Context) *evidenceOutcome {
	value, _ := ctx.Value(evidenceOutcomeContextKey{}).(*evidenceOutcome)
	return value
}

func recordQueuedEvidenceOutcome(ctx context.Context) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return
	}
	outcome.mu.Lock()
	outcome.accepted = true
	outcome.mu.Unlock()
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

func snapshotEvidenceOutcome(ctx context.Context) (bool, bool) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return false, false
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	return outcome.attempted, outcome.accepted
}

func coreHTTPError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCoreErrorBodyBytes+1))
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = sonic.Unmarshal(body, &payload)
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
	RefType     string `json:"ref_type"`
	RefID       string `json:"ref_id"`
	Version     string `json:"version"`
	DisplayHint string `json:"display_hint,omitempty"`
}

type trace30OperationEdge struct {
	OperationID string             `json:"operation_id"`
	BusinessRef trace30BusinessRef `json:"business_ref"`
	Role        string             `json:"role"`
	ObservedAt  string             `json:"observed_at"`
}

func trace30EvidenceEvent(traceBlock map[string]any, event Event, derivedRefs []BusinessRef) (trace30Event, error) {
	// agent-observability admits an event only if payload_hash equals the canonical hash of the
	// envelope it receives (ledgervo.CanonicalPayloadHash), so the digest is taken over that same
	// canonical form rather than over these bytes. Hashing the bytes as marshalled held only while
	// every value in the envelope already came out the way encoding/json writes it: default sonic
	// left map keys unsorted and every event was rejected (#1092), and ConfigStd still writes struct
	// fields in declaration order, so the schema snapshot, whose definition is a struct, was
	// rejected on every call (#1711). ConfigStd stays for sorted, stable output.
	envelope, err := sonic.ConfigStd.Marshal(event)
	if err != nil {
		return trace30Event{}, err
	}
	payloadHash := canonicalPayloadHash(envelope)
	operationID := stringValue(event["operation_id"])
	attempt := uint32(intValue(event["attempt"], 1))
	observedAt := stringValue(event["observed_at"])
	emittedAt := stringValue(event["emitted_at"])
	if emittedAt == "" {
		emittedAt = observedAt
	}
	refs := trace30BusinessRefs(event, derivedRefs)
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
		EventType: stringValue(event["event_type"]), PayloadHash: payloadHash,
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

// canonicalPayloadHash reproduces agent-observability's ledgervo.CanonicalPayloadHash, the value
// the ledger compares payload_hash against: decode into generic JSON, re-encode with
// encoding/json, sha256. Keep it on encoding/json rather than sonic - escaping and float
// formatting have to match the receiver byte for byte. The shared test vector in both modules
// fails if either side changes the algorithm.
func canonicalPayloadHash(envelope []byte) string {
	var decoded any
	if err := json.Unmarshal(envelope, &decoded); err == nil {
		if canonical, err := json.Marshal(decoded); err == nil {
			envelope = canonical
		}
	}
	sum := sha256.Sum256(envelope)
	return hex.EncodeToString(sum[:])
}

func trace30BusinessRefs(event Event, derivedRefs []BusinessRef) []trace30BusinessRef {
	payload, _ := event["payload"].(map[string]any)
	items := make([]map[string]any, 0)
	for _, field := range []string{"source_refs", "resource_refs", "field_refs"} {
		if values, ok := payload[field].([]map[string]any); ok {
			items = append(items, values...)
		}
	}
	refs := make([]trace30BusinessRef, 0)
	seen := map[string]struct{}{}
	derivedVersions := make(map[string]string, len(derivedRefs))
	for _, ref := range derivedRefs {
		derivedVersions[ref.RefType+"\x00"+ref.RefID] = ref.Version
	}
	for _, item := range items {
		refID := stringValue(item["ref_id"])
		refType := trace30RefType(stringValue(item["ref_type"]))
		if refID == "" || refType == "" {
			continue
		}
		version := strings.TrimSpace(derivedVersions[refType+"\x00"+refID])
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
			RefType: refType, RefID: refID,
			Version: version, DisplayHint: stringValue(item["display_hint"]),
		})
	}
	for _, ref := range derivedRefs {
		refType := trace30RefType(ref.RefType)
		refID := strings.TrimSpace(ref.RefID)
		if refType == "" || refID == "" {
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
			RefType: refType, RefID: refID,
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

func setArtifactHeaders(headers http.Header, traceBlock map[string]any) {
	headers.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(os.Getenv(envArtifactToken)); token != "" {
		headers.Set("X-BKN-Trace-Ingest-Token", token)
	}
	accountID := stringValue(traceBlock["bkn.account.id"])
	accountType := stringValue(traceBlock["bkn.account.type"])
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
	case json.Number:
		// Downstream bodies are decoded with UseNumber so that wide integers keep
		// their digits; see drivenadapters.precisionJSON.
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
		return fallback
	default:
		return fallback
	}
}

// floatValue reads a JSON number that may have been decoded either as float64
// (plain encoding/json) or as json.Number (the UseNumber decoders that keep wide
// integers intact).
func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func evidenceArtifactURL() string {
	return strings.TrimSpace(os.Getenv(envArtifactEndpoint))
}

func artifactTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv(envArtifactTimeoutMS))
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
	// The account identity may only be in the request body/header, not in ctx (this is the case in REST). missing it.
	// SubmitEvents will silently send no events, so every request type with an identity field must be backfilled here.
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
		traceID:           spanContext.TraceID().String(),
		spanID:            spanContext.SpanID().String(),
		traceparent:       fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:         traceContext.RequestID,
		accountID:         accountID,
		accountType:       accountType,
		applicationID:     applicationID,
		applicationName:   applicationName,
		subjectType:       subjectType,
		conversationID:    strings.TrimSpace(traceContext.ConversationID),
		interactionID:     strings.TrimSpace(traceContext.InteractionID),
		operationID:       strings.TrimSpace(traceContext.OperationID),
		parentOperationID: strings.TrimSpace(traceContext.ParentOperationID),
		causationEventID:  strings.TrimSpace(traceContext.CausationEventID),
		claimID:           strings.TrimSpace(traceContext.ClaimID),
		attempt:           traceContext.Attempt,
		observedAt:        observedAt,
	}, true
}

func traceBlockFromEventContext(ec eventContext) map[string]any {
	return map[string]any{
		"trace_id": ec.traceID, "traceparent": ec.traceparent,
		"bkn.request.id":               ec.requestID,
		"bkn.account.id":               ec.accountID,
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
	if ec.parentOperationID != "" {
		event["parent_operation_id"] = ec.parentOperationID
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
	raw, err := sonic.ConfigStd.Marshal(value)
	if err != nil {
		return nil, false
	}
	var itemMap map[string]any
	if err := common.UnmarshalPreciseJSON(raw, &itemMap); err != nil {
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
	score, ok := floatValue(item["_score"])
	if !ok {
		score, ok = floatValue(item["score"])
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
	return req.ResolvedKnID()
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

//nolint:unused // Retained for evidence identity extraction.
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
		"condition": req.Cond,
		"cursor":    req.Cursor,
		"filters":   req.Filters,
		"offset":    req.Offset,
	})
}

func queryObjectTruncated(req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) bool {
	if resp == nil {
		return false
	}
	if resp.Cursor != "" {
		return true
	}
	// Missing TotalCount means that the downstream has not calculated the total (this is true from the second page of the cursor), and it is not a zero hit;
	// This path has been picked up upstream by the Cursor branch, so just don't treat nil as 0.
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

//nolint:unused // Retained for relation evidence traversal.
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

//nolint:unused // Retained for relation evidence traversal.
func isRelationContainerKey(key string) bool {
	switch key {
	case "relation", "relations", "relation_path", "relation_paths", "relation_type", "relation_types":
		return true
	default:
		return false
	}
}

// exploreSubgraphEvidenceRefs takes the evidence reference from the response, not from the request like in path template mode.
// There is only one starting point object type in the exploration mode request. Which object types and relation types are hit will only be known after the engine runs.
// Extracting according to the request means that only one starting point is recorded, and the evidence chain is abolished.
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

// exploreSubgraphHash summarizes "What is this exploration asking?" - starting point, direction, number of hops, starting point filtering and.
// Concept grouping. Pagination fields do not enter the summary: turning pages does not change the question itself.
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

//nolint:unused // Retained for evidence identity hashing.
func hashSuffix(value any) string {
	hash := strings.TrimPrefix(HashValue(value), "sha256:")
	if len(hash) > 24 {
		return hash[:24]
	}
	return hash
}
