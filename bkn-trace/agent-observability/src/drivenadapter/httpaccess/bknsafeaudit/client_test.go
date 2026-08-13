package bknsafeaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestSearchForwardsCallerAuthorizationFiltersAtSourceAndDropsDetail(t *testing.T) {
	var request *http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(candidate *http.Request) (*http.Response, error) {
		request = candidate
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"logs":[{"id":"audit-a","actor_id":"admin-a","actor_name_snapshot":"Administrator","actor_type":"user","auth_method":"unknown","request_id":"req-a","source_channel":"api","method":"DELETE","resource":"users","action":"users","target_id":"user-a","target_name":"User A","detail":"{\"password\":\"must-not-pass\"}","status":404,"client_ip":"10.0.0.1","created_at":"2026-08-01T10:00:00Z"}],"total":1}`)),
			Header:     make(http.Header), Request: candidate,
		}, nil
	})}
	client := New("http://bkn-safe:3000", httpClient)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token-a")
	from := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	page, err := client.Search(ctx, observabilityvo.LogQuery{
		ActorID: "admin-a", Action: "users", TargetType: "users", TargetID: "user-a",
		Outcomes: []string{"failure"}, TimeFrom: &from, Limit: 20,
		AuthorizedTenantID: "tenant-a", AuthorizedCategories: []string{observabilityvo.CategoryAuditAdmin},
	})
	if err != nil {
		t.Fatalf("search audit source: %v", err)
	}
	if request.Header.Get("Authorization") != "Bearer token-a" || request.URL.Query().Get("failed_only") != "true" ||
		request.URL.Query().Get("actor_id") != "admin-a" || request.URL.Query().Get("resource") != "users" ||
		request.URL.Query().Get("action") != "users" || request.URL.Query().Get("target_id") != "user-a" {
		t.Fatalf("source filters or caller authorization missing: %s headers=%v", request.URL.String(), request.Header)
	}
	if len(page.Records) != 1 {
		t.Fatalf("expected one projected record: %+v", page)
	}
	record := page.Records[0]
	if record.SourceID != "bkn-safe-admin" || record.Category != observabilityvo.CategoryAuditAdmin || record.EventName != "resource_config.changed" || record.Outcome != "failure" {
		t.Fatalf("unexpected audit projection: %+v", record)
	}
	if record.EventID != "audit-a" || record.BusinessModule != "system_management" || record.Action != "users" ||
		record.TargetType != "users" || record.TargetID != "user-a" || record.TargetNameSnapshot != "User A" ||
		record.ActorNameSnapshot != "Administrator" || record.ActorType != "user" || record.AuthMethod != "unknown" ||
		record.RequestID != "req-a" || record.SourceChannel != "api" || !record.EventTime.Equal(record.EventTimestamp) || !record.RecordedAt.Equal(record.ObservedTimestamp) {
		t.Fatalf("operation audit business projection is incomplete: %+v", record)
	}
	if record.Attributes["method"] != "DELETE" || record.Attributes["status_code"] != 404 ||
		record.Attributes["client_ip"] != "10.0.0.1" {
		t.Fatalf("management audit facts were not preserved: %+v", record.Attributes)
	}
	if strings.Contains(record.SafeSummary, "password") || strings.Contains(record.SafeSummary, "must-not-pass") ||
		strings.Contains(record.FailureMessage, "must-not-pass") {
		t.Fatalf("raw BKN Safe detail leaked through projection: %+v", record)
	}
}

func TestSearchRequiresForwardedCallerAuthorization(t *testing.T) {
	client := New("http://bkn-safe:3000", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request must not be sent without caller authorization")
		return nil, nil
	})})
	_, err := client.Search(context.Background(), observabilityvo.LogQuery{
		AuthorizedTenantID: "tenant-a", AuthorizedCategories: []string{observabilityvo.CategoryAuditAdmin},
	})
	if err == nil {
		t.Fatal("missing source authorization must fail")
	}
}

func TestSearchPreservesNanosecondKeysetBoundary(t *testing.T) {
	var request *http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(candidate *http.Request) (*http.Response, error) {
		request = candidate
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"logs":[],"total":0}`)), Header: make(http.Header), Request: candidate}, nil
	})}
	client := New("http://bkn-safe:3000", httpClient)
	boundary := time.Date(2026, 8, 1, 10, 0, 0, 123456789, time.UTC)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token-a")

	_, err := client.Search(ctx, observabilityvo.LogQuery{
		AuthorizedCategories: []string{observabilityvo.CategoryAuditAdmin},
		PageBefore:           &observabilityvo.SourcePosition{EventTimestamp: boundary, LogID: "audit-boundary"},
	})
	if err != nil {
		t.Fatalf("search audit source: %v", err)
	}
	if got := request.URL.Query().Get("to"); got != "2026-08-01T10:00:00.123456789Z" {
		t.Fatalf("keyset timestamp lost precision: %q", got)
	}
	if got := request.URL.Query().Get("before_id"); got != "audit-boundary" {
		t.Fatalf("same-timestamp tiebreaker missing: %q", got)
	}
}

