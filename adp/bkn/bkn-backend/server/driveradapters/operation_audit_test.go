// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/operationaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"go.uber.org/mock/gomock"
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
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics", "", "create", "metric", true},
		{http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/:metric_id", "", "update", "metric", true},
		{http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/:metric_id", "", "delete", "metric", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/:metric_id/versions", "", "update", "metric", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/:metric_id/publish", "", "update", "metric", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/:metric_id/deprecate", "", "update", "metric", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/:metric_id/logic-properties", "", "update", "object_type", true},
		{http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/:metric_id/logic-properties/:property_name/metric-ref", "", "update", "object_type", true},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/validate", "", "", "", false},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/object-metrics/preview", "", "", "", false},
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

func TestOperationAuditRepeatedRequestIDProducesDistinctAttempts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.Set("operation_audit_target_names", []string{"供应链"})
		c.Status(http.StatusNoContent)
	})
	for range 2 {
		request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
		request.Header.Set("bkn-request-id", "reused-correlation-id")
		engine.ServeHTTP(httptest.NewRecorder(), request)
	}
	if len(store.entries) != 2 || store.entries[0].RequestID != store.entries[1].RequestID || store.entries[0].EventID == store.entries[1].EventID {
		t.Fatalf("request attempts share an event identity: %#v", store.entries)
	}
}

func TestOperationAuditBatchTargetRemainsPublishable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-types", nil)
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}}
	entries := make([]map[string]string, 20)
	for i := range entries {
		entries[i] = map[string]string{"id": strings.Repeat("x", 20) + string(rune('a'+i))}
	}
	body, _ := json.Marshal(map[string]any{"entries": entries})
	facts := operationAuditFacts(c, operationAuditRule{Action: "create", TargetType: "object_type"}, body, nil, "req-batch")
	if len(facts.targetID) > 256 || facts.targetID == "" {
		t.Fatalf("batch target is not publishable: %q", facts.targetID)
	}
}

func TestOperationAuditOversizedNetworkPathStillProducesFailureFact(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/oversized", nil)
	c.Params = gin.Params{{Key: "kn_id", Value: strings.Repeat("x", 129)}}
	c.Status(http.StatusNotFound)
	facts := operationAuditFacts(c, operationAuditRule{Action: "delete", TargetType: "knowledge_network"}, nil, nil, "req-long-kn")
	if facts.knowledgeNetworkID != "" || facts.targetID != strings.Repeat("x", 129) {
		t.Fatalf("oversized network scope must be omitted while retaining the bounded target: %#v", facts)
	}
	entry := operationaudit.Entry{EventID: "evt-long-kn", EventTime: time.Now().UTC(),
		ActorID: "user-a", ActorName: "Alice", TargetName: "供应链", RequestID: "req-long-kn", Method: http.MethodDelete,
		HTTPStatus: http.StatusNotFound, Action: "delete", TargetType: "knowledge_network",
		TargetID: facts.targetID, KnowledgeNetworkID: facts.knowledgeNetworkID,
		Outcome: facts.outcome, ChangeSummary: facts.changeSummary,
	}
	if _, err := operationaudit.BuildKafkaAuditRecord(entry, "test"); err != nil {
		t.Fatalf("failed business attempt must remain auditable: %v", err)
	}
}

type recordingOperationAuditStore struct {
	entries []operationaudit.Entry
}

func (s *recordingOperationAuditStore) Record(_ context.Context, entry operationaudit.Entry) error {
	s.entries = append(s.entries, entry)
	return nil
}

