package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

type handlerLogSource struct {
	records []observabilityvo.LogRecord
}

func (source handlerLogSource) ID() string { return "otel" }

func (source handlerLogSource) Search(
	context.Context,
	observabilityvo.LogQuery,
) (observabilityvo.SourcePage, error) {
	return observabilityvo.SourcePage{Records: source.records, CountAccuracy: "exact"}, nil
}

func TestLogHandlerReturnsTheR62ListEnvelope(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", ActorID: "builder-a", EffectiveSubjectID: "builder-a",
		Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-a"},
		AccountActive: true, TenantActive: true, Fingerprint: "sha256:profile-a",
	}
	handler := newTestLogHandler(profile, []observabilityvo.LogRecord{{
		SchemaVersion: "1.0.0", LogID: "log-a", SourceID: "otel", SourceLogID: "source-a",
		Category: observabilityvo.CategoryRuntimeBusiness, EventName: "knowledge.read.completed",
		EventTimestamp: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), ObservedTimestamp: time.Date(2026, 8, 1, 10, 0, 1, 0, time.UTC),
		SeverityNumber: 9, SeverityText: "INFO", Outcome: "success", SafeSummary: "读取需求预测对象",
		ServiceName: "context-loader", Environment: "local", TenantID: "tenant-a", BusinessDomain: "domain-a",
		EffectiveSubjectID: "other-a", IngressPrincipal: "otel-collector", TrustLevel: "trusted",
		KnowledgeNetworkIDs: []string{"kn-a"}, TraceID: "4b3d59daeff5bfbb23d46c47a5051ec9",
	}})
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?categories=runtime.business&limit=20", nil)
	request.Header.Set("x-account-id", "builder-a")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("x-tenant-id", "tenant-a")
	request.Header.Set("x-business-domain", "domain-a")
	response := httptest.NewRecorder()

	handler.ListLogs(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected log list, got %d: %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["partial"] != false || body["data"] == nil || body["source_status"] == nil || body["count"] == nil ||
		body["request_trace_context"] == nil {
		t.Fatalf("missing R6.2 envelope fields: %#v", body)
	}
	data := body["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["log_id"] != "log-a" {
		t.Fatalf("unexpected log projection: %#v", body)
	}
}

func TestLogHandlerReturnsStableAccessDeniedForNormalUserGlobalSearch(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", ActorID: "user-a", EffectiveSubjectID: "user-a",
		Roles: []string{"normal_user"}, AccountActive: true, TenantActive: true,
	}
	handler := newTestLogHandler(profile, nil)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs", nil)
	request.Header.Set("x-account-id", "user-a")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("x-tenant-id", "tenant-a")
	request.Header.Set("x-business-domain", "domain-a")
	response := httptest.NewRecorder()

	handler.ListLogs(response, request)

	if response.Code != http.StatusForbidden || !containsJSONCode(response.Body.Bytes(), "observability_access_denied") {
		t.Fatalf("expected stable access denial, got %d: %s", response.Code, response.Body.String())
	}
}

func newTestLogHandler(profile evidencevo.AccessProfile, records []observabilityvo.LogRecord) *LogHandler {
	resolver := &fakeAccessScopeResolver{profile: profile}
	evidenceHandler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver,
	})
	return NewLogHandler(logsvc.New([]logsvc.Source{handlerLogSource{records: records}}), evidenceHandler)
}

func containsJSONCode(payload []byte, expected string) bool {
	var body map[string]any
	return json.Unmarshal(payload, &body) == nil && (body["code"] == expected || body["error_code"] == expected)
}
