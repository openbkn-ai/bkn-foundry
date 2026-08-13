package modelmanageraudit

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

func TestClientSearchProjectsModelManagementAudit(t *testing.T) {
	client := New("http://model-manager", &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/mf-model-manager/v1/operation-audits" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer token" || request.Header.Get("x-tenant-id") != "tenant-a" || request.Header.Get("x-business-domain") != "domain-a" {
			t.Fatalf("source request did not retain trusted caller context")
		}
		query := request.URL.Query()
		if query.Get("actor_id") != "user-1" || query.Get("action") != "set_default" || query.Get("target_type") != "llm_model" || query.Get("target_id") != "model-1" || query.Get("outcome") != "success" {
			t.Fatalf("filters were not forwarded: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"entries":[{"event_id":"evt-1","event_time":"2026-08-13T08:00:00Z","recorded_at":"2026-08-13T08:00:01Z","tenant_id":"tenant-a","business_domain_id":"domain-a","actor_id":"user-1","actor_name":"管理员","actor_type":"user","auth_method":"oauth","request_id":"req-1","source_channel":"api","method":"POST","action":"set_default","target_type":"llm_model","target_id":"model-1","target_name":"默认模型","outcome":"success"}]}`)), Header: make(http.Header)}, nil
	})})
	from := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token")
	page, err := client.Search(ctx, observabilityvo.LogQuery{AuthorizedCategories: []string{observabilityvo.CategoryAuditAdmin}, AuthorizedTenantID: "tenant-a", AuthorizedBusinessDomain: "domain-a", TimeFrom: &from, TimeTo: &to, ActorID: "user-1", Action: "set_default", TargetType: "llm_model", TargetID: "model-1", Outcomes: []string{"success"}})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(page.Records))
	}
	record := page.Records[0]
	if record.BusinessModule != "model_management" || record.ActorNameSnapshot != "管理员" || record.TargetNameSnapshot != "默认模型" || record.LogID != "model-manager:evt-1" {
		t.Fatalf("unexpected projected record: %+v", record)
	}
}
