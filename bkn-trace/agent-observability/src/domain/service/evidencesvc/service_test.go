package evidencesvc

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type fakeStore struct {
	calls int
}

func (s *fakeStore) StoreEvidence(_ context.Context, _ evidencevo.NormalizedTrace) error {
	s.calls++
	return nil
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

func hasValidationCode(validationErrors evidencevo.ValidationErrors, code string) bool {
	for _, validationError := range validationErrors {
		if validationError.Code == code {
			return true
		}
	}
	return false
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
