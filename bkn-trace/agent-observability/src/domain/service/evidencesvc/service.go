package evidencesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
)

var (
	traceparentRE = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)
	traceIDRE     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	requestIDRE   = regexp.MustCompile(`^req_[0-9A-Za-z_.-]+$`)
	timestampRE   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$`)
)

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)\b(access|refresh|id)[_-]?token\s*[:=]\s*[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)\bapi[_-]?key\s*[:=]\s*[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)\bcookie\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?is)\bselect\s+.+\s+from\b`),
}

var forbiddenRawKeys = map[string]struct{}{
	"access-token":  {},
	"access_token":  {},
	"api-key":       {},
	"api_key":       {},
	"authorization": {},
	"cookie":        {},
	"id-token":      {},
	"id_token":      {},
	"raw-answer":    {},
	"raw-input":     {},
	"raw-output":    {},
	"raw-prompt":    {},
	"raw-sql":       {},
	"raw_answer":    {},
	"raw_input":     {},
	"raw_output":    {},
	"raw_prompt":    {},
	"raw_sql":       {},
	"refresh-token": {},
	"refresh_token": {},
	"row-data":      {},
	"row_data":      {},
	"token":         {},
}

var eventTypes = map[string]struct{}{
	"claim.created":               {},
	"evidence.refs.created":       {},
	"business.refs.resolved":      {},
	"structured_output.validated": {},
	"agent_as_tool.invoked":       {},
	"tool.budget.exhausted":       {},
	"action.recommended":          {},
	"action.approval_requested":   {},
	"action.approved":             {},
	"action.rejected":             {},
	"action.executed":             {},
	"action.result_recorded":      {},
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

	errors := evidencevo.ValidationErrors{}
	checkSensitive(raw, "$", &errors)
	if len(errors) > 0 {
		return evidencevo.IngestResponse{}, errors, nil
	}

	var req evidencevo.IngestRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return evidencevo.IngestResponse{}, evidencevo.ValidationErrors{
			evidencevo.NewValidationError("BKN_TRACE_INVALID_JSON", "$", "request body must match evidence ingest schema"),
		}, nil
	}

	normalized := normalize(req, &errors)
	if len(errors) > 0 {
		return evidencevo.IngestResponse{}, errors, nil
	}

	if err := s.store.StoreEvidence(ctx, normalized); err != nil {
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

	for _, trace := range traces {
		for _, event := range trace.Events {
			switch event.EventType {
			case "claim.created":
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
				claimID, _ := stringField(event.Payload, "claim_id")
				if claimID != "" {
					claimRefs[claimID] = struct{}{}
				}
				response.Data.BusinessRefs = appendVisibleRefs(response.Data.BusinessRefs, arrayField(event.Payload, "business_refs"), &response.VisibilitySummary)
			}
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

	for _, trace := range traces {
		for _, event := range trace.Events {
			if event.EventType == "business.refs.resolved" {
				businessRefEvents++
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
					addBusinessNode(&response, businessNodes, refID, claimID, ref)
					if claimID != "" && !edgeSeen(edges, "claim:"+claimID, "business:"+refID, businessEdgeType(ref)) {
						edgeIndex++
						response.Data.Edges = append(response.Data.Edges, evidencevo.BusinessGraphEdge{
							ID:         "edge:" + strconv.Itoa(edgeIndex),
							SourceID:   "claim:" + claimID,
							TargetID:   "business:" + refID,
							EdgeType:   businessEdgeType(ref),
							Visibility: visibilityValue(ref),
						})
					}
				}
			}
		}
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
	label, _ := stringField(ref, "label")
	versionStatus, _ := stringField(ref, "version_status")
	response.Data.Nodes = append(response.Data.Nodes, evidencevo.BusinessGraphNode{
		ID:            "business:" + refID,
		NodeType:      nodeType,
		Label:         label,
		ClaimID:       claimID,
		VersionStatus: versionStatus,
		Visibility:    visibilityValue(ref),
		Properties:    cloneMap(ref),
	})
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

func normalize(req evidencevo.IngestRequest, errors *evidencevo.ValidationErrors) evidencevo.NormalizedTrace {
	if req.SchemaVersion != evidencevo.ContractVersion {
		add(errors, "BKN_TRACE_SCHEMA_VERSION_UNSUPPORTED", "$.bkn.trace.schema.version", "unsupported phase-two contract version")
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
		SchemaVersion:  req.SchemaVersion,
		Events:         req.Events,
		AcceptedEvents: len(req.Events),
	}
	for i, event := range req.Events {
		checkEvent(event, i, knownClaims, &normalized, errors)
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
	}
}

func checkEvent(event evidencevo.EvidenceEvent, i int, knownClaims map[string]struct{}, normalized *evidencevo.NormalizedTrace, errors *evidencevo.ValidationErrors) {
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
	if event.SchemaVersion != "" && event.SchemaVersion != evidencevo.ContractVersion {
		add(errors, "BKN_TRACE_SCHEMA_VERSION_UNSUPPORTED", base+".bkn.trace.schema.version", "unsupported event contract version")
	}
	if event.ObservedAt != "" && !timestampRE.MatchString(event.ObservedAt) {
		add(errors, "BKN_TRACE_INVALID_TIMESTAMP", base+".observed_at", "timestamp must be UTC RFC3339Nano")
	}
	if event.EmittedAt != "" && !timestampRE.MatchString(event.EmittedAt) {
		add(errors, "BKN_TRACE_INVALID_TIMESTAMP", base+".emitted_at", "timestamp must be UTC RFC3339Nano")
	}
	if _, ok := eventTypes[event.EventType]; event.EventType != "" && !ok {
		add(errors, "BKN_TRACE_EVENT_TYPE_UNREGISTERED", base+".event_type", "event type is not registered for phase two")
	}
	if event.Payload == nil {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".payload", "payload must be an object")
		return
	}

	switch event.EventType {
	case "claim.created":
		checkClaim(event.Payload, base+".payload", normalized, errors)
	case "evidence.refs.created":
		checkRefs(event.Payload, base+".payload", "evidence_refs", knownClaims, errors)
		normalized.EvidenceRefCount += len(arrayField(event.Payload, "evidence_refs"))
	case "business.refs.resolved":
		checkRefs(event.Payload, base+".payload", "business_refs", knownClaims, errors)
		normalized.BusinessRefCount += len(arrayField(event.Payload, "business_refs"))
	}
}

func checkClaim(payload map[string]any, base string, normalized *evidencevo.NormalizedTrace, errors *evidencevo.ValidationErrors) {
	claimID, ok := stringField(payload, "claim_id")
	if !ok || claimID == "" {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".claim_id", "missing required field claim_id")
	}
	requiredStringField(payload, "claim_type", base, errors)
	requiredStringField(payload, "claim_hash", base, errors)
	requiredStringField(payload, "visibility", base, errors)
	requiredStringField(payload, "version_status", base, errors)
	checkVisibility(payload, base+".visibility", errors)
	normalized.ClaimCount++
	if claimID != "" {
		normalized.ClaimIDs = append(normalized.ClaimIDs, claimID)
	}
}

func checkRefs(payload map[string]any, base string, key string, knownClaims map[string]struct{}, errors *evidencevo.ValidationErrors) {
	claimID, ok := stringField(payload, "claim_id")
	if !ok || claimID == "" {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".claim_id", "missing required field claim_id")
	} else if _, exists := knownClaims[claimID]; !exists {
		add(errors, "BKN_TRACE_UNKNOWN_CLAIM_ID", base+".claim_id", "refs must point to a known claim_id in the same batch")
	}
	refs := arrayField(payload, key)
	if len(refs) == 0 {
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