func TestOperationAuditRequestIDFailureDoesNotChangeBusinessResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uuid.SetRand(strings.NewReader(""))
	defer uuid.SetRand(nil)

	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.POST("/api/bkn-backend/v1/knowledge-networks", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks", nil)
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("business response status = %d, want %d", response.Code, http.StatusCreated)
	}
	if len(store.entries) != 0 {
		t.Fatalf("audit entries = %d, want 0", len(store.entries))
	}
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
	req.Header.Set("bkn-request-id", "req-a")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)

	if len(store.entries) != 1 {
		t.Fatalf("audit entries = %d", len(store.entries))
	}
	entry := store.entries[0]
	if entry.Action != "create" || entry.TargetType != "object_type" || entry.TargetID != "material" || entry.TargetName != "物料" ||
		entry.KnowledgeNetworkID != "kn-a" || entry.ActorName != "Alice" || entry.Outcome != "success" || entry.HTTPStatus != http.StatusCreated || entry.RequestID != "req-a" {
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
		"req-test",
	)
	if facts.targetID != "supplychain" || facts.knowledgeNetworkID != "supplychain" {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestOperationAuditFactsUseObjectMetricDefinitionSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics", nil)
	context.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}}
	facts := operationAuditFacts(
		context,
		operationAuditRule{Action: "create", TargetType: "metric"},
		[]byte(`{"name":"订单履约耗时","owner_object_type_id":"order"}`),
		[]byte(`{"definition":{"id":"metric-a","name":"订单履约耗时"},"kn_id":"kn-a"}`),
		"req-test",
	)
	if facts.targetID != "metric-a" || facts.targetName != "订单履约耗时" || facts.knowledgeNetworkID != "kn-a" {
		t.Fatalf("facts = %#v", facts)
	}
	if changed, _ := facts.changeSummary["changed_fields"].([]string); !containsString(changed, "owner_object_type_id") {
		t.Fatalf("change summary = %#v", facts.changeSummary)
	}
}

func TestOperationAuditFactsUseLogicPropertySnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics/metric-a/logic-properties", nil)
	context.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}, {Key: "metric_id", Value: "metric-a"}}
	context.Set("operation_audit_target_ids", []string{"order"})
	context.Set("operation_audit_target_names", []string{"订单"})
	facts := operationAuditFacts(
		context,
		operationAuditRule{Action: "update", TargetType: "object_type"},
		[]byte(`{"object_type_id":"order","property_name":"duration","display_name":"履约耗时"}`),
		[]byte(`{"name":"duration","display_name":"履约耗时"}`),
		"req-test",
	)
	if facts.targetID != "order" || facts.targetName != "订单" {
		t.Fatalf("facts = %#v", facts)
	}
	entry := operationaudit.Entry{
		EventID: "evt-logic-property", EventTime: time.Now().UTC(), ActorID: "user-a", ActorName: "Alice",
		RequestID: "req-test", Method: http.MethodPost, HTTPStatus: http.StatusCreated, Action: "update",
		TargetType: "object_type", TargetID: facts.targetID, TargetName: facts.targetName, Outcome: "success",
		ChangeSummary: facts.changeSummary,
	}
	if _, err := operationaudit.BuildKafkaAuditRecord(entry, "test"); err != nil {
		t.Fatalf("logic-property audit must be publishable: %v", err)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestOperationAuditMiddlewareSkipsUncommittedConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "name_conflict", "message": "名称已存在", "details": "must not be stored"}})
	})
	req := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", bytes.NewReader([]byte(`{"name":"供应链","password":"secret"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("bkn-request-id", string(make([]byte, 129)))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)

	if len(store.entries) != 0 {
		t.Fatalf("uncommitted request produced audit: %#v", store.entries)
	}
}

func TestOperationAuditFailedCreateDoesNotInventKnowledgeNetworkScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.POST("/api/bkn-backend/v1/knowledge-networks", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing name"})
	})
	request := httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks", bytes.NewReader([]byte(`{}`)))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if len(store.entries) != 0 {
		t.Fatalf("uncommitted request produced audit: %#v", store.entries)
	}
}

func TestOperationAuditMiddlewareSkipsDeniedAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "permission_denied", "message": "无权删除该知识网络"}})
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if len(store.entries) != 0 {
		t.Fatalf("uncommitted request produced audit: %#v", store.entries)
	}
}

func TestOperationAuditMiddlewareDoesNotTrustUnverifiedIdentityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "unauthorized", "message": "invalid token"}})
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	request.Header.Set("x-account-id", "spoofed-user")
	request.Header.Set("x-account-type", "admin")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if len(store.entries) != 0 {
		t.Fatalf("uncommitted request produced audit: %#v", store.entries)
	}
}

