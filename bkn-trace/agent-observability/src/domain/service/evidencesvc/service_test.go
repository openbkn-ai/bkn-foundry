package evidencesvc

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type fakeStore struct {
	calls  int
	traces []evidencevo.NormalizedTrace
}

func (s *fakeStore) StoreEvidence(_ context.Context, _ evidencevo.NormalizedTrace) error {
	s.calls++
	return nil
}

func (s *fakeStore) GetEvidenceByTraceID(_ context.Context, traceID string) ([]evidencevo.NormalizedTrace, error) {
	var result []evidencevo.NormalizedTrace
	for _, trace := range s.traces {
		if trace.TraceID == traceID {
			result = append(result, trace)
		}
	}
	return result, nil
}

func (s *fakeStore) GetEvidenceByRequestID(_ context.Context, requestID string) ([]evidencevo.NormalizedTrace, error) {
	var result []evidencevo.NormalizedTrace
	for _, trace := range s.traces {
		if trace.RequestID == requestID {
			result = append(result, trace)
		}
	}
	return result, nil
}

func TestIngestAcceptsPhaseTwoEvidenceBatch(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	response, validationErrors, err := service.Ingest(context.Background(), []byte(validBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	if store.calls != 1 {
		t.Fatalf("expected evidence to be stored once, got %d", store.calls)
	}
	if response.TraceID != "8c0d0000000000000000000000000001" {
		t.Fatalf("unexpected trace id: %s", response.TraceID)
	}
	if response.AcceptedEvents != 3 || response.ClaimCount != 1 || response.EvidenceRefCount != 1 || response.BusinessRefCount != 1 {
		t.Fatalf("unexpected response counts: %+v", response)
	}
}

func TestIngestRejectsMissingClaimID(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(missingClaimIDBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) == 0 {
		t.Fatal("expected validation errors")
	}
	if validationErrors[0].Code != "BKN_TRACE_REQUIRED_FIELD_MISSING" {
		t.Fatalf("unexpected error code: %+v", validationErrors[0])
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsSensitivePayload(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(sensitiveBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) == 0 {
		t.Fatal("expected validation errors")
	}
	if validationErrors[0].Code != "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD" {
		t.Fatalf("unexpected error code: %+v", validationErrors[0])
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsUnknownClaimIDWithoutClaimBatch(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(unknownClaimIDBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_UNKNOWN_CLAIM_ID") {
		t.Fatalf("expected unknown claim id error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsJoinMismatch(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"bkn.request.id": "req_phase2_001",`, `"bkn.request.id": "req_phase2_other",`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_JOIN_FAILED") {
		t.Fatalf("expected join failed error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsUnsupportedSchemaVersion(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"bkn.trace.schema.version": "2.0.0"`, `"bkn.trace.schema.version": "1.0.0"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_SCHEMA_VERSION_UNSUPPORTED") {
		t.Fatalf("expected unsupported schema error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsInvalidTraceparent(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"traceparent": "00-8c0d0000000000000000000000000001-1f12000000000001-01"`, `"traceparent": "00-00000000000000000000000000000000-0000000000000000-01"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_INVALID_TRACEPARENT") {
		t.Fatalf("expected invalid traceparent error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsInvalidTimestamp(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"observed_at": "2026-07-22T04:00:00.000000000Z"`, `"observed_at": "2026-07-22 04:00:00"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_INVALID_TIMESTAMP") {
		t.Fatalf("expected invalid timestamp error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsEmptyEvents(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(emptyEventsBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_REQUIRED_FIELD_MISSING") {
		t.Fatalf("expected required field error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestAllowsReferenceLikeStringsAndNonSensitiveKeySubstrings(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"version_status": "versioned"`, `"version_status": "versioned",
        "source_url": "https://docs.example.com/source/123",
        "owner_ref": "user@company.com",
        "prompt_note": "prompt: is a label in external documentation",
        "token_bucket": "rate-limit-window",
        "cookie_policy": "same-site",
        "authorization_scope": "trace:evidence"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	if store.calls != 1 {
		t.Fatalf("expected evidence to be stored once, got %d", store.calls)
	}
}

func TestGetEvidenceChainByTraceIDFiltersHiddenRefsAndReturnsEnvelope(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_query_001", "req_query_001")}}
	service := New(store)

	response, found, err := service.GetEvidenceChainByTraceID(context.Background(), "trace_query_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected evidence chain to be found")
	}
	if response.TraceID != "trace_query_001" || response.RequestID != "req_query_001" {
		t.Fatalf("unexpected identity: %+v", response)
	}
	if response.Partial {
		t.Fatalf("expected complete visible response, got partial: %+v", response.PartialReasons)
	}
	if response.VisibilitySummary.HiddenRefCount != 1 || response.VisibilitySummary.AuthorizedRefCount != 2 {
		t.Fatalf("unexpected visibility summary: %+v", response.VisibilitySummary)
	}
	if response.Page.NodeCount != 3 || response.Page.EdgeCount != 2 || response.Page.Truncated {
		t.Fatalf("unexpected page: %+v", response.Page)
	}
	if len(response.Data.Claims) != 1 || len(response.Data.EvidenceRefs) != 1 || len(response.Data.BusinessRefs) != 1 {
		t.Fatalf("unexpected data counts: %+v", response.Data)
	}
	if response.Data.EvidenceRefs[0]["ref_id"] != "row:visible" {
		t.Fatalf("hidden evidence ref leaked or visible ref missing: %+v", response.Data.EvidenceRefs)
	}
}

func TestGetEvidenceChainByRequestIDReturnsMissingClaimPartial(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{{
		TraceID:   "trace_no_claim",
		RequestID: "req_no_claim",
		Events: []evidencevo.EvidenceEvent{
			{
				EventType: "evidence.refs.created",
				Payload: map[string]any{
					"claim_id":      "missing_claim",
					"evidence_refs": []any{map[string]any{"ref_id": "row:visible", "visibility": "visible"}},
				},
			},
		},
	}}}
	service := New(store)

	response, found, err := service.GetEvidenceChainByRequestID(context.Background(), "req_no_claim")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected evidence chain to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "missing_claim") {
		t.Fatalf("expected missing claim partial, got: %+v", response)
	}
}

func TestGetBusinessGraphByTraceIDReturnsClaimAndBusinessNodes(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_graph_001", "req_graph_001")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if response.TraceID != "trace_graph_001" || response.RequestID != "req_graph_001" {
		t.Fatalf("unexpected identity: %+v", response)
	}
	if response.Partial {
		t.Fatalf("expected complete graph, got partial: %+v", response.PartialReasons)
	}
	if response.Page.NodeCount != 2 || response.Page.EdgeCount != 1 {
		t.Fatalf("unexpected page: %+v", response.Page)
	}
	if response.VisibilitySummary.AuthorizedRefCount != 1 {
		t.Fatalf("unexpected visibility summary: %+v", response.VisibilitySummary)
	}
	if len(response.Data.Nodes) != 2 || len(response.Data.Edges) != 1 {
		t.Fatalf("unexpected graph size: %+v", response.Data)
	}
	if response.Data.Edges[0].SourceID != "claim:claim_visible" || response.Data.Edges[0].TargetID != "business:object:customer" {
		t.Fatalf("unexpected edge: %+v", response.Data.Edges[0])
	}
}

func TestGetBusinessGraphByRequestIDHandlesHiddenAndUnresolvedRefs(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithGovernance("trace_graph_002", "req_graph_002")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByRequestID(context.Background(), "req_graph_002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "business_ref_unresolved") {
		t.Fatalf("expected unresolved partial reason, got: %+v", response)
	}
	if response.VisibilitySummary.HiddenRefCount != 1 || response.VisibilitySummary.UnresolvedRefCount != 1 {
		t.Fatalf("unexpected visibility summary: %+v", response.VisibilitySummary)
	}
	if len(response.Data.Nodes) != 2 {
		t.Fatalf("hidden/unresolved refs must not leak as graph nodes: %+v", response.Data.Nodes)
	}
}

func TestGetBusinessGraphDoesNotDependOnEventOrder(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithBusinessBeforeClaim("trace_graph_003", "req_graph_003")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_003")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if contains(response.PartialReasons, "missing_claim") {
		t.Fatalf("business graph must collect claims before linking refs: %+v", response)
	}
	if response.Page.NodeCount != 2 || response.Page.EdgeCount != 1 {
		t.Fatalf("unexpected page: %+v", response.Page)
	}
}

func TestGetBusinessGraphDeduplicatesEdgesAndAuthorizedRefs(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithDuplicateRefs("trace_graph_004", "req_graph_004")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_004")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if response.Page.EdgeCount != 1 || len(response.Data.Edges) != 1 {
		t.Fatalf("expected duplicate edges to be collapsed, got page=%+v edges=%+v", response.Page, response.Data.Edges)
	}
	if response.VisibilitySummary.AuthorizedRefCount != 1 {
		t.Fatalf("expected duplicate refs to count once, got %+v", response.VisibilitySummary)
	}
}

func TestGetBusinessGraphDoesNotLeakHiddenClaimThroughSyntheticNode(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithHiddenClaim("trace_graph_005", "req_graph_005")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_005")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "hidden_claim") {
		t.Fatalf("expected hidden claim partial, got %+v", response)
	}
	if response.Page.NodeCount != 0 || response.Page.EdgeCount != 0 {
		t.Fatalf("hidden claim must not leak through nodes or edges: %+v", response)
	}
	if response.VisibilitySummary.HiddenRefCount != 1 || response.VisibilitySummary.OmittedRefCount != 1 {
		t.Fatalf("expected hidden claim and omitted business ref counts, got %+v", response.VisibilitySummary)
	}
}

func TestGetBusinessGraphReturnsMissingBusinessRefsPartial(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{{
		TraceID:   "trace_no_business_refs",
		RequestID: "req_no_business_refs",
		Events: []evidencevo.EvidenceEvent{
			{
				EventType: "claim.created",
				Payload: map[string]any{
					"claim_id":       "claim_no_business_refs",
					"claim_type":     "answer",
					"claim_hash":     "sha256:claim",
					"visibility":     "visible",
					"version_status": "versioned",
				},
			},
		},
	}}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_no_business_refs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "missing_business_refs") {
		t.Fatalf("expected missing business refs partial, got: %+v", response)
	}
}

func TestGetEvidenceChainByTraceIDNotFound(t *testing.T) {
	service := New(&fakeStore{})

	_, found, err := service.GetEvidenceChainByTraceID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func hasValidationCode(validationErrors evidencevo.ValidationErrors, code string) bool {
	for _, validationError := range validationErrors {
		if validationError.Code == code {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func queryTrace(traceID, requestID string) evidencevo.NormalizedTrace {
	return evidencevo.NormalizedTrace{
		TraceID:   traceID,
		RequestID: requestID,
		Events: []evidencevo.EvidenceEvent{
			{
				EventType: "claim.created",
				Payload: map[string]any{
					"claim_id":       "claim_visible",
					"claim_type":     "finding",
					"claim_hash":     "sha256:claim",
					"visibility":     "visible",
					"version_status": "versioned",
				},
			},
			{
				EventType: "evidence.refs.created",
				Payload: map[string]any{
					"claim_id": "claim_visible",
					"evidence_refs": []any{
						map[string]any{"ref_id": "row:visible", "ref_type": "row_ref", "visibility": "visible"},
						map[string]any{"ref_id": "row:hidden", "ref_type": "row_ref", "visibility": "hidden"},
					},
				},
			},
			{
				EventType: "business.refs.resolved",
				Payload: map[string]any{
					"claim_id":      "claim_visible",
					"business_refs": []any{map[string]any{"ref_id": "object:customer", "ref_type": "object", "label": "Customer", "visibility": "visible", "version_status": "versioned"}},
				},
			},
		},
	}
}

func businessGraphTraceWithGovernance(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[2].Payload["business_refs"] = []any{
		map[string]any{"ref_id": "object:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"},
		map[string]any{"ref_id": "object:hidden", "ref_type": "object", "visibility": "hidden"},
		map[string]any{"ref_id": "object:deleted", "ref_type": "object", "visibility": "unresolved"},
	}
	return trace
}

func businessGraphTraceWithBusinessBeforeClaim(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[0], trace.Events[2] = trace.Events[2], trace.Events[0]
	return trace
}

func businessGraphTraceWithDuplicateRefs(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[2].Payload["business_refs"] = []any{
		map[string]any{"ref_id": "object:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"},
		map[string]any{"ref_id": "object:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"},
	}
	trace.Events = append(trace.Events, trace.Events[2])
	return trace
}

func businessGraphTraceWithHiddenClaim(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[0].Payload["visibility"] = "hidden"
	return trace
}

func validBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000001",
    "bkn.request.id": "req_phase2_001",
    "traceparent": "00-8c0d0000000000000000000000000001-1f12000000000001-01",
    "bkn.tenant.id": "tenant_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000001",
      "span_id": "1f12000000000001",
      "bkn.request.id": "req_phase2_001",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_001",
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    },
    {
      "event_id": "evt_evidence",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.002000000Z",
      "emitted_at": "2026-07-22T04:00:00.003000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000001",
      "span_id": "1f12000000000001",
      "bkn.request.id": "req_phase2_001",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_001",
        "evidence_refs": [{"ref_id": "eref_001"}]
      }
    },
    {
      "event_id": "evt_business",
      "event_type": "business.refs.resolved",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.004000000Z",
      "emitted_at": "2026-07-22T04:00:00.005000000Z",
      "producer_module": "bkn-trace",
      "trace_id": "8c0d0000000000000000000000000001",
      "span_id": "1f12000000000001",
      "bkn.request.id": "req_phase2_001",
      "bkn.operation.name": "bkn_trace.resolve_business_refs",
      "payload": {
        "claim_id": "claim_001",
        "business_refs": [{"ref_id": "bref_001"}]
      }
    }
  ]
}`
}

func missingClaimIDBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000002",
    "bkn.request.id": "req_phase2_002",
    "traceparent": "00-8c0d0000000000000000000000000002-1f12000000000002-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim_missing",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000002",
      "span_id": "1f12000000000002",
      "bkn.request.id": "req_phase2_002",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    }
  ]
}`
}

func sensitiveBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000003",
    "bkn.request.id": "req_phase2_003",
    "traceparent": "00-8c0d0000000000000000000000000003-1f12000000000003-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_sensitive",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "vega-data",
      "trace_id": "8c0d0000000000000000000000000003",
      "span_id": "1f12000000000003",
      "bkn.request.id": "req_phase2_003",
      "bkn.operation.name": "data.query.execute",
      "payload": {
        "claim_id": "claim_003",
        "evidence_refs": [{"ref_id": "eref_003"}],
        "raw_sql": "select email from customer"
      }
    }
  ]
}`
}

func unknownClaimIDBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000004",
    "bkn.request.id": "req_phase2_004",
    "traceparent": "00-8c0d0000000000000000000000000004-1f12000000000004-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_unknown_claim",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000004",
      "span_id": "1f12000000000004",
      "bkn.request.id": "req_phase2_004",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_DOES_NOT_EXIST",
        "evidence_refs": [{"ref_id": "eref_004"}]
      }
    }
  ]
}`
}

func emptyEventsBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000005",
    "bkn.request.id": "req_phase2_005",
    "traceparent": "00-8c0d0000000000000000000000000005-1f12000000000005-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": []
}`
}
