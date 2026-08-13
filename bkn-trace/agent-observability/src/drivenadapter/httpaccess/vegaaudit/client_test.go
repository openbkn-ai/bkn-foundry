package vegaaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (fn roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestClientSearchProjectsVegaManagementAudit(t *testing.T) {
	client := New("http://vega", &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer token" || request.Header.Get("x-tenant-id") != "tenant-a" || request.Header.Get("x-business-domain") != "domain-a" {
			t.Fatalf("source request did not retain trusted caller context")
		}
		query := request.URL.Query()
		if query.Get("actor_id") != "user-1" || query.Get("action") != "create" || query.Get("target_type") != "catalog" || query.Get("target_id") != "catalog-1" || query.Get("outcome") != "success" {
			t.Fatalf("filters were not forwarded: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"entries":[{"event_id":"evt-1","event_time":"2026-08-13T08:00:00Z","recorded_at":"2026-08-13T08:00:01Z","tenant_id":"tenant-a","business_domain_id":"domain-a","actor_id":"user-1","actor_name":"管理员","actor_type":"user","auth_method":"oauth","request_id":"req-1","source_channel":"api","method":"POST","action":"create","target_type":"catalog","target_id":"catalog-1","target_name":"供应链数据源","outcome":"success","schema_version":"1.0"}]}`)), Header: make(http.Header)}, nil
	})})
	from := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token")
	page, err := client.Search(ctx, observabilityvo.LogQuery{AuthorizedCategories: []string{observabilityvo.CategoryAuditAdmin}, AuthorizedTenantID: "tenant-a", AuthorizedBusinessDomain: "domain-a", TimeFrom: &from, TimeTo: &to, ActorID: "user-1", Action: "create", TargetType: "catalog", TargetID: "catalog-1", Outcomes: []string{"success"}})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(page.Records))
	}
	record := page.Records[0]
	if record.BusinessModule != "data_resource_knowledge_network" || record.ActorNameSnapshot != "管理员" || record.TargetNameSnapshot != "供应链数据源" || record.LogID != "vega:evt-1" {
		t.Fatalf("unexpected projected record: %+v", record)
	}
}