func TestOperationAuditMiddlewareUsesInternalCallerIdentityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/in/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set("operation_audit_target_names", []string{"供应链"})
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/in/v1/knowledge-networks/kn-a", nil)
	request.Header.Set("x-account-id", "internal-user")
	request.Header.Set("x-account-type", "user")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if len(store.entries) != 1 {
		t.Fatalf("audit entries = %d", len(store.entries))
	}
	entry := store.entries[0]
	if entry.ActorID != "internal-user" || entry.ActorName != "Alice" || entry.ActorType != "user" {
		t.Fatalf("internal caller identity was lost: %#v", entry)
	}
}

func TestOperationAuditMiddlewarePersistsFactWithoutLegacyScopeHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{
		auditRecorder: store,
		auditIdentityResolver: func(context.Context, string, hydra.Visitor) operationAuditActor {
			return operationAuditActor{ActorID: "user-a", ActorName: "Alice", ActorType: "user", AuthMethod: "oauth"}
		},
	}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.Set("operation_audit_target_names", []string{"供应链"})
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if len(store.entries) != 1 {
		t.Fatalf("authenticated audit fact must be persisted without a removed scope header: %#v", store.entries)
	}
}

func TestOperationAuditMiddlewareForcesNonExecutableJSONResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.Status(http.StatusBadRequest)
		_, _ = c.Writer.Write([]byte(`{"message":"<script>alert(\"reflected\")</script>"}`))
	})
	request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", contentType)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", response.Header().Get("X-Content-Type-Options"))
	}
	if response.Body.String() != `{"message":"<script>alert(\"reflected\")</script>"}` {
		t.Fatalf("business response bytes changed: %s", response.Body.String())
	}
	var responseJSON map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &responseJSON); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if responseJSON["message"] != `<script>alert("reflected")</script>` {
		t.Fatalf("response semantics changed: %#v", responseJSON)
	}
}

func TestOperationAuditDoesNotRewriteNonJSONBusinessResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingOperationAuditStore{}
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
	engine := gin.New()
	engine.Use(handler.OperationAudit())
	engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) {
		c.Set(operationAuditVisitorKey, hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")})
		c.Data(http.StatusAccepted, "application/octet-stream", []byte("opaque-response"))
	})
	request := httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.String() != "opaque-response" || response.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("audit middleware rewrote business response: status=%d body=%q type=%q", response.Code, response.Body.String(), response.Header().Get("Content-Type"))
	}
}

func TestOperationAuditResponseCaptureIsBoundedAndLossless(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writer := &boundedResponseWriter{ResponseWriter: context.Writer, limit: maximumOperationAuditBody}
	body := bytes.Repeat([]byte("x"), maximumOperationAuditBody*3)
	if written, err := writer.Write(body); err != nil || written != len(body) {
		t.Fatalf("business write = %d, %v", written, err)
	}
	if writer.body.Len() != maximumOperationAuditBody || !bytes.Equal(recorder.Body.Bytes(), body) {
		t.Fatalf("response capture=%d business bytes=%d", writer.body.Len(), recorder.Body.Len())
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
	handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, visitor hydra.Visitor) operationAuditActor {
		name := ""
		if visitor.ID != "" {
			name = "Alice"
		}
		return operationAuditActor{ActorID: visitor.ID, ActorName: name, ActorType: string(visitor.Type), AuthMethod: "oauth"}
	}}
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

func TestOperationAuditCreatedArrayUsesActualIDsAndReadableBatch(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	facts := operationAuditFacts(c, operationAuditRule{Action: "create", TargetType: "object_type"}, []byte(`{"entries":[{"name":"客户"},{"name":"订单"}]}`), []byte(`[{"id":"actual-a"},{"id":"actual-b"}]`), "req-a")
	if facts.targetID != "actual-a,actual-b" || facts.targetName != "客户、订单，共2个" {
		t.Fatalf("facts=%#v", facts)
	}
}

func TestOperationAuditNamesKeepsBoundedReadableSummary(t *testing.T) {
	name := operationAuditNames([]string{"采购", "库存", "销售", "财务", "生产", "运输"})
	if !strings.Contains(name, "采购") || !strings.Contains(name, "共6个") || !strings.Contains(name, "已截断") {
		t.Fatal(name)
	}
}

