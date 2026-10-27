package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	infraCommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/sandbox"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

type executionErrorSessionPool struct {
	sandbox.SessionPool
	response *interfaces.ExecuteCodeResp
	err      error
}

func (p executionErrorSessionPool) ExecuteCode(context.Context, *interfaces.ExecuteCodeReq) (*interfaces.ExecuteCodeResp, error) {
	return p.response, p.err
}

func TestPublicFunctionExecuteErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *interfaces.ExecuteCodeResp
		err      error
		status   int
		contains string
	}{
		{name: "validation", err: infraErr.DefaultHTTPError(context.Background(), 422, map[string]any{"reason": "code_too_long", "stage": "validation", "actual": 102401, "max": 102400, "unit": "unicode_characters"}), status: 422, contains: "code_too_long"},
		{name: "timeout", response: &interfaces.ExecuteCodeResp{Status: "timeout", SessionID: "sess-1", ID: "exec-1"}, status: 504, contains: "execution_timeout"},
		{name: "startup failure", response: &interfaces.ExecuteCodeResp{Status: "failed", ExitCode: -1, Stderr: "[Errno 7] Argument list too long"}, status: 200, contains: "Argument list too long"},
		{name: "success", response: &interfaces.ExecuteCodeResp{Status: "completed", ReturnValue: map[string]any{"ok": true}}, status: 200, contains: `"ok":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			auth := mocks.NewMockIAuthorizationService(ctrl)
			auth.EXPECT().OperationCheckAll(gomock.Any(), gomock.Any(), "adhoc", interfaces.AuthResourceTypeFunction, interfaces.AuthOperationTypeExecute).Return(true, nil)
			handler := &unifiedProxyHandler{Logger: logger.DefaultLogger(), AuthService: auth, SessionPool: executionErrorSessionPool{response: tc.response, err: tc.err}}
			router := gin.New()
			router.POST("/function/execute", func(c *gin.Context) {
				ctx := infraCommon.SetPublicAPIToCtx(c.Request.Context(), true)
				ctx = infraCommon.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{AccountID: "caller"})
				c.Request = c.Request.WithContext(ctx)
				handler.FunctionExecute(c)
			})
			rec := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/function/execute", strings.NewReader(`{"code":"print(1)","event":{},"language":"python","timeout":2}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, request)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.contains) {
				t.Fatalf("response=%d %s, want %d %s", rec.Code, rec.Body, tc.status, tc.contains)
			}
			if tc.status == 504 {
				var envelope struct {
					Details  string         `json:"error_details"`
					Metadata map[string]any `json:"metadata"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Details != "function execution timed out after 2 seconds" || envelope.Metadata["timeout_seconds"] != float64(2) || envelope.Metadata["execution_id"] != "exec-1" || envelope.Metadata["session_id"] != "sess-1" {
					t.Fatalf("timeout diagnostics=%s", rec.Body)
				}
			}
		})
	}
}

func TestFunctionTimeoutExcludesInvalidCorrelation(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/function/execute", nil)
	replyFunctionExecutionResult(ctx, &interfaces.ExecuteCodeResp{Status: "timeout", ID: "http://internal", SessionID: "token secret"}, 2)
	if strings.Contains(recorder.Body.String(), "http://internal") || strings.Contains(recorder.Body.String(), "token secret") {
		t.Fatalf("leaked IDs: %s", recorder.Body)
	}
}
