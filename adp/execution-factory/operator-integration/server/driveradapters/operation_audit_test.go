package driveradapters

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
	infra "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func TestCaptureExecutionAuditRequestRestoresOversizedBody(t *testing.T) {
	body := strings.Repeat("x", maximumExecutionAuditRequestBody+1)
	request := httptest.NewRequest(http.MethodPost, "/operator/register", strings.NewReader(body))
	request.ContentLength = -1 // chunked body: the middleware must restore its consumed prefix.

	if captured := captureExecutionAuditRequest(request); captured != nil {
		t.Fatalf("captured oversized body = %#v, want nil", captured)
	}
	restored, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read restored request body: %v", err)
	}
	if string(restored) != body {
		t.Fatalf("request body was not restored: got %d bytes, want %d", len(restored), len(body))
	}
}

func TestExecutionAuditTargetUsesRegisteredToolboxName(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	id, name := executionAuditTarget(context, "toolbox", map[string]any{"box_name": "batch_probe"}, "req-1")
	if id != "toolbox:req-1" || name != "batch_probe" {
		t.Fatalf("target = %q/%q, want request-scoped ID and stated toolbox name", id, name)
	}
}

func TestConvertOperatorToToolAuditUsesReturnedToolID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "Operator"},
		}))
		c.Next()
	})
	engine.Use(OperationAudit(recorder))
	engine.POST("/operator/convert/tool", func(c *gin.Context) {
		c.Set("bkn.operation_audit.tool_id", "created-tool-1")
		c.Status(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodPost, "/operator/convert/tool", strings.NewReader(`{"operator_id":"source-operator-1","box_id":"box-1"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(infra.HeaderBKNRequestID, "req-convert-1")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if len(recorder.entries) != 1 || recorder.entries[0].TargetID != "created-tool-1" || recorder.entries[0].TargetType != "tool" {
		t.Fatalf("convert-to-tool target = %+v, want one created Tool", recorder.entries)
	}
}

func TestConvertOperatorToToolFailureDoesNotCallSourceOperatorTheTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "Operator"},
		}))
		c.Next()
	})
	engine.Use(OperationAudit(recorder))
	engine.POST("/operator/convert/tool", func(c *gin.Context) { c.Status(http.StatusBadRequest) })
	request := httptest.NewRequest(http.MethodPost, "/operator/convert/tool", strings.NewReader(`{"operator_id":"source-operator-1","box_id":"box-1"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(infra.HeaderBKNRequestID, "req-convert-failure")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if len(recorder.entries) != 1 || recorder.entries[0].TargetID != "tool:req-convert-failure" || recorder.entries[0].Outcome != "failure" {
		t.Fatalf("failed conversion target = %+v, want request-scoped Tool attempt", recorder.entries)
	}
}

type capturedExecutionAuditRecorder struct{ entries []operationaudit.Entry }

func (recorder *capturedExecutionAuditRecorder) Record(_ context.Context, entry operationaudit.Entry) error {
	recorder.entries = append(recorder.entries, entry)
	return nil
}

func TestOperationAuditGeneratesRequestIDWhenTheCallerDoesNotSendOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "管理员"},
		}))
		c.Next()
	})
	engine.Use(OperationAudit(recorder))
	engine.POST("/operator/register", func(c *gin.Context) { c.Status(http.StatusCreated) })

	request := httptest.NewRequest(http.MethodPost, "/operator/register", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if len(recorder.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(recorder.entries))
	}
	if recorder.entries[0].RequestID == "" || response.Header().Get(infra.HeaderBKNRequestID) == "" {
		t.Fatalf("request id must be generated and returned: %+v", recorder.entries[0])
	}
}

func TestOperationAuditReusedRequestIDGetsDistinctEventIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "Operator"},
		}))
		c.Next()
	})
	engine.Use(OperationAudit(recorder))
	engine.POST("/operator/register", func(c *gin.Context) { c.Status(http.StatusCreated) })
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/operator/register", nil)
		request.Header.Set(infra.HeaderBKNRequestID, "req-reused")
		engine.ServeHTTP(httptest.NewRecorder(), request)
	}
	if len(recorder.entries) != 2 || recorder.entries[0].EventID == recorder.entries[1].EventID {
		t.Fatalf("distinct attempts must have distinct event IDs: %+v", recorder.entries)
	}
}

func TestOperationAuditOwnsRegisteredManagementRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "Operator"},
		}))
		c.Next()
	})
	engine.Use(OperationAudit(recorder))
	engine.POST("/operator/register", func(c *gin.Context) {
		if !operationaudit.ManagementAuditOwned(c.Request.Context()) {
			t.Error("registered management request must be owned by HTTP Audit")
		}
		c.Status(http.StatusCreated)
	})
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/operator/register", nil))
	if len(recorder.entries) != 1 {
		t.Fatalf("one completed management attempt is required: %+v", recorder.entries)
	}
}

func TestOperationAuditRecordsManagedFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "Operator"},
		}))
		c.Next()
	})
	engine.Use(OperationAudit(recorder))
	engine.POST("/operator/register", func(c *gin.Context) {
		c.Status(http.StatusInternalServerError)
	})
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/operator/register", nil))
	if len(recorder.entries) != 1 || recorder.entries[0].Outcome != "failure" {
		t.Fatalf("terminal failure must be observed: %+v", recorder.entries)
	}
}

func TestOperationAuditRecordsAuthenticationDenialWithoutClaimedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(OperationAudit(recorder))
	engine.Use(func(c *gin.Context) { c.Status(http.StatusUnauthorized); c.Abort() })
	engine.POST("/operator/register", func(c *gin.Context) { t.Fatal("denied request reached handler") })
	request := httptest.NewRequest(http.MethodPost, "/operator/register", strings.NewReader(`{"operator_id":"claimed-operator"}`))
	request.Header.Set("Authorization", "Bearer unverified-claim")
	request.Header.Set(infra.HeaderBKNRequestID, "req-denied")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if len(recorder.entries) != 1 {
		t.Fatalf("denied management attempt entries = %+v, want one", recorder.entries)
	}
	entry := recorder.entries[0]
	if entry.Outcome != "denied" || entry.ActorID != "anonymous" || entry.ActorType != "anonymous" || entry.AuthMethod != "unknown" || entry.HTTPStatus != http.StatusUnauthorized || entry.TargetID != "operator:req-denied" {
		t.Fatalf("denied management attempt = %+v, want verified anonymous denial", entry)
	}
}

func TestOperationAuditRecordsVerifiedAuthorizationDenial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(OperationAudit(recorder))
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "verified-user", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "Verified User"},
		}))
		c.Next()
	})
	engine.POST("/operator/register", func(c *gin.Context) { c.Status(http.StatusForbidden) })
	request := httptest.NewRequest(http.MethodPost, "/operator/register", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if len(recorder.entries) != 1 || recorder.entries[0].ActorID != "verified-user" || recorder.entries[0].Outcome != "denied" {
		t.Fatalf("verified authorization denial = %+v", recorder.entries)
	}
}

func TestPrivateManagementAuditDoesNotTrustClaimedAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(OperationAuditPrivate(recorder))
	engine.Use(func(c *gin.Context) {
		// The internal-v1 middleware currently constructs this context solely
		// from caller-supplied X-Account-ID/X-Account-Type headers.
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "forged-user", AccountType: interfaces.AccessorTypeUser,
		}))
		c.Next()
	})
	engine.POST("/api/agent-operator-integration/internal-v1/operator/register", func(c *gin.Context) {
		if !operationaudit.ManagementAuditOwned(c.Request.Context()) {
			t.Error("private management request must be owned by HTTP Audit")
		}
		c.Status(http.StatusCreated)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-operator-integration/internal-v1/operator/register", strings.NewReader(`{"operator_id":"claimed-target"}`))
	request.Header.Set(infra.HeaderBKNRequestID, "req-private-register")
	request.Header.Set("X-Account-ID", "forged-user")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if len(recorder.entries) != 1 {
		t.Fatalf("private management entries = %+v, want one", recorder.entries)
	}
	entry := recorder.entries[0]
	if entry.ActorID != "anonymous" || entry.ActorType != "anonymous" || entry.AuthMethod != "unknown" || entry.TargetID != "operator:req-private-register" || entry.Outcome != "success" || entry.SourceChannel != "unknown" {
		t.Fatalf("untrusted private management projection = %+v", entry)
	}
}

func TestPrivateCategoryManagementAuditRules(t *testing.T) {
	for _, tc := range []struct{ method, path, action string }{
		{http.MethodPost, "/operator/category", "create"},
		{http.MethodPut, "/operator/category/:category_type", "update"},
		{http.MethodDelete, "/operator/category/:category_type", "delete"},
	} {
		rule, ok := registeredPrivateOperationAudit(tc.method, tc.path)
		if !ok || rule.Action != tc.action || rule.TargetType != "operator_category" {
			t.Fatalf("private %s %s = %+v, registered=%t", tc.method, tc.path, rule, ok)
		}
	}
}

func TestOperationAuditDoesNotCallPartialOpenAPIBundleFullySuccessful(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &capturedExecutionAuditRecorder{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{VisitorName: "Operator"},
		}))
		c.Next()
	})
	engine.Use(OperationAudit(recorder))
	engine.POST("/capabilities/openapi-bundle", func(c *gin.Context) {
		c.Set("bkn.operation_audit.partial_bundle", true)
		c.Status(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodPost, "/capabilities/openapi-bundle", nil)
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if len(recorder.entries) != 1 || recorder.entries[0].Outcome != "unknown" || recorder.entries[0].TargetID != "capability_bundle:"+recorder.entries[0].RequestID {
		t.Fatalf("partial bundle Audit = %+v, want one request-level unknown outcome", recorder.entries)
	}
}