func TestOperationAuditRejectsPureIDActorAndMissingTargetName(t *testing.T) {
	for _, actorName := range []string{"", "user-a", "Alice"} {
		store := &recordingOperationAuditStore{}
		handler := &restHandler{auditRecorder: store, auditIdentityResolver: func(context.Context, string, hydra.Visitor) operationAuditActor {
			return operationAuditActor{ActorID: "user-a", ActorName: actorName}
		}}
		engine := gin.New()
		engine.Use(handler.OperationAudit())
		engine.PUT("/api/bkn-backend/v1/knowledge-networks/:kn_id", func(c *gin.Context) { c.Status(204) })
		engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/api/bkn-backend/v1/knowledge-networks/kn-a", nil))
		if len(store.entries) != 0 {
			t.Fatalf("invalid snapshots published: %#v", store.entries)
		}
	}
}

func TestOperationAuditCapabilityDetachRealHandler(t *testing.T) {
	function := &interfaces.CapabilityBinding{ID: "binding-function", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, CapabilityID: "tool-1", Name: "Invoice lookup"}
	skill := &interfaces.CapabilityBinding{ID: "binding-skill", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1", Name: "Invoice review"}
	mcp := &interfaces.CapabilityBinding{ID: "binding-mcp", CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL, OwnerID: "mcp-1", CapabilityID: "search", Name: "search"}
	tests := []struct {
		name                                     string
		entries                                  []*interfaces.CapabilityBinding
		disabled, lookupFailure, mutationFailure bool
		rows                                     int64
		wantName                                 string
	}{
		{name: "disabled", entries: []*interfaces.CapabilityBinding{function}, disabled: true, rows: 1},
		{name: "function", entries: []*interfaces.CapabilityBinding{function}, rows: 1, wantName: "Sales / Invoice lookup"},
		{name: "skill", entries: []*interfaces.CapabilityBinding{skill}, rows: 1, wantName: "Sales / Invoice review"},
		{name: "mcp_tool", entries: []*interfaces.CapabilityBinding{mcp}, rows: 1, wantName: "Sales / search"},
		{name: "mixed_batch", entries: []*interfaces.CapabilityBinding{function, skill, mcp}, rows: 3, wantName: "Sales / Invoice lookup、Sales / Invoice review、Sales / search，共3个"},
		{name: "missing_mcp_metadata", entries: []*interfaces.CapabilityBinding{{ID: "binding-mcp", CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL, CapabilityID: "search", Status: interfaces.CAPABILITY_STATUS_MISSING}}, rows: 1, wantName: "Sales / search"},
		{name: "missing_function_name", entries: []*interfaces.CapabilityBinding{{ID: "binding-function", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, CapabilityID: "tool-1", Status: interfaces.CAPABILITY_STATUS_MISSING}}, rows: 1},
		{name: "technical_id_is_not_function_name", entries: []*interfaces.CapabilityBinding{{ID: "binding-function", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, CapabilityID: "tool-1", Name: "tool-1"}}, rows: 1},
		{name: "lookup_failure", entries: []*interfaces.CapabilityBinding{function}, lookupFailure: true, rows: 1},
		{name: "mutation_denied", entries: []*interfaces.CapabilityBinding{mcp}, mutationFailure: true},
		{name: "missing_mcp_tool_name", entries: []*interfaces.CapabilityBinding{{ID: "binding-mcp", CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL}}, rows: 1},
		{name: "no_change", entries: []*interfaces.CapabilityBinding{function}, rows: 0},
		{name: "partial_change", entries: []*interfaces.CapabilityBinding{function, skill, mcp}, rows: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			kns := bmock.NewMockKNService(ctrl)
			bindings := bmock.NewMockCapabilityBindingService(ctrl)
			store := &recordingOperationAuditStore{}
			handler := &restHandler{kns: kns, cbs: bindings, auditIdentityResolver: func(_ context.Context, _ string, v hydra.Visitor) operationAuditActor {
				return operationAuditActor{ActorID: v.ID, ActorName: "Alice", ActorType: "user", AuthMethod: "oauth"}
			}}
			previous := kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn-1", "main").Return("Sales", true, nil)
			if !test.disabled {
				handler.auditRecorder = store
				previous = bindings.EXPECT().ListCapabilities(gomock.Any(), interfaces.CapabilityBindingsQueryParams{
					KNID: "kn-1", Branch: "main", WithDetail: true,
					PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
				}).After(previous)
				if test.lookupFailure {
					previous.Return(nil, fmt.Errorf("metadata unavailable"))
				} else {
					previous.Return(&interfaces.CapabilityBindingsList{Entries: test.entries}, nil)
				}
			}
			ids := make([]string, 0, len(test.entries))
			for _, binding := range test.entries {
				ids = append(ids, binding.ID)
			}
			var mutationErr error
			if test.mutationFailure {
				mutationErr = rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden)
			}
			bindings.EXPECT().DetachCapabilities(gomock.Any(), gomock.Any(), "kn-1", "main", ids).After(previous).Return(test.rows, mutationErr)
			engine := gin.New()
			engine.Use(handler.OperationAudit())
			engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id/capabilities/:binding_ids", func(c *gin.Context) {
				v := hydra.Visitor{ID: "user-1", Type: "user"}
				c.Set(operationAuditVisitorKey, v)
				handler.DetachCapabilities(c, v)
			})
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest("DELETE", "/api/bkn-backend/v1/knowledge-networks/kn-1/capabilities/"+strings.Join(ids, ","), nil))
			wantStatus := http.StatusNoContent
			if test.mutationFailure {
				wantStatus = http.StatusForbidden
			}
			if response.Code != wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantName == "" {
				if len(store.entries) != 0 {
					t.Fatalf("entries=%+v", store.entries)
				}
				return
			}
			if len(store.entries) != 1 {
				t.Fatalf("entries=%+v", store.entries)
			}
			entry := store.entries[0]
			if entry.TargetName != test.wantName || entry.Action != "detach" || entry.ActorName != "Alice" {
				t.Fatalf("entry=%+v", entry)
			}
			// Exercise the production event builder, including the Audit SDK's schema validation.
			payload, err := operationaudit.BuildKafkaAuditRecord(entry, "test")
			if err != nil {
				t.Fatal(err)
			}
			var event struct {
				Summary string `json:"summary"`
				Target  struct {
					Name string `json:"name"`
				} `json:"target"`
			}
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Target.Name != test.wantName || !strings.Contains(event.Summary, test.wantName) || strings.Contains(event.Summary, "binding-") {
				t.Fatalf("event=%s", payload)
			}
		})
	}
}

