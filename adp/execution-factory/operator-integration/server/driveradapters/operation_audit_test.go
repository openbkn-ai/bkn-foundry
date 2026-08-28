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
	request.Header.Set("x-tenant-id", "tenant-a")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if len(recorder.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(recorder.entries))
	}
	if recorder.entries[0].RequestID == "" || response.Header().Get(infra.HeaderBKNRequestID) == "" {
		t.Fatalf("request id must be generated and returned: %+v", recorder.entries[0])
	}
}
