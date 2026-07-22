package evidencesvc

import (
	"context"
	"encoding/json"
	"regexp"
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
	rawKeyRE      = regexp.MustCompile(`(?i)(raw[_-]?(sql|prompt|answer|output|input)|row[_-]?data|authorization|cookie|token|api[_-]?key)`)
)

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)access[_-]?token`),
	regexp.MustCompile(`(?i)api[_-]?key`),
	regexp.MustCompile(`(?i)cookie`),
	regexp.MustCompile(`(?is)\bselect\s+.+\s+from\b`),
	regexp.MustCompile(`(?i)prompt\s*[:=]`),
	regexp.MustCompile(`(?i)https?://[^\s"']+`),
	regexp.MustCompile(`[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}`),
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

type Service struct {
	store ievidencestore.EvidenceStorePort
}

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
	normalized.ClaimCount++
	if claimID != "" {
		normalized.ClaimIDs = append(normalized.ClaimIDs, claimID)
	}
}

func checkRefs(payload map[string]any, base string, key string, knownClaims map[string]struct{}, errors *evidencevo.ValidationErrors) {
	claimID, ok := stringField(payload, "claim_id")
	if !ok || claimID == "" {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+".claim_id", "missing required field claim_id")
	} else if len(knownClaims) > 0 {
		if _, exists := knownClaims[claimID]; !exists {
			add(errors, "BKN_TRACE_UNKNOWN_CLAIM_ID", base+".claim_id", "refs must point to a known claim_id")
		}
	}
	refs := arrayField(payload, key)
	if len(refs) == 0 {
		add(errors, "BKN_TRACE_REQUIRED_FIELD_MISSING", base+"."+key, key+" must be a non-empty array")
	}
}

func checkSensitive(value any, path string, errors *evidencevo.ValidationErrors) {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			childPath := path + "." + k
			if rawKeyRE.MatchString(k) {
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