func TestOperationAuditGroupMembershipRealHandler(t *testing.T) {
	for _, action := range []string{"add", "remove"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%v", action, enabled), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				kns := bmock.NewMockKNService(ctrl)
				groups := bmock.NewMockConceptGroupService(ctrl)
				objects := bmock.NewMockObjectTypeService(ctrl)
				store := &recordingOperationAuditStore{}
				handler := &restHandler{kns: kns, cgs: groups, ots: objects, auditIdentityResolver: func(_ context.Context, _ string, v hydra.Visitor) operationAuditActor {
					return operationAuditActor{ActorID: v.ID, ActorName: "Alice", ActorType: "user", AuthMethod: "oauth"}
				}}
				kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn-1", "main").Return("Sales", true, nil)
				groups.EXPECT().CheckConceptGroupExistByID(gomock.Any(), "kn-1", "main", "group-1").Return("Transactions", true, nil)
				if enabled {
					handler.auditRecorder = store
					objects.EXPECT().CheckObjectTypeExistByID(gomock.Any(), "kn-1", "main", "object-1").Return("Orders", true, nil)
				}
				engine := gin.New()
				engine.Use(handler.OperationAudit())
				v := hydra.Visitor{ID: "user-1", Type: "user"}
				var request *http.Request
				if action == "remove" {
					groups.EXPECT().ListConceptGroupRelations(gomock.Any(), gomock.Any()).Return([]interfaces.ConceptGroupRelation{{ConceptID: "object-1"}}, nil)
					groups.EXPECT().DeleteObjectTypesFromGroup(gomock.Any(), gomock.Any(), "kn-1", "main", "group-1", []string{"object-1"}).Return(nil)
					engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types/:ot_ids", func(c *gin.Context) { c.Set(operationAuditVisitorKey, v); handler.DeleteObjectTypesFromGroup(c, v) })
					request = httptest.NewRequest("DELETE", "/api/bkn-backend/v1/knowledge-networks/kn-1/concept-groups/group-1/object-types/object-1", nil)
				} else {
					groups.EXPECT().AddObjectTypesToConceptGroup(gomock.Any(), gomock.Any(), "kn-1", "main", "group-1", []interfaces.ID{{ID: "object-1"}}, interfaces.ImportMode_Normal, true).Return([]string{"membership-1"}, nil)
					engine.POST("/api/bkn-backend/v1/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types", func(c *gin.Context) { c.Set(operationAuditVisitorKey, v); handler.AddObjectTypesToConceptGroup(c, v) })
					request = httptest.NewRequest("POST", "/api/bkn-backend/v1/knowledge-networks/kn-1/concept-groups/group-1/object-types", strings.NewReader(`{"entries":[{"id":"object-1"}]}`))
				}
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != 204 && response.Code != 201 {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				if enabled {
					if len(store.entries) != 1 || store.entries[0].TargetName != "Transactions / Orders" {
						t.Fatalf("entries=%+v", store.entries)
					}
				} else if len(store.entries) != 0 {
					t.Fatal("disabled audit recorded")
				}
			})
		}
	}
}

