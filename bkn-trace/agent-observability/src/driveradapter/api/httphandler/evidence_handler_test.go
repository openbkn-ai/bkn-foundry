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

func TestEvidenceHandlerRejectsIngestWithoutConfiguredToken(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandlerWithIngestToken(evidencesvc.New(store), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	queryReq := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/evidence-chain", nil)
	queryRec := httptest.NewRecorder()
	handler.GetEvidenceChainByTraceID(queryRec, queryReq)
	if queryRec.Code != http.StatusNotFound {
		t.Fatalf("expected rejected event to stay unstored, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestEvidenceHandlerAcceptsBearerIngestToken(t *testing.T) {
	handler := NewEvidenceHandlerWithIngestToken(evidencesvc.New(evidencestore.New()), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAcceptsIngestTokenHeader(t *testing.T) {
	handler := NewEvidenceHandlerWithIngestToken(evidencesvc.New(evidencestore.New()), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	req.Header.Set("X-BKN-Trace-Ingest-Token", "secret-token")
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
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

func TestEvidenceHandlerReturnsEvidenceChainByTrace(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/evidence-chain", nil)
	rec := httptest.NewRecorder()
	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"trace_id":"9c0d0000000000000000000000000001"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"claims"`) {
		t.Fatalf("expected claims in body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsEvidenceChainByRequest(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request?request_id=req_handler_001", nil)
	rec := httptest.NewRecorder()
	handler.GetEvidenceChainByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_001"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsInvalidEvidenceQueryLimit(t *testing.T) {
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request?request_id=req_handler_001&limit=0", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByRequestID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"limit must be an integer between 1 and 1000"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsBusinessGraphByTrace(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000002/business-graph", nil)
	rec := httptest.NewRecorder()
	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"nodes"`) || !strings.Contains(rec.Body.String(), `"edges"`) {
		t.Fatalf("expected graph data in body: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"target_id":"business:object:customer"`) {
		t.Fatalf("expected business node edge in body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsBusinessGraphByRequest(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request/business-graph?request_id=req_handler_002", nil)
	rec := httptest.NewRecorder()
	handler.GetBusinessGraphByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_002"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsSnapshotPreviewByTraceWithoutStorageURI(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000002/snapshot-preview", nil)
	rec := httptest.NewRecorder()
	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"mode":"preview"`) || !strings.Contains(body, `"manifest_hash":"sha256:`) {
		t.Fatalf("expected snapshot preview manifest in body: %s", body)
	}
	if strings.Contains(body, `"uri"`) || strings.Contains(body, "s3://") || strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Fatalf("snapshot preview must not expose object storage uri or bare urls: %s", body)
	}
}

func TestEvidenceHandlerReturnsSnapshotPreviewByRequest(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request/snapshot-preview?request_id=req_handler_002", nil)
	rec := httptest.NewRecorder()
	handler.GetSnapshotPreviewByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_002"`) || !strings.Contains(rec.Body.String(), `"snapshot_ref"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsEvidenceNodeByTrace(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/evidence-nodes/claim%3Aclaim_handler?trace_id=9c0d0000000000000000000000000001", nil)
	rec := httptest.NewRecorder()
	handler.GetEvidenceNode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"node_id":"claim:claim_handler"`) || !strings.Contains(rec.Body.String(), `"node_type":"claim"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsEvidenceNodeWithoutScope(t *testing.T) {
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/evidence-nodes/claim%3Aclaim_handler", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceNode(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "trace_id or request_id is required") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsNotFoundForUnknownTraceSubresource(t *testing.T) {
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/unknown", nil)
	rec := httptest.NewRecorder()

	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsNotFoundForMissingEvidenceChain(t *testing.T) {
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
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

func validHandlerBusinessBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "9c0d0000000000000000000000000002",
    "bkn.request.id": "req_handler_002",
    "traceparent": "00-9c0d0000000000000000000000000002-2f12000000000002-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim_business",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "9c0d0000000000000000000000000002",
      "span_id": "2f12000000000002",
      "bkn.request.id": "req_handler_002",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_handler_business",
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    },
    {
      "event_id": "evt_business",
      "event_type": "business.refs.resolved",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.002000000Z",
      "emitted_at": "2026-07-22T04:00:00.003000000Z",
      "producer_module": "bkn-trace",
      "trace_id": "9c0d0000000000000000000000000002",
      "span_id": "2f12000000000002",
      "bkn.request.id": "req_handler_002",
      "bkn.operation.name": "bkn_trace.resolve_business_refs",
      "payload": {
        "claim_id": "claim_handler_business",
        "business_refs": [
          {
            "ref_id": "object:customer",
            "ref_type": "object",
            "label": "Customer",
            "visibility": "visible",
            "version_status": "versioned"
          }
        ]
      }
    }
  ]
}`
}
