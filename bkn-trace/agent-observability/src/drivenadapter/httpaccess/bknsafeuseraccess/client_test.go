package bknsafeuseraccess

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

func TestSearchProjectsBKNsafeUserAccessFacts(t *testing.T) {
	var request *http.Request
	client := New("http://bkn-safe:3000", &http.Client{Transport: roundTripFunc(func(candidate *http.Request) (*http.Response, error) {
		request = candidate
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: candidate,
			Body: io.NopCloser(strings.NewReader(`{"logs":[{"id":"access-a","actor_id":"user-a","actor_name_snapshot":"Alice","auth_method":"password","source_channel":"web","action":"login","outcome":"failure","failure_code":"invalid_credentials","request_id":"req-a","client_ip":"10.0.0.1","created_at":"2026-08-13T12:00:00Z"}],"total":1}`))}, nil
	})})
	from := time.Date(2026, 8, 13, 11, 0, 0, 0, time.UTC)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer audit-token")
	page, err := client.Search(ctx, observabilityvo.LogQuery{
		ActorID: "user-a", Action: "login", Outcomes: []string{"failure"}, TimeFrom: &from, Limit: 20,
		AuthorizedTenantID: "tenant-a", AuthorizedCategories: []string{observabilityvo.CategoryAccessUser},
	})
	if err != nil {
		t.Fatalf("search BKN Safe access: %v", err)
	}
	if request.URL.Path != "/api/safe/v1/admin/access-logs" || request.Header.Get("Authorization") != "Bearer audit-token" ||
		request.URL.Query().Get("actor_id") != "user-a" || request.URL.Query().Get("action") != "login" || request.URL.Query().Get("outcome") != "failure" {
		t.Fatalf("access source request is incomplete: %s headers=%v", request.URL.String(), request.Header)
	}
	if len(page.Records) != 1 {
		t.Fatalf("records = %+v, want one", page)
	}
	record := page.Records[0]
	if record.LogID != "bkn-safe-access:access-a" || record.Category != observabilityvo.CategoryAccessUser ||
		record.EventName != "login.failed" || record.Outcome != "failure" || record.ActorNameSnapshot != "Alice" ||
		record.AuthMethod != "password" || record.TargetType != "user" || record.TargetID != "user-a" || record.FailureCode != "invalid_credentials" {
		t.Fatalf("access fact projection is incomplete: %+v", record)
	}
}
