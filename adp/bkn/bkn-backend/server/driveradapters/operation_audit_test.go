// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"

	"bkn-backend/common/operationaudit"
)

func TestRegisteredOperationAuditRoutes(t *testing.T) {
	tests := []struct {
		method, path, override, action, target string
		registered                             bool
	}{
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks", "", "create", "knowledge_network", true},
		{http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/:kn_id", "", "update", "knowledge_network", true},
		{http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/:kn_id", "", "delete", "knowledge_network", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-types", "", "create", "object_type", true},
		{http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-types/:ot_id/data_properties/:property_names", "", "update", "object_type", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-types", http.MethodGet, "", "", false},
		{http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/:kn_id/relation-types/:rt_id", "", "update", "relation_type", true},
		{http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/:kn_id/action-types/:at_ids", "", "delete", "action_type", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/metrics", "", "create", "metric", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/risk-types", "", "create", "risk_type", true},
		{http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/:kn_id/risk-types/:rt_id", "", "update", "risk_type", true},
		{http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/:kn_id/risk-types/:rt_ids", "", "delete", "risk_type", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types", "", "add_members", "concept_group", true},
		{http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types/:ot_ids", "", "remove_members", "concept_group", true},
		{http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/:kn_id/action-schedules/:schedule_id/status", "", "update", "action_schedule", true},
		{http.MethodPost, "/api/bkn-backend/v1/bkns", "", "import", "knowledge_network", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/validation", "", "", "", false},
		{http.MethodGet, "/api/bkn-backend/v1/bkns/:kn_id", "", "", "", false},
		{http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/:kn_id", "", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			rule, ok := registeredOperationAudit(test.method, test.path, test.override)
			if ok != test.registered || rule.Action != test.action || rule.TargetType != test.target {
				t.Fatalf("registeredOperationAudit() = %#v, %v", rule, ok)
			}
		})
	}
}

func TestOperationAuditIdentityUsesVerifiedVisitor(t *testing.T) {
	handler := &restHandler{}
	visitor := hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user"), TokenID: "token-a"}
	identity := handler.operationAuditIdentity(context.Background(), "Bearer oauth", visitor)
	if identity.ActorID != "user-a" || identity.ActorType != "user" || identity.CredentialID != "token-a" || identity.AuthMethod != "oauth" {
		t.Fatalf("operationAuditIdentity() = %#v", identity)
	}

	identity = handler.operationAuditIdentity(context.Background(), "Bearer bak_example", visitor)
	if identity.AuthMethod != "app_key" {
		t.Fatalf("AppKey auth method = %q", identity.AuthMethod)
	}

	identity = handler.operationAuditIdentity(context.Background(), "", visitor)
	if identity.AuthMethod != "service_header" {
		t.Fatalf("service auth method = %q", identity.AuthMethod)
	}
}

func TestOperationAuditEventIDIsStableForOneRequestAttempt(t *testing.T) {
	first := operationAuditEventID("tenant-a", "req-a", http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a")
	second := operationAuditEventID("tenant-a", "req-a", http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a")
	differentTarget := operationAuditEventID("tenant-a", "req-a", http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-b")
	if first != second || first == differentTarget {
		t.Fatalf("stable IDs = %q, %q, %q", first, second, differentTarget)
	}
}

type recordingOperationAuditStore struct {
	entries []operationaudit.Entry
}

func (s *recordingOperationAuditStore) Record(_ context.Context, entry operationaudit.Entry) error {
	s.entries = append(s.entries, entry)
	return nil
}

func TestOperationAuditMiddlewareRecordsOneReadableSuccessFact(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{
		auditRecorder: store,
		auditIdentityResolver: func(context.Context, string, hydra.Visitor) operationAuditActor {
			return operationAuditActor{ActorID: "user-a", ActorName: "Alice", ActorType: "user", AuthMethod: "app_key", CredentialID: "key-a"}
		},
	}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.POST("/api/bkn-backend/v1/knowledge-networks/:kn_id/object-types", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.Status(http.StatusCreated)
	})
	body := []byte(`{"entries":[{"id":"material","name":"物料","comment":"业务对象"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-types", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer bak_example")
	req.Header.Set("x-tenant-id", "tenant-a")
	req.Header.Set("x-business-domain", "domain-a")
	req.Header.Set("bkn-request-id", "req-a")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)

	if len(store.entries) != 1 {
		t.Fatalf("audit entries = %d", len(store.entries))
	}
	entry := store.entries[0]
	if entry.Action != "create" || entry.TargetType != "object_type" || entry.TargetID != "material" || entry.TargetName != "物料" ||
		entry.KnowledgeNetworkID != "kn-a" || entry.ActorName != "Alice" || entry.Outcome != "success" || entry.RequestID != "req-a" {
		t.Fatalf("audit entry = %#v", entry)
	}
	if changed, _ := entry.ChangeSummary["changed_fields"].([]string); len(changed) == 0 {
		t.Fatalf("change summary = %#v", entry.ChangeSummary)
	}
}

func TestOperationAuditFactsUseImportedKnowledgeNetworkID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/bkns", nil)
	facts := operationAuditFacts(
		context,
		operationAuditRule{Action: "import", TargetType: "knowledge_network"},
		nil,
		[]byte(`{"kn_id":"supplychain"}`),
	)
	if facts.targetID != "supplychain" || facts.knowledgeNetworkID != "supplychain" {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestOperationAuditMiddlewareRecordsBoundedFailureAndReplacesInvalidRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "name_conflict", "message": "名称已存在", "details": "must not be stored"}})
	})
	req := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", bytes.NewReader([]byte(`{"name":"供应链","password":"secret"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-tenant-id", "tenant-a")
	req.Header.Set("x-business-domain", "domain-a")
	req.Header.Set("bkn-request-id", string(make([]byte, 129)))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)

	if len(store.entries) != 1 {
		t.Fatalf("audit entries = %d", len(store.entries))
	}
	entry := store.entries[0]
	if entry.Outcome != "failure" || entry.FailureCode != "name_conflict" || entry.FailureMessage != "名称已存在" || len(entry.RequestID) > 128 || entry.RequestID == string(make([]byte, 129)) {
		t.Fatalf("failure entry = %#v", entry)
	}
	encoded, _ := json.Marshal(entry.ChangeSummary)
	if bytes.Contains(encoded, []byte("secret")) || bytes.Contains(encoded, []byte("password")) {
		t.Fatalf("sensitive field leaked: %s", encoded)
	}
	if _, err := time.Parse(time.RFC3339Nano, entry.EventTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("event time invalid: %v", err)
	}
}

func TestOperationAuditMiddlewareRecordsDeniedAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "permission_denied", "message": "无权删除该知识网络"}})
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	request.Header.Set("x-tenant-id", "tenant-a")
	request.Header.Set("x-business-domain", "domain-a")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if len(store.entries) != 1 || store.entries[0].Outcome != "denied" || store.entries[0].FailureCode != "permission_denied" {
		t.Fatalf("denied entry = %#v", store.entries)
	}
}

func TestOperationAuditMiddlewareDoesNotTrustUnverifiedIdentityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "unauthorized", "message": "invalid token"}})
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	request.Header.Set("x-tenant-id", "tenant-a")
	request.Header.Set("x-business-domain", "domain-a")
	request.Header.Set("x-account-id", "spoofed-user")
	request.Header.Set("x-account-type", "admin")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if len(store.entries) != 1 {
		t.Fatalf("audit entries = %d", len(store.entries))
	}
	entry := store.entries[0]
	if entry.ActorID != "unauthenticated" || entry.ActorName != "未认证调用者" || entry.ActorType != "unknown" {
		t.Fatalf("unverified identity was trusted: %#v", entry)
	}
}

func TestOperationAuditMiddlewareUsesInternalCallerIdentityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/in/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/in/v1/knowledge-networks/kn-a", nil)
	request.Header.Set("x-tenant-id", "tenant-a")
	request.Header.Set("x-business-domain", "domain-a")
	request.Header.Set("x-account-id", "internal-user")
	request.Header.Set("x-account-type", "user")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if len(store.entries) != 1 {
		t.Fatalf("audit entries = %d", len(store.entries))
	}
	entry := store.entries[0]
	if entry.ActorID != "internal-user" || entry.ActorName != "internal-user" || entry.ActorType != "user" {
		t.Fatalf("internal caller identity was lost: %#v", entry)
	}
}

