package evidencesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
)

var (
	traceparentRE   = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)
	traceIDRE       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDRE        = regexp.MustCompile(`^[0-9a-f]{16}$`)
	requestIDRE     = regexp.MustCompile(`^req_[0-9A-Za-z_.-]+$`)
	timestampRE     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$`)
	hashRE          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	controlledRefRE = regexp.MustCompile(`^[a-z][a-z0-9_.-]*:[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
)

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)\b(access|refresh|id)[_-]?token\s*[:=]\s*[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)\bapi[_-]?key\s*[:=]\s*[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)\bcookie\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?is)\bselect\b.+\bfrom\b`),
	regexp.MustCompile(`(?is)\bupdate\b.+\bset\b`),
	regexp.MustCompile(`(?is)\binsert\s+into\b`),
	regexp.MustCompile(`(?is)\bdelete\s+from\b`),
	regexp.MustCompile(`(?is)\bmerge\s+into\b`),
	regexp.MustCompile(`(?is)\b(alter|drop|create|truncate)\s+(table|view|index|database|schema)\b`),
	regexp.MustCompile(`(?is)\b(grant|revoke)\b.+\bon\b`),
	regexp.MustCompile(`(?i)https?://`),
	regexp.MustCompile(`(?i)\b(?:s3|oss|obs|cos|gs)://`),
	regexp.MustCompile(`(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`),
	regexp.MustCompile(`(?:^|\D)1[3-9]\d{9}(?:\D|$)`),
	regexp.MustCompile(`(?i)\b(?:physical[_-]?(?:table|field)|table[_-]?name|column[_-]?name|field[_-]?name)[.:/_-][A-Za-z0-9_.-]+`),
}

var forbiddenRawKeys = map[string]struct{}{
	"access-token":     {},
	"access_token":     {},
	"api-key":          {},
	"api_key":          {},
	"authorization":    {},
	"cookie":           {},
	"id-token":         {},
	"id_token":         {},
	"password":         {},
	"private-key":      {},
	"private_key":      {},
	"prompt":           {},
	"user-question":    {},
	"user_question":    {},
	"approval-comment": {},
	"approval_comment": {},
	"sql":              {},
	"sql-params":       {},
	"sql_params":       {},
	"raw-answer":       {},
	"raw-input":        {},
	"raw-output":       {},
	"raw-prompt":       {},
	"raw-sql":          {},
	"raw-tool-args":    {},
	"raw-tool-io":      {},
	"raw-tool-result":  {},
	"raw_answer":       {},
	"raw_input":        {},
	"raw_output":       {},
	"raw_prompt":       {},
	"raw_sql":          {},
	"raw_tool_args":    {},
	"raw_tool_io":      {},
	"raw_tool_result":  {},
	"refresh-token":    {},
	"refresh_token":    {},
	"row-data":         {},
	"row_data":         {},
	"token":            {},
}

var legacyEventTypes = map[string]struct{}{
	"claim.created":               {},
	"evidence.refs.created":       {},
	"business.refs.resolved":      {},
	"structured_output.validated": {},
	"agent_as_tool.invoked":       {},
	"tool.budget.exhausted":       {},
	"tool.called":                 {},
	"tool.result.observed":        {},
	"action.recommended":          {},
	"action.approval_requested":   {},
	"action.approved":             {},
	"action.rejected":             {},
	"action.executed":             {},
	"action.result_recorded":      {},
}

var twoPointOneEventTypes = func() map[string]struct{} {
	types := make(map[string]struct{}, 16)
	for _, eventType := range []string{
		"agent.interaction.started",
		"retrieval.completed",
		"knowledge.read.observed",
		"data.query.observed",
		"model.call.observed",
		"tool.called",
		"tool.result.observed",
		"claim.created",
		"evidence.refs.created",
		"business.refs.resolved",
		"action.recommended",
		"action.approval_requested",
		"action.approved",
		"action.rejected",
		"action.executed",
		"action.result_recorded",
	} {
		types[eventType] = struct{}{}
	}
	return types
}()

var twoPointOnePayloadFields = map[string]map[string]struct{}{
	"agent.interaction.started": fields("intent_hash", "mode", "agent_id", "app_ref"),
	"retrieval.completed":       fields("query_hash", "candidate_count", "truncated", "source_refs", "version_status"),
	"knowledge.read.observed":   fields("kn_id", "read_kind", "business_refs", "version_status", "schema_version"),
	"data.query.observed":       fields("query_hash", "query_type", "row_count", "resource_refs", "field_refs", "truncated", "version_status", "as_of"),
	"model.call.observed":       fields("model_name", "model_provider", "status", "input_token_count", "output_token_count", "prompt_hash", "output_hash", "error_category", "error_hash"),
	"tool.called":               fields("tool_id", "tool_name", "args_hash", "visibility", "version_status"),
	"tool.result.observed":      fields("tool_id", "tool_name", "status", "result_hash", "result_length", "result_count", "error_hash", "error_category", "visibility", "version_status"),
	"claim.created":             fields("claim_id", "claim_type", "claim_hash", "source_event_ids", "operation_ids", "version_status", "visibility"),
	"evidence.refs.created":     fields("claim_id", "evidence_refs"),
	"business.refs.resolved":    fields("claim_id", "resolver_status", "business_refs"),
	"action.recommended":        fields("action_instance_id", "action_type", "target_refs", "reason_hash", "status"),
	"action.approval_requested": fields("action_instance_id", "policy_ref", "status"),
	"action.approved":           fields("action_instance_id", "actor_ref", "policy_decision_ref", "status"),
	"action.rejected":           fields("action_instance_id", "actor_ref", "policy_decision_ref", "status"),
	"action.executed":           fields("action_instance_id", "invocation_ref", "tool_ref", "status", "error_category", "error_hash"),
	"action.result_recorded":    fields("action_instance_id", "result_hash", "artifact_ref", "task_ref", "status"),
}

var twoPointOneRefFields = fields("ref_id", "ref_type", "source_system", "validity", "version_status", "visibility", "summary_hash")

func fields(names ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

var visibilityStates = map[string]struct{}{
	"":             {},
	"visible":      {},
	"redacted":     {},
	"hidden":       {},
	"omitted":      {},
	"unresolved":   {},
	"unauthorized": {},
}

type Service struct {
	store ievidencestore.EvidenceStorePort
}

const (
	DefaultEvidenceQueryLimit = 1000
	MaxEvidenceQueryLimit     = 1000
)

func New(store ievidencestore.EvidenceStorePort) *Service {
	return &Service{store: store}
}

func (s *Service) Ingest(ctx context.Context, body []byte) (evidencevo.IngestResponse, evidencevo.ValidationErrors, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
			evidencevo.NewValidationError("BKN_TRACE_INVALID_JSON", "$", "request body must be valid json"),
		}, nil
	}

	validationErrors := evidencevo.ValidationErrors{}
	checkSensitive(raw, "$", &validationErrors)
	if len(validationErrors) > 0 {
		return evidencevo.IngestResponse{}, validationErrors, nil
	}

	var req evidencevo.IngestRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
			evidencevo.NewValidationError("BKN_TRACE_INVALID_JSON", "$", "request body must match evidence ingest schema"),
		}, nil
	}

	normalized := normalize(req, &validationErrors)
	if len(validationErrors) > 0 {
		return evidencevo.IngestResponse{}, validationErrors, nil
	}

	if req.SchemaVersion == evidencevo.ContractVersion {
		existing, err := s.store.GetEvidenceHistoryByTraceID(ctx, req.Trace.TraceID)
		if err != nil {
			return evidencevo.IngestResponse{}, nil, err
		}
		for _, committed := range existing {
			if !evidencevo.SameOwnership(committed, normalized) {
				return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
					evidencevo.NewValidationError("BKN_TRACE_OWNERSHIP_CONFLICT", "$.trace", "trace request or ownership differs from the committed trace"),
				}, nil
			}
		}
		validateTwoPointOneGraph(existing, &normalized, &validationErrors)
		if len(validationErrors) > 0 {
			return evidencevo.IngestResponse{}, validationErrors, nil
		}
	}

	if err := s.store.StoreEvidence(ctx, normalized); err != nil {
		switch {
		case errors.Is(err, ievidencestore.ErrEventIDConflict):
			return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
				evidencevo.NewValidationError("BKN_TRACE_EVENT_ID_CONFLICT", "$.events", "event_id already exists with different content"),
			}, nil
		case errors.Is(err, ievidencestore.ErrActionTransitionInvalid):
			return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
				evidencevo.NewValidationError("BKN_TRACE_ACTION_TRANSITION_INVALID", "$.events", "action state changed before this append committed"),
			}, nil
		case errors.Is(err, ievidencestore.ErrCausationInvalid):
			return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
				evidencevo.NewValidationError("BKN_TRACE_REQUIRED_FIELD_MISSING", "$.events", "causation_event_id must reference a previously committed event"),
			}, nil
		case errors.Is(err, ievidencestore.ErrOwnershipConflict):
			return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
				evidencevo.NewValidationError("BKN_TRACE_OWNERSHIP_CONFLICT", "$.trace", "trace request or ownership differs from the committed trace"),
			}, nil
		case errors.Is(err, ievidencestore.ErrTraceCapacityExceeded):
			return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
				evidencevo.NewValidationError("BKN_TRACE_CAPACITY_EXCEEDED", "$.events", "trace event or serialized byte limit exceeded"),
			}, nil
		}
		return evidencevo.IngestResponse{}, nil, err
	}

	return evidencevo.IngestResponse{
		TraceID:          normalized.TraceID,
		RequestID:        normalized.RequestID,
		SchemaVersion:    normalized.SchemaVersion,
		AcceptedEvents:   normalized.AcceptedEvents,
		ClaimCount:       normalized.ClaimCount,
		EvidenceRefCount: normalized.EvidenceRefCount,
		BusinessRefCount: normalized.BusinessRefCount,
	}, nil, nil
}

