package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

type authCapturingLogSource struct {
	authorization string
}

func (source *authCapturingLogSource) ID() string { return "auth-source" }
func (source *authCapturingLogSource) Search(ctx context.Context, _ observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	source.authorization = observabilityvo.SourceAuthorization(ctx)
	return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
}

func (source handlerLogSource) ID() string { return "otel" }

func (source handlerLogSource) Search(
	context.Context,
	observabilityvo.LogQuery,
) (observabilityvo.SourcePage, error) {
	return observabilityvo.SourcePage{Records: source.records, CountAccuracy: "exact"}, nil
}

func (source handlerLogSource) Get(_ context.Context, logID string) (observabilityvo.LogRecord, bool, error) {
	for _, record := range source.records {
		if record.LogID == logID {
			return record, true, nil
		}
	}
	return observabilityvo.LogRecord{}, false, nil
}

func (source handlerLogSource) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: "otel", Status: "healthy", Reliability: "best_effort",
		CollectionMethod: "direct_otlp", CoveredModules: []string{"context-loader"},
		CountAccuracy: "exact", Categories: []string{observabilityvo.CategoryRuntimeBusiness},
	}
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
		EventTimestamp: time.Now().UTC(), ObservedTimestamp: time.Now().UTC(),
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

func TestLogHandlerReturnsAuthorizedDetailAndHidesUnauthorizedDetail(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "user-a",
		Roles: []string{"normal_user"}, AccountActive: true, TenantActive: true,
	}
	handler := newTestLogHandler(profile, []observabilityvo.LogRecord{
		{LogID: "owned", Category: observabilityvo.CategoryRuntimeBusiness, TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "user-a", TraceID: "trace-a"},
		{LogID: "other", Category: observabilityvo.CategoryRuntimeBusiness, TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "user-b", TraceID: "trace-a"},
	})

	ownedRequest := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs/owned", nil)
	setLogTestIdentity(ownedRequest, "user-a")
	ownedResponse := httptest.NewRecorder()
	handler.GetLog(ownedResponse, ownedRequest)
	if ownedResponse.Code != http.StatusOK || !containsJSONLogID(ownedResponse.Body.Bytes(), "owned") {
		t.Fatalf("expected owned detail, got %d: %s", ownedResponse.Code, ownedResponse.Body.String())
	}

	otherRequest := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs/other", nil)
	setLogTestIdentity(otherRequest, "user-a")
	otherResponse := httptest.NewRecorder()
	handler.GetLog(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusNotFound || !containsJSONCode(otherResponse.Body.Bytes(), "log_not_disclosed") {
		t.Fatalf("unauthorized detail must be undisclosed, got %d: %s", otherResponse.Code, otherResponse.Body.String())
	}
}

