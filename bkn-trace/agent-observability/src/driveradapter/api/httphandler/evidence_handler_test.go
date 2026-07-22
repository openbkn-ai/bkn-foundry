package httphandler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

func TestEvidenceHandlerAcceptsValidBatch(t *testing.T) {
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"accepted_event_count":1`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsSensitivePayload(t *testing.T) {
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(strings.Replace(validHandlerBatch(), `"claim_hash": "sha256:claim"`, `"raw_sql": "select email from customer"`, 1)))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsValidationErrorDetails(t *testing.T) {
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))
	body := strings.Replace(validHandlerBatch(), `"claim_hash": "sha256:claim",`, "", 1)
	body = strings.Replace(body, `"visibility": "visible",`, "", 1)
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("expected validation details, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `claim_hash`) || !strings.Contains(rec.Body.String(), `visibility`) {
		t.Fatalf("expected all validation errors in details, got: %s", rec.Body.String())
	}
}

func validHandlerBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "9c0d0000000000000000000000000001",
    "bkn.request.id": "req_handler_001",
    "traceparent": "00-9c0d0000000000000000000000000001-2f12000000000001-01",
    "business_domain": "bd_demo",
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
      "trace_id": "9c0d0000000000000000000000000001",
      "span_id": "2f12000000000001",
      "bkn.request.id": "req_handler_001",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_handler",
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    }
  ]
}`
}