func (s *Service) GetEvidenceChainByTraceID(ctx context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceChainResponse, bool, error) {
	result, err := s.store.GetEvidenceByTraceID(ctx, strings.TrimSpace(traceID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.EvidenceChainResponse{}, false, err
	}
	if len(result.Traces) == 0 {
		return evidencevo.EvidenceChainResponse{}, false, nil
	}
	return buildEvidenceChain(result.Traces, result.Truncated), true, nil
}

func (s *Service) GetEvidenceChainByRequestID(ctx context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceChainResponse, bool, error) {
	result, err := s.store.GetEvidenceByRequestID(ctx, strings.TrimSpace(requestID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.EvidenceChainResponse{}, false, err
	}
	if len(result.Traces) == 0 {
		return evidencevo.EvidenceChainResponse{}, false, nil
	}
	return buildEvidenceChain(result.Traces, result.Truncated), true, nil
}

func (s *Service) GetBusinessGraphByTraceID(ctx context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.BusinessGraphResponse, bool, error) {
	result, err := s.store.GetEvidenceByTraceID(ctx, strings.TrimSpace(traceID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.BusinessGraphResponse{}, false, err
	}
	if len(result.Traces) == 0 {
		return evidencevo.BusinessGraphResponse{}, false, nil
	}
	return buildBusinessGraph(result.Traces, result.Truncated), true, nil
}

func (s *Service) GetBusinessGraphByRequestID(ctx context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.BusinessGraphResponse, bool, error) {
	result, err := s.store.GetEvidenceByRequestID(ctx, strings.TrimSpace(requestID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.BusinessGraphResponse{}, false, err
	}
	if len(result.Traces) == 0 {
		return evidencevo.BusinessGraphResponse{}, false, nil
	}
	return buildBusinessGraph(result.Traces, result.Truncated), true, nil
}

func (s *Service) GetEvidenceNodeByTraceID(ctx context.Context, traceID string, nodeID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceNodeResponse, bool, error) {
	result, err := s.store.GetEvidenceByTraceID(ctx, strings.TrimSpace(traceID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.EvidenceNodeResponse{}, false, err
	}
	return findEvidenceNode(result.Traces, strings.TrimSpace(nodeID))
}

func (s *Service) GetEvidenceNodeByRequestID(ctx context.Context, requestID string, nodeID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceNodeResponse, bool, error) {
	result, err := s.store.GetEvidenceByRequestID(ctx, strings.TrimSpace(requestID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.EvidenceNodeResponse{}, false, err
	}
	return findEvidenceNode(result.Traces, strings.TrimSpace(nodeID))
}

func (s *Service) GetSnapshotPreviewByTraceID(ctx context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.SnapshotPreviewResponse, bool, error) {
	result, err := s.store.GetEvidenceByTraceID(ctx, strings.TrimSpace(traceID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.SnapshotPreviewResponse{}, false, err
	}
	if len(result.Traces) == 0 {
		return evidencevo.SnapshotPreviewResponse{}, false, nil
	}
	return buildSnapshotPreview(result.Traces, result.Truncated), true, nil
}

func (s *Service) GetSnapshotPreviewByRequestID(ctx context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.SnapshotPreviewResponse, bool, error) {
	result, err := s.store.GetEvidenceByRequestID(ctx, strings.TrimSpace(requestID), normalizeQueryOptions(options))
	if err != nil {
		return evidencevo.SnapshotPreviewResponse{}, false, err
	}
	if len(result.Traces) == 0 {
		return evidencevo.SnapshotPreviewResponse{}, false, nil
	}
	return buildSnapshotPreview(result.Traces, result.Truncated), true, nil
}

func buildEvidenceChain(traces []evidencevo.NormalizedTrace, truncated bool) evidencevo.EvidenceChainResponse {
	response := evidencevo.EvidenceChainResponse{
		TraceID:   traces[0].TraceID,
		RequestID: traces[0].RequestID,
	}
	knownClaims := map[string]struct{}{}
	claimRefs := map[string]struct{}{}
	partialReasons := map[string]struct{}{}
	knownEventIDs := map[string]struct{}{}
	claimSourceEventIDs := []string{}

	causationIDs := []string{}
	for _, trace := range traces {
		for _, event := range trace.Events {
			if event.SchemaVersion == evidencevo.LegacyContractVersion || event.SchemaVersion == "" && trace.SchemaVersion == evidencevo.LegacyContractVersion {
				partialReasons["causality_missing"] = struct{}{}
			}
			if event.EventID != "" {
				knownEventIDs[event.EventID] = struct{}{}
			}
			if event.CausationID != "" {
				causationIDs = append(causationIDs, event.CausationID)
			}
			switch event.EventType {
			case "claim.created":
				for _, sourceID := range stringArrayField(event.Payload, "source_event_ids") {
					claimSourceEventIDs = append(claimSourceEventIDs, sourceID)
				}
				if visible(event.Payload) {
					response.Data.Claims = append(response.Data.Claims, cloneMap(event.Payload))
				} else {
					countVisibility(event.Payload, &response.VisibilitySummary)
				}
				if claimID, ok := stringField(event.Payload, "claim_id"); ok && claimID != "" {
					knownClaims[claimID] = struct{}{}
				}
			case "evidence.refs.created":
				claimID, _ := stringField(event.Payload, "claim_id")
				if claimID != "" {
					claimRefs[claimID] = struct{}{}
				}
				response.Data.EvidenceRefs = appendVisibleRefs(response.Data.EvidenceRefs, arrayField(event.Payload, "evidence_refs"), &response.VisibilitySummary)
			case "business.refs.resolved":
				if resolverStatus, _ := stringField(event.Payload, "resolver_status"); resolverStatus == "partial" || resolverStatus == "unresolved" {
					partialReasons["business_ref_unresolved"] = struct{}{}
				}
				claimID, _ := stringField(event.Payload, "claim_id")
				if claimID != "" {
					claimRefs[claimID] = struct{}{}
				}
				response.Data.BusinessRefs = appendVisibleRefs(response.Data.BusinessRefs, arrayField(event.Payload, "business_refs"), &response.VisibilitySummary)
			}
		}
	}
	for _, sourceID := range claimSourceEventIDs {
		if _, ok := knownEventIDs[sourceID]; !ok {
			partialReasons["source_event_missing"] = struct{}{}
		}
	}
	for _, causationID := range causationIDs {
		if _, ok := knownEventIDs[causationID]; !ok {
			partialReasons["causality_missing"] = struct{}{}
		}
	}

	if len(knownClaims) == 0 {
		partialReasons["missing_claim"] = struct{}{}
	}
	for claimID := range claimRefs {
		if _, ok := knownClaims[claimID]; !ok {
			partialReasons["missing_claim"] = struct{}{}
		}
	}
	for _, claim := range response.Data.Claims {
		if _, ok := claim["version_status"].(string); !ok {
			partialReasons["version_status_missing"] = struct{}{}
		}
	}
	if truncated {
		partialReasons["evidence_query_truncated"] = struct{}{}
	}
	if response.VisibilitySummary.UnauthorizedRefCount > 0 {
		partialReasons["evidence_ref_unauthorized"] = struct{}{}
	}
	if response.VisibilitySummary.UnresolvedRefCount > 0 {
		partialReasons["evidence_ref_unresolved"] = struct{}{}
	}

	response.PartialReasons = sortedKeys(partialReasons)
	response.Partial = len(response.PartialReasons) > 0
	response.Page.NodeCount = len(response.Data.Claims) + len(response.Data.EvidenceRefs) + len(response.Data.BusinessRefs)
	response.Page.EdgeCount = len(response.Data.EvidenceRefs) + len(response.Data.BusinessRefs)
	response.Page.Truncated = truncated
	return response
}

func stringArrayField(payload map[string]any, key string) []string {
	values := arrayField(payload, key)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func buildSnapshotPreview(traces []evidencevo.NormalizedTrace, truncated bool) evidencevo.SnapshotPreviewResponse {
	chain := buildEvidenceChain(traces, truncated)
	artifactSummary := map[string]any{
		"trace_id":           chain.TraceID,
		"bkn.request.id":     chain.RequestID,
		"claims":             chain.Data.Claims,
		"evidence_refs":      chain.Data.EvidenceRefs,
		"business_refs":      chain.Data.BusinessRefs,
		"visibility_summary": chain.VisibilitySummary,
		"partial":            chain.Partial,
		"partial_reason":     chain.PartialReasons,
	}
	artifactHash := hashValue(artifactSummary)
	manifest := evidencevo.SnapshotManifest{
		SchemaVersion:     "bkn-trace-snapshot-preview/v1",
		Producer:          "bkn-trace.agent-observability",
		TraceID:           chain.TraceID,
		RequestID:         chain.RequestID,
		ArtifactCount:     chain.Page.NodeCount,
		ClaimCount:        len(chain.Data.Claims),
		EvidenceRefCount:  len(chain.Data.EvidenceRefs),
		BusinessRefCount:  len(chain.Data.BusinessRefs),
		VisibilitySummary: chain.VisibilitySummary,
		ComplianceStatus:  "preview/non-production compliance",
		DLPClassification: "metadata-only",
		RetentionPolicy:   "policy-managed",
		LegalHold:         "not_requested",
		SignatureStatus:   "unsigned-preview",
		ArtifactHash:      artifactHash,
	}
	manifest.ManifestHash = hashValue(manifest)
	return evidencevo.SnapshotPreviewResponse{
		TraceID:           chain.TraceID,
		RequestID:         chain.RequestID,
		Partial:           chain.Partial,
		PartialReasons:    chain.PartialReasons,
		VisibilitySummary: chain.VisibilitySummary,
		SnapshotRef: evidencevo.SnapshotRef{
			SnapshotID: "preview:" + strings.TrimPrefix(hashValue(map[string]string{
				"trace_id":       chain.TraceID,
				"bkn.request.id": chain.RequestID,
				"artifact_hash":  artifactHash,
			}), "sha256:")[:16],
			Mode: "preview",
		},
		Manifest: manifest,
	}
}

func hashValue(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func findEvidenceNode(traces []evidencevo.NormalizedTrace, nodeID string) (evidencevo.EvidenceNodeResponse, bool, error) {
	if nodeID == "" {
		return evidencevo.EvidenceNodeResponse{}, false, nil
	}
	for _, trace := range traces {
		for _, event := range trace.Events {
			switch event.EventType {
			case "claim.created":
				if response, ok := claimNodeFromEvent(trace, event, nodeID); ok {
					return response, true, nil
				}
			case "evidence.refs.created":
				if response, ok := refNodeFromEvent(trace, event, nodeID, "evidence_ref", "evidence_refs"); ok {
					return response, true, nil
				}
			case "business.refs.resolved":
				if response, ok := refNodeFromEvent(trace, event, nodeID, "business_ref", "business_refs"); ok {
					return response, true, nil
				}
			}
		}
	}
	return evidencevo.EvidenceNodeResponse{}, false, nil
}

func claimNodeFromEvent(trace evidencevo.NormalizedTrace, event evidencevo.EvidenceEvent, nodeID string) (evidencevo.EvidenceNodeResponse, bool) {
	claimID, _ := stringField(event.Payload, "claim_id")
	if claimID == "" || nodeID != "claim:"+claimID || !visible(event.Payload) {
		return evidencevo.EvidenceNodeResponse{}, false
	}
	versionStatus, _ := stringField(event.Payload, "version_status")
	return evidencevo.EvidenceNodeResponse{
		TraceID:       trace.TraceID,
		RequestID:     trace.RequestID,
		NodeID:        nodeID,
		NodeType:      "claim",
		ClaimID:       claimID,
		Visibility:    visibilityValue(event.Payload),
		VersionStatus: versionStatus,
		Data:          cloneMap(event.Payload),
	}, true
}

func refNodeFromEvent(trace evidencevo.NormalizedTrace, event evidencevo.EvidenceEvent, nodeID string, nodeType string, payloadKey string) (evidencevo.EvidenceNodeResponse, bool) {
	claimID, _ := stringField(event.Payload, "claim_id")
	for _, item := range arrayField(event.Payload, payloadKey) {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		refID, _ := stringField(ref, "ref_id")
		if refID == "" || nodeID != nodeType+":"+refID || !visible(ref) {
			continue
		}
		versionStatus, _ := stringField(ref, "version_status")
		return evidencevo.EvidenceNodeResponse{
			TraceID:       trace.TraceID,
			RequestID:     trace.RequestID,
			NodeID:        nodeID,
			NodeType:      nodeType,
			ClaimID:       claimID,
			Visibility:    visibilityValue(ref),
			VersionStatus: versionStatus,
			Data:          cloneMap(ref),
		}, true
	}
	return evidencevo.EvidenceNodeResponse{}, false
}

func buildBusinessGraph(traces []evidencevo.NormalizedTrace, truncated bool) evidencevo.BusinessGraphResponse {
	response := evidencevo.BusinessGraphResponse{
		TraceID:   traces[0].TraceID,
		RequestID: traces[0].RequestID,
	}
	knownClaims := map[string]struct{}{}
	visibleClaims := map[string]struct{}{}
	claimNodes := map[string]struct{}{}
	businessNodes := map[string]struct{}{}
	businessRefs := map[string]struct{}{}
	edges := map[string]struct{}{}
	partialReasons := map[string]struct{}{}
	edgeIndex := 0
	businessRefEvents := 0
	expanded := hasInteractionEvent(traces)

	for _, trace := range traces {
		for _, event := range trace.Events {
			if event.EventType != "claim.created" {
				continue
			}
			claimID, _ := stringField(event.Payload, "claim_id")
			if claimID != "" {
				knownClaims[claimID] = struct{}{}
			}
			if claimID != "" && visible(event.Payload) {
				visibleClaims[claimID] = struct{}{}
				addClaimNode(&response, claimNodes, event.Payload, claimID)
			} else if !visible(event.Payload) {
				countVisibility(event.Payload, &response.VisibilitySummary)
				partialReasons["hidden_claim"] = struct{}{}
			}
		}
	}

	eventNodes := map[string]string{}
	operationNodes := map[string][]string{}
	if expanded {
		eventNodes, operationNodes = projectExpandedBusinessNodes(&response, traces, visibleClaims, claimNodes)
	}

	for _, trace := range traces {
		for _, event := range trace.Events {
			if event.EventType == "business.refs.resolved" {
				businessRefEvents++
				if resolverStatus, _ := stringField(event.Payload, "resolver_status"); resolverStatus == "partial" || resolverStatus == "unresolved" {
					partialReasons["business_ref_unresolved"] = struct{}{}
				}
				claimID, _ := stringField(event.Payload, "claim_id")
				if claimID == "" {
					partialReasons["missing_claim"] = struct{}{}
				} else if _, ok := knownClaims[claimID]; !ok {
					partialReasons["missing_claim"] = struct{}{}
				}
				if _, ok := visibleClaims[claimID]; !ok {
					countVisibleBusinessRefsAsOmitted(event.Payload, &response.VisibilitySummary)
					continue
				}
				ensureSyntheticClaimNode(&response, claimNodes, claimID)
				for _, item := range arrayField(event.Payload, "business_refs") {
					ref, ok := item.(map[string]any)
					if !ok {
						partialReasons["business_ref_invalid"] = struct{}{}
						continue
					}
					if !visible(ref) {
						countVisibility(ref, &response.VisibilitySummary)
						continue
					}
					refID, _ := stringField(ref, "ref_id")
					if refID == "" {
						partialReasons["business_ref_id_missing"] = struct{}{}
						continue
					}
					if _, ok := businessRefs[refID]; !ok {
						businessRefs[refID] = struct{}{}
						response.VisibilitySummary.AuthorizedRefCount++
					}
					partialReasons["resolver_unresolved"] = struct{}{}
					addBusinessNode(&response, businessNodes, refID, claimID, ref)
					edgeType := businessEdgeType(ref)
					if expanded {
						edgeType = "uses_business_ref"
					}
					if claimID != "" && !edgeSeen(edges, "claim:"+claimID, "business:"+refID, edgeType) {
						edgeIndex++
						response.Data.Edges = append(response.Data.Edges, evidencevo.BusinessGraphEdge{
							ID:         "edge:" + strconv.Itoa(edgeIndex),
							SourceID:   "claim:" + claimID,
							TargetID:   "business:" + refID,
							EdgeType:   edgeType,
							Visibility: visibilityValue(ref),
						})
					}
				}
			}
		}
	}
	if expanded {
		projectExpandedBusinessEdges(&response, traces, eventNodes, operationNodes, visibleClaims, edges, &edgeIndex)
	}

	if len(knownClaims) == 0 {
		partialReasons["missing_claim"] = struct{}{}
	}
	if businessRefEvents == 0 {
		partialReasons["missing_business_refs"] = struct{}{}
	}
	if response.VisibilitySummary.UnresolvedRefCount > 0 {
		partialReasons["business_ref_unresolved"] = struct{}{}
	}
	if response.VisibilitySummary.UnauthorizedRefCount > 0 {
		partialReasons["business_ref_unauthorized"] = struct{}{}
	}
	if len(response.Data.Nodes) == 0 {
		partialReasons["empty_business_graph"] = struct{}{}
	}
	if truncated {
		partialReasons["evidence_query_truncated"] = struct{}{}
	}

	response.PartialReasons = sortedKeys(partialReasons)
	response.Partial = len(response.PartialReasons) > 0
	response.Page.NodeCount = len(response.Data.Nodes)
	response.Page.EdgeCount = len(response.Data.Edges)
	response.Page.Truncated = truncated
	return response
}

func hasInteractionEvent(traces []evidencevo.NormalizedTrace) bool {
	for _, trace := range traces {
		for _, event := range trace.Events {
			if event.EventType == "agent.interaction.started" && event.InteractionID != "" && event.EventID != "" {
				return true
			}
		}
	}
	return false
}

func projectExpandedBusinessNodes(response *evidencevo.BusinessGraphResponse, traces []evidencevo.NormalizedTrace, visibleClaims map[string]struct{}, claimNodes map[string]struct{}) (map[string]string, map[string][]string) {
	eventNodes := map[string]string{}
	operationNodes := map[string][]string{}
	seen := map[string]struct{}{}
	for _, node := range response.Data.Nodes {
		seen[node.ID] = struct{}{}
	}
	for _, trace := range traces {
		for _, event := range trace.Events {
			switch {
			case event.EventType == "agent.interaction.started":
				if event.InteractionID == "" || event.EventID == "" {
					continue
				}
				nodeID := "interaction:" + event.InteractionID
				addGraphNode(response, seen, evidencevo.BusinessGraphNode{
					ID: nodeID, NodeType: "interaction", Stage: "intent", Label: "interaction",
					EventID: event.EventID, InteractionID: event.InteractionID, Visibility: "visible", Properties: cloneMap(event.Payload),
				})
				eventNodes[event.EventID] = nodeID
			case isExecutionFact(event.EventType):
				if event.EventID == "" || !visible(event.Payload) {
					continue
				}
				nodeID := "event:" + event.EventID
				properties := cloneMap(event.Payload)
				properties["event_type"] = event.EventType
				properties["producer_module"] = event.Producer
				properties["operation_name"] = event.OperationName
				addGraphNode(response, seen, evidencevo.BusinessGraphNode{
					ID: nodeID, NodeType: "operation", Stage: "execution", Label: event.EventType,
					EventID: event.EventID, InteractionID: event.InteractionID, OperationID: event.OperationID,
					ClaimID: event.ClaimID, Visibility: visibilityValue(event.Payload), Properties: properties,
				})
				eventNodes[event.EventID] = nodeID
				if event.OperationID != "" {
					operationNodes[event.OperationID] = append(operationNodes[event.OperationID], nodeID)
				}
			case event.EventType == "claim.created":
				claimID, _ := stringField(event.Payload, "claim_id")
				if _, ok := visibleClaims[claimID]; ok && event.EventID != "" {
					eventNodes[event.EventID] = "claim:" + claimID
				}
			case event.EventType == "evidence.refs.created":
				claimID, _ := stringField(event.Payload, "claim_id")
				if _, ok := visibleClaims[claimID]; !ok {
					continue
				}
				for _, item := range arrayField(event.Payload, "evidence_refs") {
					ref, ok := item.(map[string]any)
					if !ok || !visible(ref) {
						if ok {
							countVisibility(ref, &response.VisibilitySummary)
						}
						continue
					}
					refID, _ := stringField(ref, "ref_id")
					if refID == "" {
						continue
					}
					nodeID := "evidence:" + refID
					if _, exists := seen[nodeID]; !exists {
						response.VisibilitySummary.AuthorizedRefCount++
					}
					addGraphNode(response, seen, evidencevo.BusinessGraphNode{
						ID: nodeID, NodeType: "evidence_ref", Stage: "evidence", Label: refID, ClaimID: claimID,
						VersionStatus: stringValue(ref, "version_status"), Visibility: visibilityValue(ref), Properties: cloneMap(ref),
					})
				}
			case strings.HasPrefix(event.EventType, "action."):
				if _, ok := visibleClaims[event.ClaimID]; !ok || event.EventID == "" {
					continue
				}
				actionID, _ := stringField(event.Payload, "action_instance_id")
				if actionID == "" {
					continue
				}
				state := strings.TrimPrefix(event.EventType, "action.")
				nodeID := "action:" + actionID + ":" + state
				addGraphNode(response, seen, evidencevo.BusinessGraphNode{
					ID: nodeID, NodeType: "action", Stage: "action", Label: state, EventID: event.EventID,
					InteractionID: event.InteractionID, OperationID: event.OperationID, ClaimID: event.ClaimID,
					ActionID: actionID, Visibility: "visible", Properties: cloneMap(event.Payload),
				})
				eventNodes[event.EventID] = nodeID
			}
		}
	}
	for index := range response.Data.Nodes {
		if response.Data.Nodes[index].NodeType == "claim" {
			response.Data.Nodes[index].Stage = "claim"
		}
		if strings.HasPrefix(response.Data.Nodes[index].ID, "business:") {
			response.Data.Nodes[index].Stage = "evidence"
		}
	}
	return eventNodes, operationNodes
}

func projectExpandedBusinessEdges(response *evidencevo.BusinessGraphResponse, traces []evidencevo.NormalizedTrace, eventNodes map[string]string, operationNodes map[string][]string, visibleClaims map[string]struct{}, seen map[string]struct{}, edgeIndex *int) {
	for _, trace := range traces {
		for _, event := range trace.Events {
			currentNode := eventNodes[event.EventID]
			if currentNode != "" && event.CausationID != "" {
				if causeNode := eventNodes[event.CausationID]; causeNode != "" {
					appendGraphEdge(response, seen, edgeIndex, currentNode, causeNode, "caused_by", "visible")
				}
			}
			switch event.EventType {
			case "claim.created":
				claimID, _ := stringField(event.Payload, "claim_id")
				if _, ok := visibleClaims[claimID]; !ok {
					continue
				}
				for _, sourceID := range stringArrayField(event.Payload, "source_event_ids") {
					if sourceNode := eventNodes[sourceID]; sourceNode != "" {
						appendGraphEdge(response, seen, edgeIndex, sourceNode, "claim:"+claimID, "supports", "visible")
					}
				}
			case "evidence.refs.created":
				claimID, _ := stringField(event.Payload, "claim_id")
				if _, ok := visibleClaims[claimID]; !ok {
					continue
				}
				for _, item := range arrayField(event.Payload, "evidence_refs") {
					ref, ok := item.(map[string]any)
					if !ok || !visible(ref) {
						continue
					}
					refID, _ := stringField(ref, "ref_id")
					if refID == "" {
						continue
					}
					evidenceNode := "evidence:" + refID
					appendGraphEdge(response, seen, edgeIndex, evidenceNode, "claim:"+claimID, "supports", visibilityValue(ref))
					for _, operationNode := range operationNodes[event.OperationID] {
						appendGraphEdge(response, seen, edgeIndex, operationNode, evidenceNode, "observed", visibilityValue(ref))
					}
				}
			case "action.recommended":
				if _, ok := visibleClaims[event.ClaimID]; ok && currentNode != "" {
					appendGraphEdge(response, seen, edgeIndex, "claim:"+event.ClaimID, currentNode, "recommends", "visible")
				}
			}
			if strings.HasPrefix(event.EventType, "action.") && event.EventType != "action.recommended" && currentNode != "" {
				if previousNode := eventNodes[event.CausationID]; previousNode != "" {
					appendGraphEdge(response, seen, edgeIndex, previousNode, currentNode, "transitions_to", "visible")
				}
			}
		}
	}
}

func isExecutionFact(eventType string) bool {
	switch eventType {
	case "retrieval.completed", "knowledge.read.observed", "data.query.observed", "model.call.observed", "tool.called", "tool.result.observed":
		return true
	default:
		return false
	}
}

func addGraphNode(response *evidencevo.BusinessGraphResponse, seen map[string]struct{}, node evidencevo.BusinessGraphNode) {
	if _, ok := seen[node.ID]; ok {
		return
	}
	seen[node.ID] = struct{}{}
	response.Data.Nodes = append(response.Data.Nodes, node)
}

func appendGraphEdge(response *evidencevo.BusinessGraphResponse, seen map[string]struct{}, edgeIndex *int, sourceID, targetID, edgeType, visibility string) {
	if sourceID == "" || targetID == "" || edgeSeen(seen, sourceID, targetID, edgeType) {
		return
	}
	*edgeIndex++
	response.Data.Edges = append(response.Data.Edges, evidencevo.BusinessGraphEdge{
		ID: "edge:" + strconv.Itoa(*edgeIndex), SourceID: sourceID, TargetID: targetID, EdgeType: edgeType, Visibility: visibility,
	})
}

func stringValue(value map[string]any, key string) string {
	result, _ := stringField(value, key)
	return result
}

func normalizeQueryOptions(options evidencevo.EvidenceQueryOptions) evidencevo.EvidenceQueryOptions {
	if options.Limit <= 0 {
		options.Limit = DefaultEvidenceQueryLimit
	}
	if options.Limit > MaxEvidenceQueryLimit {
		options.Limit = MaxEvidenceQueryLimit
	}
	return options
}

func appendVisibleRefs(target []map[string]any, refs []any, summary *evidencevo.VisibilitySummary) []map[string]any {
	for _, item := range refs {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if visible(ref) {
			target = append(target, cloneMap(ref))
			summary.AuthorizedRefCount++
			continue
		}
		countVisibility(ref, summary)
	}
	return target
}

func visible(item map[string]any) bool {
	visibility, _ := item["visibility"].(string)
	return visibility == "" || visibility == "visible"
}

func countVisibility(item map[string]any, summary *evidencevo.VisibilitySummary) {
	visibility, _ := item["visibility"].(string)
	switch visibility {
	case "redacted":
		summary.RedactedRefCount++
	case "hidden":
		summary.HiddenRefCount++
	case "omitted":
		summary.OmittedRefCount++
	case "unresolved":
		summary.UnresolvedRefCount++
	case "unauthorized":
		summary.UnauthorizedRefCount++
	default:
		summary.OmittedRefCount++
	}
}

func countVisibleBusinessRefsAsOmitted(payload map[string]any, summary *evidencevo.VisibilitySummary) {
	for _, item := range arrayField(payload, "business_refs") {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if visible(ref) {
			summary.OmittedRefCount++
			continue
		}
		countVisibility(ref, summary)
	}
}

func cloneMap(value map[string]any) map[string]any {
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func addClaimNode(response *evidencevo.BusinessGraphResponse, seen map[string]struct{}, payload map[string]any, claimID string) {
	if _, ok := seen[claimID]; ok {
		return
	}
	seen[claimID] = struct{}{}
	label, _ := stringField(payload, "claim_type")
	versionStatus, _ := stringField(payload, "version_status")
	response.Data.Nodes = append(response.Data.Nodes, evidencevo.BusinessGraphNode{
		ID:            "claim:" + claimID,
		NodeType:      "claim",
		Label:         label,
		ClaimID:       claimID,
		VersionStatus: versionStatus,
		Visibility:    visibilityValue(payload),
		Properties:    cloneMap(payload),
	})
}

func ensureSyntheticClaimNode(response *evidencevo.BusinessGraphResponse, seen map[string]struct{}, claimID string) {
	if _, ok := seen[claimID]; ok {
		return
	}
	seen[claimID] = struct{}{}
	response.Data.Nodes = append(response.Data.Nodes, evidencevo.BusinessGraphNode{
		ID:       "claim:" + claimID,
		NodeType: "claim",
		ClaimID:  claimID,
	})
}

func addBusinessNode(response *evidencevo.BusinessGraphResponse, seen map[string]struct{}, refID string, claimID string, ref map[string]any) {
	if _, ok := seen[refID]; ok {
		return
	}
	seen[refID] = struct{}{}
	nodeType, _ := stringField(ref, "ref_type")
	if nodeType == "" {
		nodeType = "business_ref"
	}
	versionStatus, _ := stringField(ref, "version_status")
	response.Data.Nodes = append(response.Data.Nodes, evidencevo.BusinessGraphNode{
		ID:            "business:" + refID,
		NodeType:      nodeType,
		ClaimID:       claimID,
		VersionStatus: versionStatus,
		Visibility:    visibilityValue(ref),
		Properties:    registeredRefProperties(ref),
	})
}

func registeredRefProperties(ref map[string]any) map[string]any {
	properties := make(map[string]any, len(twoPointOneRefFields))
	for key := range twoPointOneRefFields {
		if value, ok := ref[key]; ok {
			properties[key] = value
		}
	}
	return properties
}

func businessEdgeType(ref map[string]any) string {
	refType, _ := stringField(ref, "ref_type")
	if refType == "" {
		return "claim_to_business_ref"
	}
	return "claim_to_" + refType
}

func edgeSeen(edges map[string]struct{}, sourceID, targetID, edgeType string) bool {
	key := sourceID + "|" + targetID + "|" + edgeType
	if _, ok := edges[key]; ok {
		return true
	}
	edges[key] = struct{}{}
	return false
}

func visibilityValue(item map[string]any) string {
	visibility, _ := item["visibility"].(string)
	if visibility == "" {
		return "visible"
	}
	return visibility
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type actionState struct {
	state       string
	claimID     string
	operationID string
	lastEventID string
}

func validateTwoPointOneGraph(existing []evidencevo.NormalizedTrace, incoming *evidencevo.NormalizedTrace, validationErrors *evidencevo.ValidationErrors) {
	storedHashes := map[string]string{}
	knownClaims := map[string]struct{}{}
	actions := map[string]actionState{}
	priorEventIDs := map[string]struct{}{}

	for _, trace := range existing {
		for _, event := range trace.Events {
			priorEventIDs[event.EventID] = struct{}{}
			hash, err := event.ContentHash()
			if err == nil {
				storedHashes[event.EventID] = hash
			}
			if event.EventType == "claim.created" {
				knownClaims[event.ClaimID] = struct{}{}
				if claimID, ok := stringField(event.Payload, "claim_id"); ok {
					knownClaims[claimID] = struct{}{}
				}
			}
			if strings.HasPrefix(event.EventType, "action.") {
				advanceStoredAction(actions, event)
			}
		}
	}
	seenIncoming := map[string]string{}
	for i, event := range incoming.Events {
		base := path("events", i)
		hash, err := event.ContentHash()
		if err != nil {
			add(validationErrors, "BKN_TRACE_EVENT_ID_CONFLICT", base+".event_id", "event content cannot be canonicalized")
			continue
		}
		if storedHash, ok := storedHashes[event.EventID]; ok {
			if storedHash != hash {
				add(validationErrors, "BKN_TRACE_EVENT_ID_CONFLICT", base+".event_id", "event_id already exists with different content")
			}
			continue
		}
		if previousHash, ok := seenIncoming[event.EventID]; ok {
			if previousHash != hash {
				add(validationErrors, "BKN_TRACE_EVENT_ID_CONFLICT", base+".event_id", "event_id is duplicated with different content")
			}
			continue
		}
		seenIncoming[event.EventID] = hash

		if event.CausationID == event.EventID && event.CausationID != "" {
			add(validationErrors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".causation_event_id", "event cannot cause itself")
		}
		if eventNeedsClaim(event.EventType) && event.EventType != "claim.created" {
			if _, ok := knownClaims[event.ClaimID]; event.ClaimID != "" && !ok {
				add(validationErrors, "BKN_TRACE_UNKNOWN_CLAIM_ID", base+".claim_id", "event must reference a known claim_id in the same trace")
			}
		}
		if strings.HasPrefix(event.EventType, "action.") {
			validateActionTransition(actions, event, base, validationErrors)
		}
		priorEventIDs[event.EventID] = struct{}{}
		if event.EventType == "claim.created" {
			knownClaims[event.ClaimID] = struct{}{}
		}
	}
	validateCausationCycles(existing, incoming.Events, validationErrors)
}

func validateCausationCycles(existing []evidencevo.NormalizedTrace, incoming []evidencevo.EvidenceEvent, validationErrors *evidencevo.ValidationErrors) {
	causes := map[string]string{}
	for _, trace := range existing {
		for _, event := range trace.Events {
			causes[event.EventID] = event.CausationID
		}
	}
	for _, event := range incoming {
		causes[event.EventID] = event.CausationID
	}
	for _, event := range incoming {
		seen := map[string]struct{}{}
		for current := event.EventID; current != ""; current = causes[current] {
			if _, ok := seen[current]; ok {
				add(validationErrors, "BKN_TRACE_REQUIRED_FIELD_MISSING", "$.events.causation_event_id", "causation_event_id must not form a cycle")
				break
			}
			seen[current] = struct{}{}
			if _, known := causes[current]; !known {
				break
			}
		}
	}
}

func advanceStoredAction(actions map[string]actionState, event evidencevo.EvidenceEvent) {
	actionID, _ := stringField(event.Payload, "action_instance_id")
	if actionID == "" {
		return
	}
	actions[actionID] = actionState{
		state:       actionStateForEvent(event.EventType),
		claimID:     event.ClaimID,
		operationID: event.OperationID,
		lastEventID: event.EventID,
	}
}

func validateActionTransition(actions map[string]actionState, event evidencevo.EvidenceEvent, base string, validationErrors *evidencevo.ValidationErrors) {
	actionID, _ := stringField(event.Payload, "action_instance_id")
	if actionID == "" {
		return
	}
	previous, exists := actions[actionID]
	expectedPrevious := map[string]string{
		"action.approval_requested": "recommended",
		"action.approved":           "approval_requested",
		"action.rejected":           "approval_requested",
		"action.executed":           "approved",
		"action.result_recorded":    "executed",
	}[event.EventType]
	valid := true
	if event.EventType == "action.recommended" {
		valid = !exists
	} else {
		valid = exists && previous.state == expectedPrevious && event.CausationID == previous.lastEventID
	}
	if exists && (previous.claimID != event.ClaimID || previous.operationID != event.OperationID) {
		valid = false
	}
	if !valid {
		add(validationErrors, "BKN_TRACE_ACTION_TRANSITION_INVALID", base+".event_type", "invalid action lifecycle transition or action identity drift")
		return
	}
	actions[actionID] = actionState{
		state:       actionStateForEvent(event.EventType),
		claimID:     event.ClaimID,
		operationID: event.OperationID,
		lastEventID: event.EventID,
	}
}

func actionStateForEvent(eventType string) string {
	return strings.TrimPrefix(eventType, "action.")
}

func normalize(req evidencevo.IngestRequest, errors *evidencevo.ValidationErrors) evidencevo.NormalizedTrace {
	if !evidencevo.SupportedContractVersion(req.SchemaVersion) {
		add(errors, "BKN_TRACE_SCHEMA_VERSION_UNSUPPORTED", "$.bkn.trace.schema.version", "unsupported evidence contract version")
	}

	checkTrace(req.Trace, errors)
	if len(req.Events) == 0 {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", "$.events", "phase-two evidence ingest requires events")
	}

	knownClaims := map[string]struct{}{}
	for i, event := range req.Events {
		if event.EventType == "claim.created" {
			claimID, _ := stringField(event.Payload, "claim_id")
			if claimID != "" {
				knownClaims[claimID] = struct{}{}
			}
		}
		if event.TraceID != req.Trace.TraceID || event.RequestID != req.Trace.RequestID {
			add(errors, "BKN_TRACE_JOIN_FAILED", path("events", i), "event cannot join trace/request")
		}
	}

	normalized := evidencevo.NormalizedTrace{
		TraceID:        req.Trace.TraceID,
		RequestID:      req.Trace.RequestID,
		TenantID:       req.Trace.TenantID,
		BusinessDomain: req.Trace.BusinessDomain,
		AccountID:      req.Trace.AccountID,
		AccountType:    req.Trace.AccountType,
		SchemaVersion:  req.SchemaVersion,
		Events:         req.Events,
		AcceptedEvents: len(req.Events),
	}
	for i := range normalized.Events {
		checkEvent(&normalized.Events[i], i, req.SchemaVersion, knownClaims, &normalized, errors)
	}
	return normalized
}

func checkTrace(trace evidencevo.TraceContext, errors *evidencevo.ValidationErrors) {
	required(trace.TraceID, "$.trace.trace_id", errors)
	required(trace.Traceparent, "$.trace.traceparent", errors)
	required(trace.RequestID, "$.trace.bkn.request.id", errors)
	required(trace.AccountID, "$.trace.bkn.account.id", errors)
	required(trace.AccountType, "$.trace.bkn.account.type", errors)
	if trace.TenantID == "" && trace.BusinessDomain == "" {
		add(errors, "BKN_TRACE_PERMISSION_CONTEXT_MISSING", "$.trace", "phase-two ingest requires bkn.tenant.id or business_domain")
	}
	if trace.TraceID != "" && !traceIDRE.MatchString(trace.TraceID) {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", "$.trace.trace_id", "missing valid trace id")
	}
	if trace.RequestID != "" && !requestIDRE.MatchString(trace.RequestID) {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", "$.trace.bkn.request.id", "missing valid bkn.request.id")
	}
	if trace.Traceparent != "" && !validTraceparent(trace.Traceparent) {
		add(errors, "BKN_TRACE_INVALID_TRACEPARENT", "$.trace.traceparent", "invalid traceparent")
	} else if match := traceparentRE.FindStringSubmatch(trace.Traceparent); len(match) == 3 && trace.TraceID != "" && match[1] != trace.TraceID {
		add(errors, "BKN_TRACE_JOIN_FAILED", "$.trace.traceparent", "traceparent trace id must match trace.trace_id")
	}
}

func checkEvent(event *evidencevo.EvidenceEvent, i int, requestVersion string, knownClaims map[string]struct{}, normalized *evidencevo.NormalizedTrace, errors *evidencevo.ValidationErrors) {
	base := path("events", i)
	required(event.EventID, base+".event_id", errors)
	required(event.EventType, base+".event_type", errors)
	required(event.SchemaVersion, base+".bkn.trace.schema.version", errors)
	required(event.ObservedAt, base+".observed_at", errors)
	required(event.EmittedAt, base+".emitted_at", errors)
	required(event.Producer, base+".producer_module", errors)
	required(event.TraceID, base+".trace_id", errors)
	required(event.SpanID, base+".span_id", errors)
	required(event.RequestID, base+".bkn.request.id", errors)
	required(event.OperationName, base+".bkn.operation.name", errors)
	if event.SchemaVersion != "" && (!evidencevo.SupportedContractVersion(event.SchemaVersion) || event.SchemaVersion != requestVersion) {
		add(errors, "BKN_TRACE_SCHEMA_VERSION_UNSUPPORTED", base+".bkn.trace.schema.version", "event contract version must match the ingest envelope")
	}
	if event.ObservedAt != "" && !validTimestamp(event.ObservedAt) {
		add(errors, "BKN_TRACE_INVALID_TIMESTAMP", base+".observed_at", "timestamp must be UTC RFC3339Nano")
	}
	if event.EmittedAt != "" && !validTimestamp(event.EmittedAt) {
		add(errors, "BKN_TRACE_INVALID_TIMESTAMP", base+".emitted_at", "timestamp must be UTC RFC3339Nano")
	}
	registeredTypes := legacyEventTypes
	unsupportedCode := "BKN_TRACE_EVENT_TYPE_UNREGISTERED"
	if requestVersion == evidencevo.ContractVersion {
		registeredTypes = twoPointOneEventTypes
		unsupportedCode = "BKN_TRACE_EVENT_TYPE_UNSUPPORTED"
	}
	if _, ok := registeredTypes[event.EventType]; event.EventType != "" && !ok {
		add(errors, unsupportedCode, base+".event_type", "event type is not registered for this contract version")
	}
	if event.Payload == nil {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".payload", "payload must be an object")
		return
	}
	if event.SpanID != "" && (!spanIDRE.MatchString(event.SpanID) || event.SpanID == strings.Repeat("0", 16)) {
		add(errors, "BKN_TRACE_JOIN_FAILED", base+".span_id", "span_id must be 16 lowercase hex and non-zero")
	}
	if requestVersion == evidencevo.ContractVersion {
		checkTwoPointOneEnvelope(event, base, errors)
		checkTwoPointOnePayload(*event, base+".payload", errors)
	}

	switch event.EventType {
	case "claim.created":
		checkClaim(event.Payload, base+".payload", normalized, errors)
	case "evidence.refs.created":
		checkRefs(event.Payload, base+".payload", "evidence_refs", knownClaims, requestVersion, errors)
		normalized.EvidenceRefCount += len(arrayField(event.Payload, "evidence_refs"))
	case "business.refs.resolved":
		checkRefs(event.Payload, base+".payload", "business_refs", knownClaims, requestVersion, errors)
		normalized.BusinessRefCount += len(arrayField(event.Payload, "business_refs"))
	case "tool.called":
		checkToolCalled(event.Payload, base+".payload", requestVersion, errors)
	case "tool.result.observed":
		checkToolResultObserved(event.Payload, base+".payload", requestVersion, errors)
	}
}

func checkTwoPointOneEnvelope(event *evidencevo.EvidenceEvent, base string, errors *evidencevo.ValidationErrors) {
	required(event.InteractionID, base+".interaction_id", errors)
	if event.Attempt == 0 {
		event.Attempt = 1
	}
	if event.Attempt < 1 {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".attempt", "attempt must be greater than zero")
	}
	if event.EventType != "agent.interaction.started" && event.EventType != "claim.created" {
		required(event.OperationID, base+".operation_id", errors)
	}
	if event.EventType != "agent.interaction.started" {
		required(event.CausationID, base+".causation_event_id", errors)
	}
	if eventNeedsClaim(event.EventType) {
		required(event.ClaimID, base+".claim_id", errors)
		if payloadClaimID, ok := stringField(event.Payload, "claim_id"); ok && payloadClaimID != "" && event.ClaimID != "" && payloadClaimID != event.ClaimID {
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".claim_id", "claim_id must match payload.claim_id")
		}
	}
}

func eventNeedsClaim(eventType string) bool {
	return eventType == "claim.created" || eventType == "evidence.refs.created" || eventType == "business.refs.resolved" || strings.HasPrefix(eventType, "action.")
}

func checkTwoPointOnePayload(event evidencevo.EvidenceEvent, base string, errors *evidencevo.ValidationErrors) {
	payload := event.Payload
	checkHashFields(payload, base, errors)
	allowed, registered := twoPointOnePayloadFields[event.EventType]
	if registered {
		for key := range payload {
			if _, ok := allowed[key]; !ok {
				add(errors, "BKN_TRACE_PAYLOAD_FIELD_UNSUPPORTED", base+"."+key, "payload field is not registered for this event type")
			}
		}
	}
	switch event.EventType {
	case "agent.interaction.started":
		requiredStringField(payload, "intent_hash", base, errors)
		requiredStringField(payload, "mode", base, errors)
		requiredOneStringField(payload, []string{"agent_id", "app_ref"}, base, errors)
	case "retrieval.completed":
		requiredStringField(payload, "query_hash", base, errors)
		requiredNonNegativeIntField(payload, "candidate_count", base, errors)
		requiredBoolField(payload, "truncated", base, errors)
		requiredFactRefArray(payload, "source_refs", base, errors)
		requiredVersionStatus(payload, base, errors)
	case "knowledge.read.observed":
		requiredStringField(payload, "kn_id", base, errors)
		requiredStringField(payload, "read_kind", base, errors)
		requiredVersionStatus(payload, base, errors)
		requiredFactRefArray(payload, "business_refs", base, errors)
	case "data.query.observed":
		requiredStringField(payload, "query_hash", base, errors)
		requiredStringField(payload, "query_type", base, errors)
		requiredNonNegativeIntField(payload, "row_count", base, errors)
		requiredBoolField(payload, "truncated", base, errors)
		requiredVersionStatus(payload, base, errors)
		requiredOneFactRefArray(payload, []string{"resource_refs", "field_refs"}, base, errors)
	case "model.call.observed":
		for _, field := range []string{"model_name", "model_provider", "status", "prompt_hash", "output_hash"} {
			requiredStringField(payload, field, base, errors)
		}
		for _, field := range []string{"input_token_count", "output_token_count"} {
			requiredNonNegativeIntField(payload, field, base, errors)
		}
		status, _ := stringField(payload, "status")
		requireEnum(status, []string{"ok", "success", "error"}, base+".status", errors)
		if status == "error" {
			requiredStringField(payload, "error_category", base, errors)
			requiredStringField(payload, "error_hash", base, errors)
		}
	case "claim.created":
		requiredStringArrayField(payload, "source_event_ids", base, errors)
		requiredStringArrayField(payload, "operation_ids", base, errors)
	case "evidence.refs.created":
		checkTwoPointOneRefs(payload, "evidence_refs", base, errors)
	case "business.refs.resolved":
		requiredStringField(payload, "resolver_status", base, errors)
		if status, _ := stringField(payload, "resolver_status"); status != "resolved" && status != "partial" && status != "unresolved" {
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".resolver_status", "resolver_status must be resolved, partial, or unresolved")
		}
		checkTwoPointOneRefs(payload, "business_refs", base, errors)
	case "action.recommended":
		checkActionBase(payload, base, errors)
		checkActionStatus(payload, "recommended", base, errors)
		requiredStringField(payload, "action_type", base, errors)
		requiredStringArrayField(payload, "target_refs", base, errors)
		requiredStringField(payload, "reason_hash", base, errors)
	case "action.approval_requested":
		checkActionBase(payload, base, errors)
		checkActionStatus(payload, "approval_requested", base, errors)
		requiredStringField(payload, "policy_ref", base, errors)
	case "action.approved":
		checkActionBase(payload, base, errors)
		checkActionStatus(payload, "approved", base, errors)
		requiredStringField(payload, "actor_ref", base, errors)
		requiredStringField(payload, "policy_decision_ref", base, errors)
	case "action.rejected":
		checkActionBase(payload, base, errors)
		checkActionStatus(payload, "rejected", base, errors)
		requiredStringField(payload, "actor_ref", base, errors)
		requiredStringField(payload, "policy_decision_ref", base, errors)
	case "action.executed":
		checkActionBase(payload, base, errors)
		requiredOneStringField(payload, []string{"invocation_ref", "tool_ref"}, base, errors)
		if status, _ := stringField(payload, "status"); status == "error" {
			requiredStringField(payload, "error_category", base, errors)
			requiredStringField(payload, "error_hash", base, errors)
		} else if status != "ok" && status != "success" {
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".status", "action.executed status must be ok, success, or error")
		}
	case "action.result_recorded":
		checkActionBase(payload, base, errors)
		requiredStringField(payload, "result_hash", base, errors)
		requiredOneStringField(payload, []string{"artifact_ref", "task_ref"}, base, errors)
		status, _ := stringField(payload, "status")
		requireEnum(status, []string{"recorded", "created", "updated", "deleted", "success", "error"}, base+".status", errors)
	}
}

func checkHashFields(value any, path string, errors *evidencevo.ValidationErrors) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := path + "." + key
			if strings.HasSuffix(key, "_hash") {
				text, ok := child.(string)
				if !ok || !hashRE.MatchString(text) {
					add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", childPath, "hash must use sha256:<64 lowercase hex> format")
				}
			}
			checkHashFields(child, childPath, errors)
		}
	case []any:
		for i, child := range typed {
			checkHashFields(child, path+"["+strconv.Itoa(i)+"]", errors)
		}
	}
}

func checkActionStatus(payload map[string]any, expected string, base string, errors *evidencevo.ValidationErrors) {
	if status, ok := stringField(payload, "status"); ok && status != "" && status != expected {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".status", "action status must match event type")
	}
}

func checkActionBase(payload map[string]any, base string, errors *evidencevo.ValidationErrors) {
	requiredStringField(payload, "action_instance_id", base, errors)
	requiredStringField(payload, "status", base, errors)
}

func checkTwoPointOneRefs(payload map[string]any, key, base string, errors *evidencevo.ValidationErrors) {
	refs := arrayField(payload, key)
	if len(refs) == 0 {
		if key == "business_refs" {
			if status, _ := stringField(payload, "resolver_status"); status == "unresolved" {
				return
			}
		}
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a non-empty reference array")
		return
	}
	for i, item := range refs {
		ref, ok := item.(map[string]any)
		if !ok {
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key+"["+strconv.Itoa(i)+"]", "reference must be an object")
			continue
		}
		checkTwoPointOneRef(ref, base+"."+key+"["+strconv.Itoa(i)+"]", errors)
	}
}

func checkTwoPointOneRef(ref map[string]any, refBase string, errors *evidencevo.ValidationErrors) {
	validValidity := fields("observed", "available", "unavailable", "expired", "partial")
	validVersionStatus := fields("versioned", "unversioned", "not_auditable")
	for field := range ref {
		if _, ok := twoPointOneRefFields[field]; !ok {
			add(errors, "BKN_TRACE_PAYLOAD_FIELD_UNSUPPORTED", refBase+"."+field, "reference field is not registered")
		}
	}
	for _, field := range []string{"ref_id", "ref_type", "source_system", "validity", "version_status", "visibility"} {
		requiredStringField(ref, field, refBase, errors)
	}
	if refID, _ := stringField(ref, "ref_id"); refID != "" && !validControlledRef(refID) {
		add(errors, "BKN_TRACE_SENSITIVE_VALUE_LEAKED", refBase+".ref_id", "reference id must be a controlled namespaced identifier")
	}
	if validity, _ := stringField(ref, "validity"); validity != "" {
		if _, ok := validValidity[validity]; !ok {
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", refBase+".validity", "unsupported reference validity")
		}
	}
	if versionStatus, _ := stringField(ref, "version_status"); versionStatus != "" {
		if _, ok := validVersionStatus[versionStatus]; !ok {
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", refBase+".version_status", "unsupported reference version_status")
		}
	}
	checkVisibility(ref, refBase+".visibility", errors)
}

func requiredFactRefArray(payload map[string]any, key, base string, errors *evidencevo.ValidationErrors) {
	refs := arrayField(payload, key)
	if len(refs) == 0 {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a non-empty reference array")
		return
	}
	checkFactRefs(refs, key, base, errors)
}

func requiredOneFactRefArray(payload map[string]any, keys []string, base string, errors *evidencevo.ValidationErrors) {
	found := false
	for _, key := range keys {
		refs := arrayField(payload, key)
		if len(refs) == 0 {
			continue
		}
		found = true
		checkFactRefs(refs, key, base, errors)
	}
	if !found {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+keys[0], "one of "+strings.Join(keys, " or ")+" must be a non-empty reference array")
	}
}