func TestOperationAuditCapabilityAttachRealHandler(t *testing.T) {
	for _, tc := range []struct {
		name                                                        string
		disabled, existing, duplicate, mixed, lookupFailure, denied bool
		kind, capability, business                                  string
	}{
		{name: "function", kind: "function", capability: "tool-1", business: "Invoice lookup"},
		{name: "skill", kind: "skill", capability: "skill-1", business: "Invoice review"},
		{name: "mcp_tool", kind: "mcp_tool", capability: "search", business: "search"},
		{name: "mixed_existing_and_new", mixed: true, kind: "skill", capability: "skill-1", business: "Invoice review"},
		{name: "missing_name", kind: "function", capability: "tool-1"},
		{name: "technical_name", kind: "function", capability: "tool-1", business: "tool-1"},
		{name: "duplicate", duplicate: true, kind: "skill", capability: "skill-1", business: "Invoice review"},
		{name: "already_attached", existing: true, kind: "skill", capability: "skill-1", business: "Invoice review"},
		{name: "disabled", disabled: true, kind: "skill", capability: "skill-1", business: "Invoice review"},
		{name: "lookup_failure", lookupFailure: true, kind: "skill", capability: "skill-1", business: "Invoice review"},
		{name: "denied", denied: true, kind: "skill", capability: "skill-1", business: "Invoice review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			kns := bmock.NewMockKNService(ctrl)
			cbs := bmock.NewMockCapabilityBindingService(ctrl)
			store := &recordingOperationAuditStore{}
			h := &restHandler{kns: kns, cbs: cbs, auditIdentityResolver: func(_ context.Context, _ string, v hydra.Visitor) operationAuditActor {
				return operationAuditActor{ActorID: v.ID, ActorName: "Alice", ActorType: "user", AuthMethod: "oauth"}
			}}
			if !tc.disabled {
				h.auditRecorder = store
			}
			kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn-1", "main").Return("Sales", true, nil)
			binding := &interfaces.CapabilityBinding{ID: "binding-1", CapabilityType: tc.kind, CapabilityID: tc.capability, CreatedByAttach: !tc.existing}
			returned := []*interfaces.CapabilityBinding{binding}
			if tc.mixed {
				returned = append(returned, &interfaces.CapabilityBinding{ID: "binding-old", CapabilityType: "skill", CapabilityID: "skill-old"})
			}
			if tc.duplicate {
				returned = append(returned, binding)
			}
			var err error
			if tc.denied {
				err = rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden)
			}
			mutation := cbs.EXPECT().AttachCapabilities(gomock.Any(), nil, "kn-1", "main", gomock.Any()).Return(returned, err)
			if !tc.disabled && !tc.existing && !tc.denied {
				lookup := cbs.EXPECT().ListCapabilities(gomock.Any(), interfaces.CapabilityBindingsQueryParams{KNID: "kn-1", Branch: "main", WithDetail: true, PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1}}).After(mutation)
				if tc.lookupFailure {
					lookup.Return(nil, fmt.Errorf("metadata unavailable"))
				} else {
					metadata := *binding
					metadata.Name = tc.business
					lookup.Return(&interfaces.CapabilityBindingsList{Entries: []*interfaces.CapabilityBinding{&metadata}}, nil)
				}
			}
			engine := gin.New()
			engine.Use(h.OperationAudit())
			engine.POST("/api/bkn-backend/v1/knowledge-networks/:kn_id/capabilities", func(c *gin.Context) {
				v := hydra.Visitor{ID: "user-1", Type: "user"}
				c.Set(operationAuditVisitorKey, v)
				h.AttachCapabilities(c, v)
			})
			request := httptest.NewRequest("POST", "/api/bkn-backend/v1/knowledge-networks/kn-1/capabilities", strings.NewReader(fmt.Sprintf(`{"capabilities":[{"capability_type":%q,"capability_id":%q}],"entries":[{"name":"forged one"},{"name":"forged two"}]}`, tc.kind, tc.capability)))
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			wantStatus := http.StatusOK
			if tc.denied {
				wantStatus = http.StatusForbidden
			}
			if response.Code != wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.disabled || tc.existing || tc.lookupFailure || tc.denied || tc.business == "" || (tc.kind != "mcp_tool" && tc.business == tc.capability) {
				if len(store.entries) != 0 {
					t.Fatalf("entries=%+v", store.entries)
				}
				return
			}
			assertReadableAuditEvent(t, store, "attach", "Sales / "+tc.business)
			if store.entries[0].TargetID != "binding-1" {
				t.Fatalf("target ID includes unaffected or duplicate bindings: %s", store.entries[0].TargetID)
			}
			if strings.Contains(response.Body.String(), "CreatedByAttach") || strings.Contains(response.Body.String(), "Invoice") {
				t.Fatalf("audit metadata leaked into response: %s", response.Body.String())
			}
		})
	}
}