func TestLogHandlerReturnsAuthorizedFacetsSourcesAndPolicies(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "admin-a",
		Roles: []string{"admin"}, AccountActive: true, TenantActive: true,
	}
	handler := newTestLogHandler(profile, []observabilityvo.LogRecord{{
		LogID: "system-a", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "admin-a",
		EventTimestamp: time.Now().UTC(),
	}})

	tests := []struct {
		name   string
		path   string
		call   func(http.ResponseWriter, *http.Request)
		assert func([]byte) bool
	}{
		{name: "facets", path: "/api/observability/v1/log-facets?facet=event_name", call: handler.GetLogFacets, assert: func(body []byte) bool { return containsJSONFacet(body, "service.started", 1) }},
		{name: "sources", path: "/api/observability/v1/log-sources", call: handler.ListLogSources, assert: func(body []byte) bool { return containsJSONSource(body, "otel") }},
		{name: "policies", path: "/api/observability/v1/log-policies", call: handler.ListLogPolicies, assert: func(body []byte) bool { return containsJSONPolicy(body, observabilityvo.CategoryRuntimeSystem) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedQueryRequest(http.MethodGet, test.path, nil)
			setLogTestIdentity(request, "admin-a")
			response := httptest.NewRecorder()
			test.call(response, request)
			if response.Code != http.StatusOK || !test.assert(response.Body.Bytes()) {
				t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestLogFacetRejectsUnsupportedFacet(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "admin-a",
		Roles: []string{"admin"}, AccountActive: true, TenantActive: true,
	}
	handler := newTestLogHandler(profile, nil)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/log-facets?facet=tenant_id", nil)
	setLogTestIdentity(request, "admin-a")
	response := httptest.NewRecorder()

	handler.GetLogFacets(response, request)

	if response.Code != http.StatusBadRequest || !containsJSONCode(response.Body.Bytes(), "invalid_log_filter") {
		t.Fatalf("unsupported facet must be rejected, got %d: %s", response.Code, response.Body.String())
	}
}

func TestParseLogQueryAcceptsRFC3339TimeRangeAndRejectsReverseRange(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/observability/v1/logs?time_from=2026-08-01T10:00:00Z&time_to=2026-08-01T11:00:00Z", nil)
	query, err := parseLogQuery(request)
	if err != nil || query.TimeFrom == nil || query.TimeTo == nil || !query.TimeFrom.Before(*query.TimeTo) {
		t.Fatalf("valid time range was not parsed: query=%+v err=%v", query, err)
	}

	reversed := httptest.NewRequest(http.MethodGet, "/api/observability/v1/logs?time_from=2026-08-01T12:00:00Z&time_to=2026-08-01T11:00:00Z", nil)
	if _, err := parseLogQuery(reversed); err == nil {
		t.Fatal("reverse time range must be rejected")
	}
}

func TestParseLogQueryRejectsUnregisteredAndMalformedFilters(t *testing.T) {
	paths := []string{
		"/api/observability/v1/logs?categories=plugin.custom",
		"/api/observability/v1/logs?event_names=plugin.custom.event",
		"/api/observability/v1/logs?trace_id=trace-a",
		"/api/observability/v1/logs?span_id=span-a",
		"/api/observability/v1/logs?q=" + strings.Repeat("x", 513),
	}
	for _, path := range paths {
		if _, err := parseLogQuery(httptest.NewRequest(http.MethodGet, path, nil)); err == nil {
			t.Fatalf("invalid filter was accepted: %s", path)
		}
	}
}

func TestLogHandlerReturnsCursorInvalidAndCursorStaleContracts(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "admin-a",
		Roles: []string{"admin"}, AccountActive: true, TenantActive: true, Fingerprint: "sha256:scope-a",
	}
	base := time.Now().UTC().Truncate(time.Second)
	handler := newTestLogHandler(profile, []observabilityvo.LogRecord{
		{LogID: "log-3", Category: observabilityvo.CategoryRuntimeSystem, TenantID: "tenant-a", EventTimestamp: base},
		{LogID: "log-2", Category: observabilityvo.CategoryRuntimeSystem, TenantID: "tenant-a", EventTimestamp: base.Add(-time.Second)},
		{LogID: "log-1", Category: observabilityvo.CategoryRuntimeSystem, TenantID: "tenant-a", EventTimestamp: base.Add(-2 * time.Second)},
	})
	firstRequest := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?limit=2", nil)
	setLogTestIdentity(firstRequest, "admin-a")
	firstResponse := httptest.NewRecorder()
	handler.ListLogs(firstResponse, firstRequest)
	var firstBody struct {
		NextCursor string `json:"next_cursor"`
	}
	if firstResponse.Code != http.StatusOK || json.Unmarshal(firstResponse.Body.Bytes(), &firstBody) != nil || firstBody.NextCursor == "" {
		t.Fatalf("first page did not return a cursor: %d %s", firstResponse.Code, firstResponse.Body.String())
	}

	invalidRequest := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?limit=2&cursor=invalid", nil)
	setLogTestIdentity(invalidRequest, "admin-a")
	invalidResponse := httptest.NewRecorder()
	handler.ListLogs(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest || !containsJSONCode(invalidResponse.Body.Bytes(), "cursor_invalid") {
		t.Fatalf("invalid cursor contract mismatch: %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	changedFilterRequest := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?limit=2&failed_only=true&cursor="+firstBody.NextCursor, nil)
	setLogTestIdentity(changedFilterRequest, "admin-a")
	changedFilterResponse := httptest.NewRecorder()
	handler.ListLogs(changedFilterResponse, changedFilterRequest)
	if changedFilterResponse.Code != http.StatusConflict || !containsJSONCode(changedFilterResponse.Body.Bytes(), "cursor_stale") {
		t.Fatalf("stale cursor contract mismatch: %d %s", changedFilterResponse.Code, changedFilterResponse.Body.String())
	}
}

func TestLogHandlerUsesCanonicalObservabilityErrorEnvelope(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "admin-a",
		Roles: []string{"admin"}, AccountActive: true, TenantActive: true,
	}
	handler := newTestLogHandler(profile, nil)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?trace_id=invalid", nil)
	request.Header.Set("X-Request-ID", "req-error-contract")
	response := httptest.NewRecorder()

	handler.ListLogs(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "invalid_log_filter" || body.Error.Message == "" || body.Error.Retryable || body.Error.RequestID != "req-error-contract" {
		t.Fatalf("unexpected observability error envelope: %s", response.Body.String())
	}
}

func TestLogHandlerForwardsCallerAuthorizationToSourceAdapters(t *testing.T) {
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "audit-a",
		Roles: []string{"audit"}, AccountActive: true, TenantActive: true,
	}
	source := &authCapturingLogSource{}
	resolver := &fakeAccessScopeResolver{profile: profile}
	authorizer := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver,
	})
	handler := NewLogHandler(logsvc.New([]logsvc.Source{source}), authorizer)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs", nil)
	setLogTestIdentity(request, "audit-a")
	request.Header.Set("Authorization", "Bearer audit-token")
	response := httptest.NewRecorder()

	handler.ListLogs(response, request)

	if response.Code != http.StatusOK || source.authorization != "Bearer audit-token" {
		t.Fatalf("caller authorization was not forwarded: status=%d authorization=%q", response.Code, source.authorization)
	}
}

func newTestLogHandler(profile evidencevo.AccessProfile, records []observabilityvo.LogRecord) *LogHandler {
	for index := range records {
		if records[index].SchemaVersion == "" {
			records[index].SchemaVersion = "1.0.0"
		}
		if records[index].SourceID == "" {
			records[index].SourceID = "test-source"
		}
		if records[index].SourceLogID == "" {
			records[index].SourceLogID = records[index].LogID
		}
		if records[index].EventTimestamp.IsZero() {
			records[index].EventTimestamp = time.Now().UTC()
		}
		if records[index].ObservedTimestamp.IsZero() {
			records[index].ObservedTimestamp = records[index].EventTimestamp
		}
		if records[index].SeverityNumber == 0 {
			records[index].SeverityNumber, records[index].SeverityText = 9, "INFO"
		}
		if records[index].Outcome == "" {
			records[index].Outcome = "success"
		}
		if records[index].ServiceName == "" {
			records[index].ServiceName = "test-service"
		}
		if records[index].Environment == "" {
			records[index].Environment = "test"
		}
		if records[index].EventName == "" {
			switch records[index].Category {
			case observabilityvo.CategoryRuntimeBusiness:
				records[index].EventName = "knowledge.read.completed"
			case observabilityvo.CategoryRuntimeModel:
				records[index].EventName = "model.inference.completed"
			default:
				records[index].EventName = "service.started"
			}
		}
		if records[index].TrustLevel == "" {
			records[index].TrustLevel = "trusted"
		}
		if records[index].IngressPrincipal == "" {
			records[index].IngressPrincipal = "test-gateway"
		}
	}
	resolver := &fakeAccessScopeResolver{profile: profile}
	evidenceHandler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver,
	})
	return NewLogHandler(logsvc.New([]logsvc.Source{handlerLogSource{records: records}}), evidenceHandler)
}