func checkFactRefs(refs []any, key, base string, errors *evidencevo.ValidationErrors) {
	for i, item := range refs {
		refBase := base + "." + key + "[" + strconv.Itoa(i) + "]"
		switch ref := item.(type) {
		case string:
			if !validControlledRef(ref) {
				add(errors, "BKN_TRACE_SENSITIVE_VALUE_LEAKED", refBase, "reference must be a controlled namespaced identifier")
			}
		case map[string]any:
			checkTwoPointOneRef(ref, refBase, errors)
		default:
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", refBase, "reference must be a controlled identifier or reference object")
		}
	}
}

func validControlledRef(value string) bool {
	value = strings.TrimSpace(value)
	if !controlledRefRE.MatchString(value) {
		return false
	}
	namespace, _, _ := strings.Cut(value, ":")
	switch namespace {
	case "table", "column", "physical_table", "physical_field":
		return false
	default:
		return true
	}
}

func checkClaim(payload map[string]any, base string, normalized *evidencevo.NormalizedTrace, errors *evidencevo.ValidationErrors) {
	claimID, ok := stringField(payload, "claim_id")
	if !ok || claimID == "" {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".claim_id", "missing required field claim_id")
	}
	requiredStringField(payload, "claim_type", base, errors)
	claimType, _ := stringField(payload, "claim_type")
	requireEnum(claimType, []string{"answer", "structured_output", "finding", "recommendation", "action_decision", "eval_assertion"}, base+".claim_type", errors)
	requiredStringField(payload, "claim_hash", base, errors)
	requiredStringField(payload, "visibility", base, errors)
	requiredStringField(payload, "version_status", base, errors)
	checkVisibility(payload, base+".visibility", errors)
	normalized.ClaimCount++
	if claimID != "" {
		normalized.ClaimIDs = append(normalized.ClaimIDs, claimID)
	}
}

