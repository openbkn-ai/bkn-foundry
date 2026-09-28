// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type safeAuditPublisherStub struct{ values [][]byte }

func (p *safeAuditPublisherStub) TryPublish(value []byte) auditpublisher.Disposition {
	p.values = append(p.values, append([]byte(nil), value...))
	return auditpublisher.Accepted
}

func TestSafeAdminMiddlewarePublishesKafkaWithoutLegacyStore(t *testing.T) {
	publisher := &safeAuditPublisherStub{}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(ctxAccessorID, "verified-admin"); c.Next() })
	router.Use(auditMiddleware(audit.NewKafkaRecorder(publisher, "test"), nil, nil))
	router.POST("/api/safe/v1/admin/users", func(c *gin.Context) { c.Status(http.StatusCreated) })
	request := httptest.NewRequest(http.MethodPost, "/api/safe/v1/admin/users", nil)
	request.Header.Set("x-request-id", "req-safe-kafka-user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || len(publisher.values) != 1 {
		t.Fatalf("business status=%d, Kafka records=%d", response.Code, len(publisher.values))
	}
	var record map[string]any
	if err := json.Unmarshal(publisher.values[0], &record); err != nil {
		t.Fatal(err)
	}
	if record["source_id"] != "bkn-safe-admin" || record["event_name"] != "safe.admin.operation.observed" {
		t.Fatalf("wrong Safe event: %+v", record)
	}
}

func TestSafeAdminGateRefusalPublishesAnonymousKafkaAudit(t *testing.T) {
	publisher := &safeAuditPublisherStub{}
	router := gin.New()
	router.Use(auditAuthFailures(audit.NewKafkaRecorder(publisher, "test"), nil, nil))
	router.POST("/api/safe/v1/admin/users", func(c *gin.Context) { abortGate(c, http.StatusUnauthorized, gateAuthn) })
	request := httptest.NewRequest(http.MethodPost, "/api/safe/v1/admin/users", nil)
	request.Header.Set("x-request-id", "req-safe-denied")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || len(publisher.values) != 1 {
		t.Fatalf("business status=%d, Kafka records=%d", response.Code, len(publisher.values))
	}
	var record map[string]any
	if err := json.Unmarshal(publisher.values[0], &record); err != nil {
		t.Fatal(err)
	}
	actor := record["actor"].(map[string]any)
	if record["outcome"] != "denied" || actor["type"] != "anonymous" {
		t.Fatalf("wrong denied Safe event: %+v", record)
	}
}

func TestSafeAuditRuntimeDoesNotMountHistoricalQueryRoute(t *testing.T) {
	publisher := &safeAuditPublisherStub{}
	kafka := audit.NewKafkaRecorder(publisher, "test")
	router := New(Deps{Audit: kafka})
	request := httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/audit-logs", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("retired query returned %d, want 404", response.Code)
	}
}

func TestAuditActionUsesStableBusinessSemantics(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/api/safe/v1/admin/users", "create"},
		{http.MethodPut, "/api/safe/v1/admin/users/:id", "update"},
		{http.MethodPut, "/api/safe/v1/admin/users/:id/password", "reset_password"},
		{http.MethodDelete, "/api/safe/v1/admin/users/:id", "delete"},
		{http.MethodPost, "/api/safe/v1/admin/departments/:id/members", "add_members"},
		{http.MethodDelete, "/api/safe/v1/admin/departments/:id/members", "remove_members"},
		{http.MethodPost, "/api/safe/v1/admin/role-bindings", "bind_role"},
		{http.MethodDelete, "/api/safe/v1/admin/role-bindings", "unbind_role"},
		{http.MethodPost, "/api/safe/v1/admin/object-grants", "grant"},
		{http.MethodDelete, "/api/safe/v1/admin/object-grants", "revoke"},
		{http.MethodPost, "/api/safe/v1/admin/object-grants/revoke", "revoke"},
		{http.MethodPost, "/api/safe/v1/me/object-grants", "grant"},
		{http.MethodDelete, "/api/safe/v1/me/object-grants", "revoke"},
		{http.MethodPost, "/api/safe/v1/me/object-grants/revoke", "revoke"},
		{http.MethodDelete, "/api/safe/v1/admin/enterprise-object-grants", "revoke"},
		{http.MethodPost, "/api/safe/v1/authz/explain", "explain"},
		{http.MethodPost, "/api/safe/v1/admin/roles/:id/permissions", "grant_permission"},
		{http.MethodDelete, "/api/safe/v1/admin/roles/:id/permissions", "revoke_permission"},
		{http.MethodPost, "/api/safe/v1/admin/license/import", "import"},
		{http.MethodPost, "/api/safe/v1/admin/license/activate", "activate"},
		{http.MethodDelete, "/api/safe/v1/admin/license", "remove"},
		{http.MethodPost, "/api/safe/v1/admin/oauth/access-origins", "add_access_origin"},
		{http.MethodDelete, "/api/safe/v1/admin/oauth/access-origins/:id", "remove_access_origin"},
		{http.MethodPut, "/api/safe/v1/me", "update_profile"},
	}
	for _, test := range tests {
		if got := auditAction(test.method, test.path); got != test.want {
			t.Errorf("%s %s: action=%q, want %q", test.method, test.path, got, test.want)
		}
	}
}

func TestAuditedManagementOperationExcludesPostQueries(t *testing.T) {
	for _, path := range []string{
		"/api/safe/v1/authz/explain",
		"/api/safe/v1/admin/object-grants/preview",
		"/api/safe/v1/admin/row-filter-policies/explain",
	} {
		if isAuditedManagementOperation(http.MethodPost, path) {
			t.Fatalf("POST query classified as management mutation: %s", path)
		}
	}
	for _, path := range []string{
		"/api/safe/v1/admin/users",
		"/api/safe/v1/admin/object-grants",
		"/api/safe/v1/admin/roles/:id/permissions",
	} {
		if !isAuditedManagementOperation(http.MethodPost, path) {
			t.Fatalf("management mutation was omitted: %s", path)
		}
	}
}

func TestAuditDetailTargetPromotesBusinessObjectIdentifiers(t *testing.T) {
	tests := []struct {
		resource string
		detail   string
		wantID   string
	}{
		{"object-grants", `{"accessor_id":"user-a","resource":{"type":"knowledge_network","id":"supplychain_hd0202"}}`, "supplychain_hd0202"},
		{"object-grants", `{"grant_id":"grant-a"}`, "grant-a"},
		{"object-grants", `{"grant_ids":["grant-a","grant-b"]}`, "grant-a"},
		{"enterprise-object-grants", `{"grant_id":"ee-grant-a"}`, "ee-grant-a"},
		{"explain", `{"accessor_id":"user-a","resource":{"type":"catalog","id":"catalog-a"}}`, "catalog-a"},
		{"role-bindings", `{"accessor_id":"user-a","role_id":"role-a"}`, "user-a"},
	}
	for _, test := range tests {
		if got := auditDetailTargetID(test.resource, test.detail); got != test.wantID {
			t.Errorf("resource %q: target id=%q, want %q", test.resource, got, test.wantID)
		}
	}
}