func containsJSONCode(payload []byte, expected string) bool {
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil {
		return false
	}
	if body["code"] == expected || body["error_code"] == expected {
		return true
	}
	errorBody, _ := body["error"].(map[string]any)
	return errorBody["code"] == expected
}

func setLogTestIdentity(request *http.Request, subject string) {
	request.Header.Set("x-account-id", subject)
	request.Header.Set("x-account-type", "user")
	request.Header.Set("x-tenant-id", "tenant-a")
	request.Header.Set("x-business-domain", "domain-a")
}

func containsJSONLogID(payload []byte, expected string) bool {
	var body struct {
		Data observabilityvo.LogRecord `json:"data"`
	}
	return json.Unmarshal(payload, &body) == nil && body.Data.LogID == expected
}

func containsJSONFacet(payload []byte, expected string, count int64) bool {
	var body struct {
		Data []observabilityvo.FacetValue `json:"data"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return false
	}
	for _, value := range body.Data {
		if value.Value == expected && value.Count == count {
			return true
		}
	}
	return false
}

func containsJSONSource(payload []byte, expected string) bool {
	var body struct {
		Data []observabilityvo.SourceStatus `json:"data"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return false
	}
	for _, source := range body.Data {
		if source.SourceID == expected {
			return true
		}
	}
	return false
}

func containsJSONPolicy(payload []byte, expected string) bool {
	var body struct {
		Data []observabilityvo.LogPolicy `json:"data"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return false
	}
	for _, policy := range body.Data {
		if policy.Category == expected {
			return true
		}
	}
	return false
}