func checkToolCalled(payload map[string]any, base string, requestVersion string, errors *evidencevo.ValidationErrors) {
	requiredStringField(payload, "tool_id", base, errors)
	requiredStringField(payload, "tool_name", base, errors)
	requiredStringField(payload, "args_hash", base, errors)
	requiredStringField(payload, "visibility", base, errors)
	requiredStringField(payload, "version_status", base, errors)
	if requestVersion == evidencevo.ContractVersion {
		requiredVersionStatus(payload, base, errors)
	}
}

func checkToolResultObserved(payload map[string]any, base string, requestVersion string, errors *evidencevo.ValidationErrors) {
	requiredStringField(payload, "tool_id", base, errors)
	requiredStringField(payload, "tool_name", base, errors)
	requiredStringField(payload, "status", base, errors)
	requiredStringField(payload, "visibility", base, errors)
	status, ok := stringField(payload, "status")
	if requestVersion != evidencevo.ContractVersion {
		if ok && status == "success" {
			requiredStringField(payload, "result_hash", base, errors)
		} else if ok {
			requiredStringField(payload, "error_hash", base, errors)
		}
		return
	}
	requiredVersionStatus(payload, base, errors)
	requireEnum(status, []string{"success", "error"}, base+".status", errors)
	if ok && status == "success" {
		requiredStringField(payload, "result_hash", base, errors)
		requiredNonNegativeIntField(payload, "result_length", base, errors)
		requiredNonNegativeIntField(payload, "result_count", base, errors)
	}
	if ok && status == "error" {
		requiredStringField(payload, "error_hash", base, errors)
		requiredStringField(payload, "error_category", base, errors)
	}
}

