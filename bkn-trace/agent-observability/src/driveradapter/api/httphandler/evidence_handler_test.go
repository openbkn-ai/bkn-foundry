package httphandler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

func newDevEvidenceHandler(service *evidencesvc.Service) *EvidenceHandler {
	return NewEvidenceHandlerWithSecurityConfig(service, EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedIngest: true,
		AllowUnauthenticatedQuery:  true,
	})
}

func authenticatedQueryRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("x-account-id", "acct_demo")
	req.Header.Set("x-account-type", "app")
	req.Header.Set("x-business-domain", "bd_demo")
	return req
}

func TestEvidenceHandlerFailsClosedWhenIngestTokenIsUnconfigured(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurity(evidencesvc.New(evidencestore.New()), "", false)
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "INGEST_AUTH_NOT_CONFIGURED") {
		t.Fatalf("expected fail-closed configuration error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerDevelopmentBypassesDefaultToDisabled(t *testing.T) {
	t.Setenv(evidenceIngestTokenEnv, "")
	t.Setenv(evidenceQueryGatewayTokenEnv, "")
	t.Setenv(evidenceAllowUnauthenticatedIngestEnv, "")
	t.Setenv(evidenceAllowUnauthenticatedQueryEnv, "")
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))

	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusServiceUnavailable || !strings.Contains(ingestRec.Body.String(), "INGEST_AUTH_NOT_CONFIGURED") {
		t.Fatalf("unauthenticated ingest must default off, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	queryReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	queryRec := httptest.NewRecorder()
	handler.GetEvidenceChainByTraceID(queryRec, queryReq)
	if queryRec.Code != http.StatusServiceUnavailable || !strings.Contains(queryRec.Body.String(), "QUERY_AUTH_NOT_CONFIGURED") {
		t.Fatalf("unauthenticated query must default off, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestIngestDevelopmentBypassDoesNotEnableQueryBypass(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurity(evidencesvc.New(evidencestore.New()), "", true)
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "QUERY_AUTH_NOT_CONFIGURED") {
		t.Fatalf("ingest development bypass must not enable query bypass, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerRequiresTrustedQueryIdentity(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_IDENTITY_REQUIRED") {
		t.Fatalf("expected trusted identity rejection, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsForgedIdentityWithoutGatewayToken(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		IngestToken: "producer-ingest-token", QueryGatewayToken: "gateway-query-token",
	})
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("X-BKN-Trace-Ingest-Token", "producer-ingest-token")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_GATEWAY_AUTH_REQUIRED") {
		t.Fatalf("producer token and forged identity must not authorize query, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerFailsClosedWhenQueryGatewayTokenIsUnconfigured(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{})
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "QUERY_AUTH_NOT_CONFIGURED") {
		t.Fatalf("missing query gateway auth must fail closed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAuthenticatesStudioQueryWithHydra(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/introspect" || r.FormValue("token") != "studio-token" {
			t.Fatalf("unexpected introspection request: %s token=%q", r.URL.Path, r.FormValue("token"))
		}
		_, _ = io.WriteString(w, `{"active":true,"sub":"acct_demo","client_id":"openbkn-studio","ext":{"visitor_type":"realname"}}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("Authorization", "Bearer studio-token")
	req.Header.Set("x-business-domain", "bd_demo")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("valid OAuth query must reach scoped lookup, got %d: %s", rec.Code, rec.Body.String())
	}
	if req.Header.Get("x-account-id") != "acct_demo" || req.Header.Get("x-account-type") != "user" {
		t.Fatalf("trusted OAuth identity was not derived: headers=%v", req.Header)
	}
}

func TestEvidenceHandlerRejectsOAuthIdentityMismatch(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"active":true,"sub":"acct_demo","client_id":"openbkn-studio","ext":{"visitor_type":"user"}}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL,
	})
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("Authorization", "Bearer studio-token")
	req.Header.Set("x-account-id", "forged-account")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_IDENTITY_MISMATCH") {
		t.Fatalf("forged OAuth identity must be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsInactiveOAuthToken(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"active":false}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("Authorization", "Bearer inactive-token")
	req.Header.Set("x-business-domain", "bd_demo")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_OAUTH_REQUIRED") {
		t.Fatalf("inactive OAuth token must be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerFiltersEvidenceByOwnership(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("ingest failed: %d %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/evidence-chain", nil)
	req.Header.Set("x-account-id", "acct_other")
	rec := httptest.NewRecorder()
	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account query must not reveal existence, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAuthorizesTechnicalTraceByEvidenceOwnership(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	allowed := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/trace-graph", nil)
	if rec := httptest.NewRecorder(); !handler.AuthorizeTechnicalTraceQuery(rec, allowed) {
		t.Fatalf("owner must access technical trace: %d %s", rec.Code, rec.Body.String())
	}
	denied := authenticatedQueryRequest(http.MethodGet, allowed.URL.String(), nil)
	denied.Header.Set("x-business-domain", "bd_other")
	rec := httptest.NewRecorder()
	if handler.AuthorizeTechnicalTraceQuery(rec, denied) || rec.Code != http.StatusNotFound {
		t.Fatalf("cross-domain trace access must not reveal existence: %d %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAcceptsValidBatch(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
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

	if store.TraceCount("9c0d0000000000000000000000000001") != 0 {
		t.Fatal("rejected event must stay unstored")
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
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
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
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
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
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/evidence-chain", nil)
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
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request?request_id=req_handler_001", nil)
	rec := httptest.NewRecorder()
	handler.GetEvidenceChainByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_001"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerSearchEvidenceByTraceCompatibilityEndpoint(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/evidence/by-trace?trace_id=9c0d0000000000000000000000000001", nil)
	rec := httptest.NewRecorder()
	handler.SearchEvidenceByTrace(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"trace_id":"9c0d0000000000000000000000000001"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsInvalidEvidenceQueryLimit(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request?request_id=req_handler_001&limit=0", nil)
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
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000002/business-graph", nil)
	rec := httptest.NewRecorder()
	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"nodes"`) || !strings.Contains(rec.Body.String(), `"edges"`) {
		t.Fatalf("expected graph data in body: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"target_id":"business:object:kn_demo:customer"`) {
		t.Fatalf("expected business node edge in body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsBusinessGraphByRequest(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request/business-graph?request_id=req_handler_002", nil)
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
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000002/snapshot-preview", nil)
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
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/by-request/snapshot-preview?request_id=req_handler_002", nil)
	rec := httptest.NewRecorder()
	handler.GetSnapshotPreviewByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_002"`) || !strings.Contains(rec.Body.String(), `"snapshot_ref"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReportsUnauthorizedRefsWithoutLeakingDetails(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(unauthorizedHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000003/evidence-chain", nil)
	rec := httptest.NewRecorder()
	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"unauthorized_ref_count":1`) || !strings.Contains(body, `"evidence_ref_unauthorized"`) {
		t.Fatalf("expected unauthorized summary and partial reason: %s", body)
	}
	if strings.Contains(body, "row:unauthorized") || strings.Contains(body, "policy:deny:handler") {
		t.Fatalf("unauthorized ref detail must not leak: %s", body)
	}
}

func TestEvidenceHandlerReturnsEvidenceNodeByTrace(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/evidence-nodes/claim%3Aclaim_handler?trace_id=9c0d0000000000000000000000000001", nil)
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
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/evidence-nodes/claim%3Aclaim_handler", nil)
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
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/unknown", nil)
	rec := httptest.NewRecorder()

	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsNotFoundForMissingEvidenceChain(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerIngestsAndQueriesAuthorizedArtifact(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	ingestRec := httptest.NewRecorder()

	handler.IngestEvidenceArtifact(ingestRec, ingestReq)

	if ingestRec.Code != http.StatusCreated || !strings.Contains(ingestRec.Body.String(), `"created":true`) {
		t.Fatalf("expected artifact created, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	queryReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/evidence/artifacts/artifact_handler_001", nil)
	queryReq.Header.Set("x-tenant-id", "tenant_demo")
	queryRec := httptest.NewRecorder()
	handler.GetEvidenceArtifact(queryRec, queryReq)

	if queryRec.Code != http.StatusOK || !strings.Contains(queryRec.Body.String(), "用户原始问题") {
		t.Fatalf("expected authorized artifact content, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestEvidenceHandlerArtifactQueryDoesNotLeakUnauthorizedPreview(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceArtifact(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("seed artifact: %d %s", ingestRec.Code, ingestRec.Body.String())
	}

	queryReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/evidence/artifacts/artifact_handler_001", nil)
	queryReq.Header.Set("x-tenant-id", "tenant_demo")
	queryReq.Header.Set("x-account-id", "other-account")
	queryRec := httptest.NewRecorder()
	handler.GetEvidenceArtifact(queryRec, queryReq)

	if queryRec.Code != http.StatusNotFound || strings.Contains(queryRec.Body.String(), "用户原始问题") {
		t.Fatalf("unauthorized artifact must look absent without preview leak, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestEvidenceHandlerRejectsSecretBearingArtifact(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	body := strings.Replace(validHandlerArtifact(), `"text":"用户原始问题"`, `"Authorization":"Bearer secret"`, 1)
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceArtifact(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ARTIFACT_SECRET_FORBIDDEN") || strings.Contains(rec.Body.String(), "Bearer secret") {
		t.Fatalf("expected non-leaking secret rejection, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsConflictForArtifactIDReuse(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	firstReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	firstRec := httptest.NewRecorder()
	handler.IngestEvidenceArtifact(firstRec, firstReq)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("seed artifact: %d %s", firstRec.Code, firstRec.Body.String())
	}
	conflicting := strings.Replace(validHandlerArtifact(), "用户原始问题", "不同问题", 1)
	secondReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(conflicting))
	secondRec := httptest.NewRecorder()

	handler.IngestEvidenceArtifact(secondRec, secondReq)

	if secondRec.Code != http.StatusConflict || !strings.Contains(secondRec.Body.String(), "BKN_TRACE_ARTIFACT_ID_CONFLICT") {
		t.Fatalf("expected artifact conflict, got %d: %s", secondRec.Code, secondRec.Body.String())
	}
}

func TestEvidenceHandlerListsBusinessRequestsAndRequestDetail(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	artifactReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	artifactRec := httptest.NewRecorder()
	handler.IngestEvidenceArtifact(artifactRec, artifactReq)
	if artifactRec.Code != http.StatusCreated {
		t.Fatalf("seed artifact: %d %s", artifactRec.Code, artifactRec.Body.String())
	}
	eventReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerArtifactEventBatch()))
	eventRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(eventRec, eventReq)
	if eventRec.Code != http.StatusAccepted {
		t.Fatalf("seed artifact-linked event: %d %s", eventRec.Code, eventRec.Body.String())
	}

	listReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests?keyword=原始问题&limit=10", nil)
	listReq.Header.Set("x-tenant-id", "tenant_demo")
	listRec := httptest.NewRecorder()
	handler.ListRequests(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"request_id":"req_artifact_handler"`) ||
		!strings.Contains(listRec.Body.String(), "用户原始问题") {
		t.Fatalf("expected business request list, got %d: %s", listRec.Code, listRec.Body.String())
	}

	detailReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests/req_artifact_handler", nil)
	detailReq.Header.Set("x-tenant-id", "tenant_demo")
	detailRec := httptest.NewRecorder()
	handler.GetRequestSummary(detailRec, detailReq)
	if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), "用户原始问题") {
		t.Fatalf("expected request detail, got %d: %s", detailRec.Code, detailRec.Body.String())
	}
}

func TestEvidenceHandlerListsRequestTracesAndTechnicalExecutions(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("seed evidence: %d %s", ingestRec.Code, ingestRec.Body.String())
	}

	requestTracesReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests/req_handler_001/traces", nil)
	requestTracesRec := httptest.NewRecorder()
	handler.ListRequestTraces(requestTracesRec, requestTracesReq)
	if requestTracesRec.Code != http.StatusOK || !strings.Contains(requestTracesRec.Body.String(), `"request_id":"req_handler_001"`) ||
		!strings.Contains(requestTracesRec.Body.String(), `"trace_id":"9c0d0000000000000000000000000001"`) {
		t.Fatalf("expected request traces, got %d: %s", requestTracesRec.Code, requestTracesRec.Body.String())
	}

	executionsReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/trace-executions?status=running", nil)
	executionsRec := httptest.NewRecorder()
	handler.ListTraceExecutions(executionsRec, executionsReq)
	if executionsRec.Code != http.StatusOK || !strings.Contains(executionsRec.Body.String(), `"trace_id":"9c0d0000000000000000000000000001"`) {
		t.Fatalf("expected technical execution list, got %d: %s", executionsRec.Code, executionsRec.Body.String())
	}
}

func TestEvidenceHandlerRejectsInvalidSummaryPaginationAndTime(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	for _, target := range []string{
		"/api/agent-observability/v1/requests?limit=201",
		"/api/agent-observability/v1/requests?from=not-a-time",
		"/api/agent-observability/v1/trace-executions?cursor=not-a-cursor",
	} {
		req := authenticatedQueryRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		if strings.Contains(target, "trace-executions") {
			handler.ListTraceExecutions(rec, req)
		} else {
			handler.ListRequests(rec, req)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid summary query rejection for %s, got %d: %s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestNewAPIErrorResponseIncludesStandardTraceFieldsAndCompatibilityCode(t *testing.T) {
	const traceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests?limit=201", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-bbbbbbbbbbbbbbbb-01")
	rec := httptest.NewRecorder()

	handler.ListRequests(rec, req)

	if rec.Code != http.StatusBadRequest || rec.Header().Get("x-trace-id") != traceID {
		t.Fatalf("standard trace response header is required: status=%d headers=%+v", rec.Code, rec.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error_code"] != "INVALID_ARGUMENT" || body["trace_id"] != traceID ||
		body["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("error must expose standard fields and legacy code: %s", rec.Body.String())
	}
}

func TestNewAPISuccessResponseIncludesPropagatedTraceHeader(t *testing.T) {
	const traceID = "cccccccccccccccccccccccccccccccc"
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-dddddddddddddddd-01")
	rec := httptest.NewRecorder()

	handler.ListRequests(rec, req)

	if rec.Code != http.StatusOK || rec.Header().Get("x-trace-id") != traceID {
		t.Fatalf("success response must propagate trace id: status=%d headers=%+v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestArtifactIDPathUsesSameSafeFormatAsArtifactIngest(t *testing.T) {
	if got := artifactIDFromPath("/api/agent-observability/v1/evidence/artifacts/artifact_valid-01"); got != "artifact_valid-01" {
		t.Fatalf("valid artifact id must remain queryable: %q", got)
	}
	for _, path := range []string{
		"/api/agent-observability/v1/evidence/artifacts/artifact%2Fchild",
		"/api/agent-observability/v1/evidence/artifacts/artifact%20child",
		"/api/agent-observability/v1/evidence/artifacts/%09artifact",
	} {
		if got := artifactIDFromPath(path); got != "" {
			t.Fatalf("unsafe encoded artifact path must be rejected: path=%q id=%q", path, got)
		}
	}
}

func TestInteractionSummaryPathRejectsNestedOrEmptyIDs(t *testing.T) {
	if got := interactionIDFromSummaryPath("/api/agent-observability/v1/interactions/int_supply_chain"); got != "int_supply_chain" {
		t.Fatalf("unexpected interaction id: %q", got)
	}
	for _, path := range []string{
		"/api/agent-observability/v1/interactions/",
		"/api/agent-observability/v1/interactions/int%2Fchild",
	} {
		if got := interactionIDFromSummaryPath(path); got != "" {
			t.Fatalf("unsafe interaction path must be rejected: path=%q id=%q", path, got)
		}
	}
}

func validHandlerArtifact() string {
	return `{
	  "artifact_id": "artifact_handler_001",
	  "artifact_type": "question",
	  "bkn.request.id": "req_artifact_handler",
	  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
	  "interaction_id": "interaction_handler",
	  "operation_id": "operation_handler",
	  "source_ref": "interaction:interaction_handler",
	  "content_type": "application/json",
	  "schema_version": "2.2.0",
	  "observed_at": "2026-07-26T08:00:00Z",
	  "content": {"text":"用户原始问题"},
	  "bkn.tenant.id": "tenant_demo",
	  "business_domain": "bd_demo",
	  "bkn.account.id": "acct_demo",
	  "bkn.account.type": "app"
	}`
}

func validHandlerArtifactEventBatch() string {
	return `{
	  "bkn.trace.schema.version": "2.2.0",
	  "trace": {
	    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
	    "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-2f12000000000003-01",
	    "bkn.request.id": "req_artifact_handler",
	    "bkn.tenant.id": "tenant_demo",
	    "business_domain": "bd_demo",
	    "bkn.account.id": "acct_demo",
	    "bkn.account.type": "app"
	  },
	  "events": [{
	    "event_id": "evt_artifact_question",
	    "event_type": "agent.interaction.started",
	    "bkn.trace.schema.version": "2.2.0",
	    "observed_at": "2026-07-26T08:00:00.000000000Z",
	    "emitted_at": "2026-07-26T08:00:00.001000000Z",
	    "producer_module": "bkn-agent",
	    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
	    "span_id": "2f12000000000003",
	    "bkn.request.id": "req_artifact_handler",
	    "bkn.operation.name": "agent.run",
	    "interaction_id": "interaction_handler",
	    "attempt": 1,
	    "payload": {
	      "intent_hash": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	      "mode": "task",
	      "agent_id": "agent-handler",
	      "question_artifact_ref": "artifact:artifact_handler_001"
	    }
	  }]
	}`
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
            "ref_id": "object:kn_demo:customer",
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

func unauthorizedHandlerBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "9c0d0000000000000000000000000003",
    "bkn.request.id": "req_handler_003",
    "traceparent": "00-9c0d0000000000000000000000000003-2f12000000000003-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim_authz",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "9c0d0000000000000000000000000003",
      "span_id": "2f12000000000003",
      "bkn.request.id": "req_handler_003",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_handler_authz",
        "claim_type": "answer",
        "claim_hash": "sha256:claim-authz",
        "visibility": "visible",
        "version_status": "versioned"
      }
    },
    {
      "event_id": "evt_evidence_authz",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.002000000Z",
      "emitted_at": "2026-07-22T04:00:00.003000000Z",
      "producer_module": "bkn-trace",
      "trace_id": "9c0d0000000000000000000000000003",
      "span_id": "2f12000000000003",
      "bkn.request.id": "req_handler_003",
      "bkn.operation.name": "bkn_trace.resolve_evidence_refs",
      "payload": {
        "claim_id": "claim_handler_authz",
        "evidence_refs": [
          {
            "ref_id": "row:visible",
            "ref_type": "row_ref",
            "visibility": "visible"
          },
          {
            "ref_id": "row:unauthorized",
            "ref_type": "row_ref",
            "visibility": "unauthorized",
            "policy_decision_ref": "policy:deny:handler",
            "redaction_reason": "row_scope_denied"
          }
        ]
      }
    }
  ]
}`
}