func TestOperationAuditBKNImportRealHandler(t *testing.T) {
	for _, tc := range []struct {
		name                                                                                     string
		disabled, denied, capabilityFailure, invalid, ignore, skipped, newBinding, lookupFailure bool
	}{{name: "success"}, {name: "disabled", disabled: true}, {name: "denied", denied: true}, {name: "capability_failure", capabilityFailure: true}, {name: "invalid_archive", invalid: true}, {name: "ignore_new_network", ignore: true}, {name: "ignore_existing_no_change", ignore: true, skipped: true}, {name: "ignore_existing_new_binding", ignore: true, skipped: true, newBinding: true}, {name: "ignore_existing_lookup_failure", ignore: true, skipped: true, newBinding: true, lookupFailure: true}, {name: "ignore_existing_capability_failure", ignore: true, skipped: true, capabilityFailure: true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			auth := bmock.NewMockAuthService(ctrl)
			kns := bmock.NewMockKNService(ctrl)
			cbs := bmock.NewMockCapabilityBindingService(ctrl)
			store := &recordingOperationAuditStore{}
			h := &restHandler{as: auth, kns: kns, cbs: cbs, auditIdentityResolver: func(_ context.Context, _ string, v hydra.Visitor) operationAuditActor {
				return operationAuditActor{ActorID: v.ID, ActorName: "Alice", ActorType: "user", AuthMethod: "oauth"}
			}}
			if !tc.disabled {
				h.auditRecorder = store
			}
			auth.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(hydra.Visitor{ID: "user-1", Type: "user"}, nil)
			if !tc.invalid {
				var err error
				if tc.denied {
					err = rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden)
				}
				mode := interfaces.ImportMode_Overwrite
				if tc.ignore {
					mode = interfaces.ImportMode_Ignore
				}
				kns.EXPECT().CreateKN(gomock.Any(), gomock.Any(), mode, false).DoAndReturn(func(_ context.Context, kn *interfaces.KN, _ string, _ bool) (string, error) {
					if kn.KNName != "Test Network" {
						t.Fatalf("name=%s", kn.KNName)
					}
					kn.ImportSkipped = tc.skipped
					return "kn-1", err
				})
				if !tc.denied {
					var capErr error
					if tc.capabilityFailure {
						capErr = fmt.Errorf("capability unavailable")
					}
					var bindings []*interfaces.CapabilityBinding
					if tc.newBinding {
						bindings = []*interfaces.CapabilityBinding{{ID: "binding-1", CreatedByAttach: true}}
					}
					cbs.EXPECT().ImportCapabilitiesTx(gomock.Any(), nil, "kn-1", "main", gomock.Any()).Return(&interfaces.CapabilityImportReport{}, bindings, capErr)
					if tc.skipped && tc.newBinding && !tc.disabled {
						var lookupErr error
						if tc.lookupFailure {
							lookupErr = fmt.Errorf("network name unavailable")
						}
						kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn-1", "main").Return("Persisted Network", true, lookupErr)
					}
				}
			}
			engine := gin.New()
			engine.Use(h.OperationAudit())
			engine.POST("/api/bkn-backend/v1/bkns", h.UploadBKN)
			archive := newValidBKNTar(t)
			if tc.invalid {
				archive = []byte("invalid archive")
			}
			response := httptest.NewRecorder()
			url := "/api/bkn-backend/v1/bkns"
			if tc.ignore {
				url += "?import_mode=ignore"
			}
			engine.ServeHTTP(response, newMultipartRequest(t, url, "network.tar", archive))
			status := http.StatusOK
			if tc.denied {
				status = http.StatusForbidden
			}
			if tc.invalid {
				status = http.StatusBadRequest
			}
			if response.Code != status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.disabled || tc.denied || tc.invalid || (tc.skipped && (!tc.newBinding || tc.capabilityFailure || tc.lookupFailure)) {
				if len(store.entries) != 0 {
					t.Fatalf("entries=%+v", store.entries)
				}
				return
			}
			name := "Test Network"
			if tc.skipped {
				name = "Persisted Network"
			}
			assertReadableAuditEvent(t, store, "import", name)
			if store.entries[0].KnowledgeNetworkID != "kn-1" {
				t.Fatalf("entry=%+v", store.entries[0])
			}
		})
	}
}

