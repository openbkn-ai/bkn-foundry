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
			Body:       io.NopCloser(strings.NewReader(`{"logs":[{"id":"audit-a","actor_id":"admin-a","method":"DELETE","resource":"users","action":"users","target_id":"user-a","target_name":"User A","detail":"{\"password\":\"must-not-pass\"}","status":404,"client_ip":"10.0.0.1","created_at":"2026-08-01T10:00:00Z"}],"total":1}`)),
			Header:     make(http.Header), Request: candidate,
		}, nil
	})}
	client := New("http://bkn-safe:3000", httpClient)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token-a")
	from := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	page, err := client.Search(ctx, observabilityvo.LogQuery{
		ActorID: "admin-a", ResourceType: "users", FailedOnly: true, TimeFrom: &from, Limit: 20,
		AuthorizedTenantID: "tenant-a", AuthorizedCategories: []string{observabilityvo.CategoryAuditAdmin},
	})
	if err != nil {
		t.Fatalf("search audit source: %v", err)
	}
	if request.Header.Get("Authorization") != "Bearer token-a" || request.URL.Query().Get("failed_only") != "true" || request.URL.Query().Get("actor_id") != "admin-a" || request.URL.Query().Get("resource") != "users" {
		t.Fatalf("source filters or caller authorization missing: %s headers=%v", request.URL.String(), request.Header)
	}
	if len(page.Records) != 1 {
		t.Fatalf("expected one projected record: %+v", page)
	}
	record := page.Records[0]
	if record.SourceID != "bkn-safe-admin" || record.Category != observabilityvo.CategoryAuditAdmin || record.EventName != "resource_config.changed" || record.Outcome != "failure" {
		t.Fatalf("unexpected audit projection: %+v", record)
	}
	if strings.Contains(record.SafeSummary, "password") || strings.Contains(record.SafeSummary, "must-not-pass") || len(record.Attributes) != 0 {
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
	if record.LogID != "audit-a" || record.EventName != "role.updated" || strings.Contains(record.SafeSummary, "must-not-pass") {
		t.Fatalf("unexpected safe detail projection: %+v", record)
	}
}