func TestOperationAuditMiddlewareDoesNotPersistUnscopedFact(t *testing.T) {
	t.Setenv("BKN_OPERATION_AUDIT_TENANT_ID", "")
	t.Setenv("BKN_OPERATION_AUDIT_BUSINESS_DOMAIN", "")
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if len(store.entries) != 0 {
		t.Fatalf("unscoped audit fact must not be persisted: %#v", store.entries)
	}
}

func TestOperationAuditMiddlewareForcesNonExecutableJSONResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.Status(http.StatusBadRequest)
		_, _ = c.Writer.Write([]byte(`<script>alert("reflected")</script>`))
	})
	request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	request.Header.Set("x-tenant-id", "tenant-a")
	request.Header.Set("x-business-domain", "domain-a")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", contentType)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", response.Header().Get("X-Content-Type-Options"))
	}
}

func TestBoundTextPreservesUTF8AtByteLimit(t *testing.T) {
	value := strings.Repeat("物", 341) + "料"
	result := boundText(value, 1024)
	if len(result) > 1024 || !utf8.ValidString(result) {
		t.Fatalf("boundText() returned invalid UTF-8: bytes=%d value=%q", len(result), result)
	}
}

func TestOperationAuditMiddlewareSkipsReadsAndMethodOverrideSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.GET("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.POST("/api/bkn-backend/v1/knowledge-networks/:kn_id/object-types", func(c *gin.Context) { c.Status(http.StatusOK) })

	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil))
	search := httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-types", nil)
	search.Header.Set("X-HTTP-Method-Override", http.MethodGet)
	engine.ServeHTTP(httptest.NewRecorder(), search)
	if len(store.entries) != 0 {
		t.Fatalf("read operations produced audit entries: %#v", store.entries)
	}
}
