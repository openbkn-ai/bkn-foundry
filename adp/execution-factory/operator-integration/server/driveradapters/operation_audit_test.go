package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
	infra "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

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
	request.Header.Set(string(interfaces.HeaderXBusinessDomain), "domain-a")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if len(recorder.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(recorder.entries))
	}
	if recorder.entries[0].RequestID == "" || response.Header().Get(infra.HeaderBKNRequestID) == "" {
		t.Fatalf("request id must be generated and returned: %+v", recorder.entries[0])
	}
}