func checkRefs(payload map[string]any, base string, key string, knownClaims map[string]struct{}, requestVersion string, errors *evidencevo.ValidationErrors) {
	claimID, ok := stringField(payload, "claim_id")
	if !ok || claimID == "" {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".claim_id", "missing required field claim_id")
	} else if _, exists := knownClaims[claimID]; !exists && requestVersion == evidencevo.LegacyContractVersion {
		add(errors, "BKN_TRACE_UNKNOWN_CLAIM_ID", base+".claim_id", "refs must point to a known claim_id in the same batch")
	}
	refs := arrayField(payload, key)
	allowEmptyUnresolvedBusinessRefs := requestVersion == evidencevo.ContractVersion && key == "business_refs"
	if status, _ := stringField(payload, "resolver_status"); status != "unresolved" {
		allowEmptyUnresolvedBusinessRefs = false
	}
	if len(refs) == 0 && !allowEmptyUnresolvedBusinessRefs {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a non-empty array")
	}
	for i, item := range refs {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		checkVisibility(ref, base+"."+key+"["+strconv.Itoa(i)+"].visibility", errors)
	}
}

func checkSensitive(value any, path string, errors *evidencevo.ValidationErrors) {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			childPath := path + "." + k
			if forbiddenRawKey(k) {
				add(errors, "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD", childPath, "raw prompt, SQL, answer, tool IO, row data, token, cookie, or authorization fields are forbidden")
			}
			checkSensitive(v, childPath, errors)
		}
	case []any:
		for i, v := range typed {
			checkSensitive(v, path+"["+strconv.Itoa(i)+"]", errors)
		}
	case string:
		for _, pattern := range sensitivePatterns {
			if pattern.MatchString(typed) {
				add(errors, "BKN_TRACE_SENSITIVE_VALUE_LEAKED", path, "sensitive value must be redacted, hashed, or referenced")
				return
			}
		}
	}
}