func assertReadableAuditEvent(t *testing.T, store *recordingOperationAuditStore, action, name string) {
	t.Helper()
	if len(store.entries) != 1 {
		t.Fatalf("entries=%+v", store.entries)
	}
	entry := store.entries[0]
	if entry.Action != action || entry.TargetName != name || entry.ActorName != "Alice" {
		t.Fatalf("entry=%+v", entry)
	}
	payload, err := operationaudit.BuildKafkaAuditRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Summary string `json:"summary"`
		Target  struct {
			Name string `json:"name"`
		} `json:"target"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Target.Name != name || !strings.Contains(event.Summary, name) || strings.Contains(event.Summary, "binding-") {
		t.Fatalf("event=%s", payload)
	}
}

func TestOperationAuditJSONImportIgnoreRealHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	kns := bmock.NewMockKNService(ctrl)
	store := &recordingOperationAuditStore{}
	h := &restHandler{kns: kns, auditRecorder: store, auditIdentityResolver: func(_ context.Context, _ string, v hydra.Visitor) operationAuditActor {
		return operationAuditActor{ActorID: v.ID, ActorName: "Alice", ActorType: "user"}
	}}
	kns.EXPECT().CreateKN(gomock.Any(), gomock.Any(), interfaces.ImportMode_Ignore, true).DoAndReturn(func(_ context.Context, kn *interfaces.KN, _ string, _ bool) (string, error) {
		kn.ImportSkipped = true
		return "kn-1", nil
	})
	engine := gin.New()
	engine.Use(h.OperationAudit())
	engine.POST("/api/bkn-backend/v1/knowledge-networks", func(c *gin.Context) {
		v := hydra.Visitor{ID: "user-1", Type: "user"}
		c.Set(operationAuditVisitorKey, v)
		h.CreateKN(c, v)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("POST", "/api/bkn-backend/v1/knowledge-networks?import_mode=ignore", strings.NewReader(`{"id":"kn-1","name":"Unpersisted Name","branch":"main"}`)))
	if response.Code != http.StatusCreated || len(store.entries) != 0 {
		t.Fatalf("status=%d entries=%+v body=%s", response.Code, store.entries, response.Body.String())
	}
}
