// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"bytes"
	"context"
	"database/sql"
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

	"bkn-backend/common/operationaudit"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
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

func TestOperationAuditMetricDeletionRealHandler(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			kns := bmock.NewMockKNService(ctrl)
			metrics := bmock.NewMockMetricService(ctrl)
			store := &recordingOperationAuditStore{}
			handler := &restHandler{kns: kns, ms: metrics, auditIdentityResolver: func(_ context.Context, _ string, v hydra.Visitor) operationAuditActor {
				return operationAuditActor{ActorID: v.ID, ActorName: "Alice", ActorType: "user", AuthMethod: "oauth"}
			}}
			if enabled {
				handler.auditRecorder = store
				metrics.EXPECT().CheckMetricExistByID(gomock.Any(), "kn-1", "main", "metric-1").Return("Revenue", true, nil)
			}
			kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn-1", "main").Return("Sales", true, nil)
			metrics.EXPECT().DeleteMetricsByIDs(gomock.Any(), gomock.Any(), "kn-1", "main", []string{"metric-1"}).Return(nil)
			engine := gin.New()
			engine.Use(handler.OperationAudit())
			engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id/metrics/:metric_ids", func(c *gin.Context) {
				v := hydra.Visitor{ID: "user-1", Type: "user"}
				c.Set(operationAuditVisitorKey, v)
				handler.DeleteMetricsByIDs(c, v)
			})
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest("DELETE", "/api/bkn-backend/v1/knowledge-networks/kn-1/metrics/metric-1", nil))
			if response.Code != 204 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if enabled {
				if len(store.entries) != 1 || store.entries[0].TargetName != "Revenue" {
					t.Fatalf("entries=%+v", store.entries)
				}
			} else if len(store.entries) != 0 {
				t.Fatal("disabled audit recorded")
			}
		})
	}
}

func TestOperationAuditCapabilityDetachRealHandler(t *testing.T) {
	for _, scenario := range []string{"disabled", "success", "lookup_failure", "no_change"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			kns := bmock.NewMockKNService(ctrl)
			bindings := bmock.NewMockCapabilityBindingService(ctrl)
			store := &recordingOperationAuditStore{}
			handler := &restHandler{kns: kns, cbs: bindings, auditIdentityResolver: func(_ context.Context, _ string, v hydra.Visitor) operationAuditActor {
				return operationAuditActor{ActorID: v.ID, ActorName: "Alice", ActorType: "user", AuthMethod: "oauth"}
			}}
			kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn-1", "main").Return("Sales", true, nil)
			if scenario != "disabled" {
				handler.auditRecorder = store
				lookup := bindings.EXPECT().ListCapabilities(gomock.Any(), interfaces.CapabilityBindingsQueryParams{
					KNID: "kn-1", Branch: "main", WithDetail: true,
					PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
				})
				if scenario == "lookup_failure" {
					lookup.Return(nil, fmt.Errorf("metadata unavailable"))
				} else {
					lookup.Return(&interfaces.CapabilityBindingsList{Entries: []*interfaces.CapabilityBinding{{ID: "binding-1", CapabilityID: "tool-1", Name: "Invoice lookup"}}}, nil)
				}
			}
			rows := int64(1)
			if scenario == "no_change" {
				rows = 0
			}
			bindings.EXPECT().DetachCapabilities(gomock.Any(), gomock.Any(), "kn-1", "main", []string{"binding-1"}).DoAndReturn(func(_ context.Context, _ *sql.Tx, _, _ string, _ []string) (int64, error) { return rows, nil })
			engine := gin.New()
			engine.Use(handler.OperationAudit())
			engine.DELETE("/api/bkn-backend/v1/knowledge-networks/:kn_id/capabilities/:binding_ids", func(c *gin.Context) {
				v := hydra.Visitor{ID: "user-1", Type: "user"}
				c.Set(operationAuditVisitorKey, v)
				handler.DetachCapabilities(c, v)
			})
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest("DELETE", "/api/bkn-backend/v1/knowledge-networks/kn-1/capabilities/binding-1", nil))
			if response.Code != 204 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if scenario == "success" {
				if len(store.entries) != 1 || store.entries[0].TargetName != "Sales / Invoice lookup" {
					t.Fatalf("entries=%+v", store.entries)
				}
			} else if len(store.entries) != 0 {
				t.Fatalf("entries=%+v", store.entries)
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