func forbiddenRawKey(key string) bool {
	_, ok := forbiddenRawKeys[strings.ToLower(key)]
	return ok
}

func checkVisibility(payload map[string]any, path string, errors *evidencevo.ValidationErrors) {
	visibility, _ := stringField(payload, "visibility")
	if _, ok := visibilityStates[visibility]; ok {
		return
	}
	add(errors, "BKN_TRACE_VISIBILITY_UNSUPPORTED", path, "unsupported visibility state")
}

func validTraceparent(value string) bool {
	match := traceparentRE.FindStringSubmatch(value)
	if len(match) != 3 {
		return false
	}
	return match[1] != strings.Repeat("0", 32) && match[2] != strings.Repeat("0", 16)
}

func validTimestamp(value string) bool {
	if !timestampRE.MatchString(value) {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Location() == time.UTC
}

func required(value string, path string, errors *evidencevo.ValidationErrors) {
	if value == "" {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", path, "missing required field")
	}
}

func requiredStringField(payload map[string]any, key string, base string, errors *evidencevo.ValidationErrors) {
	value, ok := stringField(payload, key)
	if !ok || value == "" {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, "missing required field "+key)
	}
}

func requiredField(payload map[string]any, key string, base string, errors *evidencevo.ValidationErrors) {
	if value, ok := payload[key]; !ok || value == nil {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, "missing required field "+key)
	}
}