func TestGetProjectsOneAuditRecordAndNeverExposesRawDetail(t *testing.T) {
	var request *http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(candidate *http.Request) (*http.Response, error) {
		request = candidate
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"audit-a","actor_id":"admin-a","method":"PUT","resource":"roles","action":"roles","target_id":"role-a","target_name":"Planner","detail":"{\"password\":\"must-not-pass\"}","status":200,"created_at":"2026-08-01T10:00:00Z"}`)),
			Header:     make(http.Header), Request: candidate,
		}, nil
	})}
	client := New("http://bkn-safe:3000", httpClient)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token-a")
	record, found, err := client.Get(ctx, "audit-a")
	if err != nil || !found {
		t.Fatalf("get audit source: found=%v err=%v", found, err)
	}
	if request.URL.Path != "/api/safe/v1/admin/audit-logs/audit-a" || request.Header.Get("Authorization") != "Bearer token-a" {
		t.Fatalf("unexpected detail request: %s headers=%v", request.URL.String(), request.Header)
	}
	if record.LogID != "bkn-safe-admin:audit-a" || record.EventName != "role.updated" || strings.Contains(record.SafeSummary, "must-not-pass") {
		t.Fatalf("unexpected safe detail projection: %+v", record)
	}
	if record.ActorNameSnapshot != "admin-a" || record.ActorType != "user" || record.AuthMethod != "unknown" || record.SourceChannel != "api" {
		t.Fatalf("legacy audit facts must use deterministic fallbacks: %+v", record)
	}
}

func TestProjectAuditLogUsesStableOperationOutcomes(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{status: http.StatusAccepted, want: "success"},
		{status: http.StatusMultiStatus, want: "success"},
		{status: http.StatusForbidden, want: "denied"},
		{status: http.StatusInternalServerError, want: "failure"},
		{status: http.StatusNoContent, want: "success"},
	}
	for _, test := range tests {
		record := projectAuditLog(auditLog{ID: "audit-a", Status: test.status}, "tenant-a")
		if record.Outcome != test.want {
			t.Errorf("status %d projected as %q, want %q", test.status, record.Outcome, test.want)
		}
	}
}

func TestProjectAuditLogNormalizesOnlyKnownLegacyRouteActions(t *testing.T) {
	tests := []struct {
		method, resource, action, want string
	}{
		{method: http.MethodPost, resource: "role-bindings", action: "role-bindings", want: "bind_role"},
		{method: http.MethodDelete, resource: "role-bindings", action: "role-bindings", want: "unbind_role"},
		{method: http.MethodPost, resource: "object-grants", action: "object-grants", want: "grant"},
		{method: http.MethodDelete, resource: "object-grants", action: "object-grants", want: "revoke"},
		{method: http.MethodPost, resource: "object-grants", action: "grant", want: "grant"},
	}
	for _, test := range tests {
		record := projectAuditLog(auditLog{Method: test.method, Resource: test.resource, Action: test.action}, "tenant-a")
		if record.Action != test.want {
			t.Errorf("%s %s action %q projected as %q, want %q", test.method, test.resource, test.action, record.Action, test.want)
		}
	}
}

func TestBusinessModuleForManagementResourcesMatchesProductModules(t *testing.T) {
	tests := map[string]string{
		"users":         "system_management",
		"departments":   "system_management",
		"role-bindings": "system_management",
		"object-grants": "system_management",
		"api-keys":      "system_management",
		"license":       "system_management",
		"clients":       "system_management",
		"profile":       "system_management",
	}
	for resource, want := range tests {
		if got := businessModuleForResource(resource); got != want {
			t.Errorf("resource %q: module=%q, want %q", resource, got, want)
		}
	}
}