func requiredNonNegativeIntField(payload map[string]any, key string, base string, errors *evidencevo.ValidationErrors) {
	value, ok := payload[key]
	if !ok {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, "missing required field "+key)
		return
	}
	number, ok := value.(float64)
	if !ok || number < 0 || number != float64(int64(number)) {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a non-negative integer")
	}
}

func requiredBoolField(payload map[string]any, key string, base string, errors *evidencevo.ValidationErrors) {
	value, ok := payload[key]
	if !ok {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, "missing required field "+key)
		return
	}
	if _, ok := value.(bool); !ok {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a boolean")
	}
}

func requiredStringArrayField(payload map[string]any, key string, base string, errors *evidencevo.ValidationErrors) {
	values := arrayField(payload, key)
	if len(values) == 0 {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a non-empty string array")
		return
	}
	for i, value := range values {
		if text, ok := value.(string); !ok || strings.TrimSpace(text) == "" {
			add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key+"["+strconv.Itoa(i)+"]", key+" entries must be non-empty strings")
		}
	}
}

func requiredVersionStatus(payload map[string]any, base string, errors *evidencevo.ValidationErrors) {
	value, _ := stringField(payload, "version_status")
	requiredStringField(payload, "version_status", base, errors)
	requireEnum(value, []string{"versioned", "unversioned", "not_auditable"}, base+".version_status", errors)
}

func requireEnum(value string, allowed []string, path string, errors *evidencevo.ValidationErrors) {
	if value == "" {
		return
	}
	for _, item := range allowed {
		if value == item {
			return
		}
	}
	add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", path, "unsupported enum value")
}

func requiredArrayField(payload map[string]any, key string, base string, errors *evidencevo.ValidationErrors) {
	if len(arrayField(payload, key)) == 0 {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a non-empty array")
	}
}

func requiredOneStringField(payload map[string]any, keys []string, base string, errors *evidencevo.ValidationErrors) {
	for _, key := range keys {
		if value, ok := stringField(payload, key); ok && value != "" {
			return
		}
	}
	add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+keys[0], "one of "+strings.Join(keys, " or ")+" is required")
}

func stringField(payload map[string]any, key string) (string, bool) {
	value, ok := payload[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	return str, ok
}

func arrayField(payload map[string]any, key string) []any {
	value, ok := payload[key]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}

func add(errors *evidencevo.ValidationErrors, code, path, message string) {
	*errors = append(*errors, evidencevo.NewValidationError(code, path, message))
}

func path(collection string, index int) string {
	return "$." + collection + "[" + strconv.Itoa(index) + "]"
}
