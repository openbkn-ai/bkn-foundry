// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// functionExecuteServer answers /v1/function/execute with status and body, the
// way Execution Factory reports a rejected execution.
func functionExecuteServer(t *testing.T, status int, body string) (*operatorIntegrationClient, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	return &operatorIntegrationClient{
		logger:     logger,
		baseURL:    srv.URL + "/api/agent-operator-integration",
		httpClient: rest.NewHTTPClientWithOptions(rest.HTTPClientOptions{TimeOut: 5}),
	}, srv.URL
}

func executeDateScript(t *testing.T, client *operatorIntegrationClient) error {
	t.Helper()
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	ctx = common.SetRawTokenToCtx(ctx, "caller-token")
	_, err := client.ExecuteFunction(ctx, &interfaces.ExecuteFunctionRequest{
		Code:     "import datetime\nprint(datetime.datetime.now().year)",
		Language: "python",
	})
	return err
}

// A missing type-level execute grant and an authorization outage both stop the
// code from running, but only the first is fixed by asking for a grant. They used
// to reach the caller as the same 403 "调用沙箱执行失败。". See #1533.
func TestExecuteFunction_DeniedExecutionNamesTheMissingGrant(t *testing.T) {
	client, internalURL := functionExecuteServer(t, http.StatusForbidden,
		`{"code":"AgentOperatorIntegration.Forbidden.CommonUseForbidden",`+
			`"description":"权限不足，您暂无使用该资源的权限，请联系管理员申请授权","solution":"请联系管理员","link":"无",`+
			`"details":{"resource_type":"operator","resource_id":"*","operation":"execute"}}`)

	he := asHTTPError(t, executeDateScript(t, client))

	if he.HTTPCode != http.StatusForbidden || he.ErrorCode != "Public.Forbidden" {
		t.Fatalf("error = %d %s, want 403 Public.Forbidden", he.HTTPCode, he.ErrorCode)
	}
	details, _ := he.ErrorDetails.(string)
	if !strings.HasPrefix(details, "AgentOperatorIntegration.Forbidden.CommonUseForbidden: ") {
		t.Errorf("details = %q, want the downstream code first", details)
	}
	if !strings.Contains(details, "execute") || strings.Contains(details, "调用沙箱执行失败") {
		t.Errorf("details = %q, want the missing execute grant, not the generic sandbox failure", details)
	}
	if strings.Contains(he.Error(), internalURL) {
		t.Errorf("error exposes the internal service URL: %s", he.Error())
	}
}

func TestExecuteFunction_AuthorizationOutageIsNotReportedAsDenial(t *testing.T) {
	client, internalURL := functionExecuteServer(t, http.StatusServiceUnavailable,
		`{"code":"AgentOperatorIntegration.ServiceUnavailable.CommonAuthorizationUnavailable",`+
			`"description":"权限校验服务暂时不可用，未能完成授权判定，请求未执行","solution":"请稍后重试","link":"无"}`)

	he := asHTTPError(t, executeDateScript(t, client))

	if he.HTTPCode != http.StatusServiceUnavailable || he.ErrorCode != "Public.ServiceUnavailable" {
		t.Fatalf("error = %d %s, want 503 Public.ServiceUnavailable", he.HTTPCode, he.ErrorCode)
	}
	details, _ := he.ErrorDetails.(string)
	if !strings.HasPrefix(details, "AgentOperatorIntegration.ServiceUnavailable.CommonAuthorizationUnavailable: ") {
		t.Errorf("details = %q, want the downstream code first", details)
	}
	if strings.Contains(he.Error(), internalURL) {
		t.Errorf("error exposes the internal service URL: %s", he.Error())
	}
}

// Other downstream 4xx keep their status and stable code, but only the localized
// description is carried through: Execution Factory details can hold internal
// addresses or raw dependency responses.
func TestExecuteFunction_OtherRejectionsCarryCodeButNotRawDetails(t *testing.T) {
	const leak = "dial tcp 10.43.0.17:8080: connect: connection refused"
	client, internalURL := functionExecuteServer(t, http.StatusBadRequest,
		`{"code":"AgentOperatorIntegration.BadRequest.DebugParamsInvalid",`+
			`"description":"调试传参错误，必须为JSON格式","solution":"请检查参数是否正确","link":"无",`+
			`"details":"`+leak+`"}`)

	he := asHTTPError(t, executeDateScript(t, client))

	if he.HTTPCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", he.HTTPCode)
	}
	want := "AgentOperatorIntegration.BadRequest.DebugParamsInvalid: 调试传参错误，必须为JSON格式"
	if he.ErrorDetails != want {
		t.Errorf("details = %#v, want %q", he.ErrorDetails, want)
	}
	if strings.Contains(he.Error(), leak) || strings.Contains(he.Error(), internalURL) {
		t.Errorf("error leaks downstream internals: %s", he.Error())
	}
}

// A 503 that is not the authorization gate (for example the sandbox) and any 5xx
// remain a dependency fault reported as 502.
func TestExecuteFunction_UpstreamFaultStaysBadGateway(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"sandbox 503", http.StatusServiceUnavailable,
			`{"code":"Public.ServiceUnavailable","description":"服务端暂时不可用","details":"sandbox control plane at http://10.43.0.9"}`},
		{"500", http.StatusInternalServerError,
			`{"code":"AgentOperatorIntegration.InternalServerError.SandboxControlPlaneFailed","description":"依赖沙箱服务异常"}`},
		{"non-envelope body", http.StatusBadGateway, `<html>bad gateway</html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := functionExecuteServer(t, tc.status, tc.body)

			he := asHTTPError(t, executeDateScript(t, client))

			if he.HTTPCode != http.StatusBadGateway || he.ErrorCode != "Public.BadGateway" {
				t.Fatalf("error = %d %s, want 502 Public.BadGateway", he.HTTPCode, he.ErrorCode)
			}
			if strings.Contains(he.Error(), "10.43.0.9") {
				t.Errorf("error leaks downstream internals: %s", he.Error())
			}
		})
	}
}

func TestExecutionFactoryErrorReadsSharedAndLegacyEnvelopes(t *testing.T) {
	for _, codeKey := range []string{"error_code", "code"} {
		body := fmt.Sprintf(`{"%s":"AgentOperatorIntegration.Forbidden.CommonUseForbidden","description":"execute denied"}`, codeKey)
		dependency := &infraErr.DependencyError{
			Err:  infraErr.DefaultHTTPError(context.Background(), http.StatusForbidden, nil),
			Body: []byte(body),
		}
		code, description := executionFactoryError(fmt.Errorf("function failed: %w", dependency))
		if code != "AgentOperatorIntegration.Forbidden.CommonUseForbidden" || description != "execute denied" {
			t.Fatalf("%s: code=%q description=%q", codeKey, code, description)
		}
	}
}

func TestExecuteFunction_ExecutionFailuresKeepSafeClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantCode, reason, contains string
		status                                 int
	}{
		{"canonical code too long", `{"error_code":"Public.UnprocessableEntity","description":"secret-token http://internal","error_details":{"reason":"code_too_long","stage":"validation","actual":102401,"max":102400,"unit":"unicode_characters","input":"secret-token"}}`, "Public.UnprocessableEntity", "code_too_long", "102401", 422},
		{"canonical execution timeout", `{"error_code":"Public.GatewayTimeout","error_details":"function execution timed out after 2 seconds","metadata":{"reason":"execution_timeout","stage":"execution","timeout_seconds":2,"session_id":"sess-1","execution_id":"exec-1"}}`, "Public.GatewayTimeout", "execution_timeout", "2", 504},
		{"code too long", `{"code":"Public.UnprocessableEntity","description":"secret-token http://internal","details":{"reason":"code_too_long","stage":"validation","actual":102401,"max":102400,"unit":"unicode_characters","input":"secret-token"}}`, "Public.UnprocessableEntity", "code_too_long", "102401", 422},
		{"unknown validation", `{"code":"Public.UnprocessableEntity","description":"secret-token","details":{"reason":"other","input":"secret-token"}}`, "Public.UnprocessableEntity", "invalid_execution_input", "", 422},
		{"execution timeout", `{"code":"Public.GatewayTimeout","details":"secret-token http://internal","metadata":{"reason":"execution_timeout","stage":"execution","timeout_seconds":2,"session_id":"sess-1","execution_id":"exec-1","input":"secret-token"}}`, "Public.GatewayTimeout", "execution_timeout", "2", 504},
		{"legacy timeout unknown duration", `{"code":"Public.GatewayTimeout","details":"function execution timed out"}`, "Public.GatewayTimeout", "execution_timeout", "", 504},
		{"timeout unknown duration", `{"code":"Public.GatewayTimeout","metadata":{"reason":"execution_timeout","stage":"execution"}}`, "Public.GatewayTimeout", "execution_timeout", "", 504},
		{"legacy timeout", `{"code":"Public.GatewayTimeout","details":"function execution timed out after 2 seconds"}`, "Public.GatewayTimeout", "execution_timeout", "2", 504},
		{"unknown gateway", `{"code":"Public.GatewayTimeout","details":"secret-token http://internal"}`, "Public.BadGateway", "", "", 504},
		{"wrong timeout code", `{"code":"Other.GatewayTimeout","metadata":{"reason":"execution_timeout","stage":"execution","timeout_seconds":2}}`, "Public.BadGateway", "", "", 504},
		{"invalid numeric detail", `{"code":"Public.UnprocessableEntity","details":{"reason":"code_too_long","stage":"validation","actual":-1,"max":102400,"unit":"unicode_characters","input":"secret-token"}}`, "Public.UnprocessableEntity", "invalid_execution_input", "", 422},
		{"invalid correlation", `{"code":"Public.GatewayTimeout","metadata":{"reason":"execution_timeout","stage":"execution","timeout_seconds":2,"session_id":"http://internal","execution_id":"secret-token http://internal"}}`, "Public.GatewayTimeout", "execution_timeout", "2", 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, internalURL := functionExecuteServer(t, tc.status, tc.body)
			he := asHTTPError(t, executeDateScript(t, client))
			if he.ErrorCode != tc.wantCode {
				t.Fatalf("error=%v, want %s", he, tc.wantCode)
			}
			if tc.reason != "" && he.Metadata["reason"] != tc.reason {
				t.Fatalf("metadata=%v, want reason %s", he.Metadata, tc.reason)
			}
			if !strings.Contains(he.Error(), tc.contains) {
				t.Fatalf("error=%v, want %s", he, tc.contains)
			}
			for _, leak := range []string{"secret-token", "http://internal", internalURL} {
				if strings.Contains(he.Error(), leak) {
					t.Fatalf("error leaks %s: %v", leak, he)
				}
			}
			if tc.name == "execution timeout" && (he.Metadata["session_id"] != "sess-1" || he.Metadata["execution_id"] != "exec-1") {
				t.Fatalf("correlation lost: %v", he.Metadata)
			}
		})
	}
}

func TestExecuteFunction_StartupFailurePreservesExitAndStderr(t *testing.T) {
	client, _ := functionExecuteServer(t, http.StatusOK, `{"stdout":"","stderr":"[Errno 7] Argument list too long","exit_code":-1}`)
	ctx := common.SetRawTokenToCtx(context.Background(), "caller-token")
	resp, err := client.ExecuteFunction(ctx, &interfaces.ExecuteFunctionRequest{Code: "print(1)", Language: "python"})
	if err != nil || resp.ExitCode != -1 || !strings.Contains(resp.Stderr, "Argument list too long") {
		t.Fatalf("startup result=%+v, error=%v", resp, err)
	}
}

func TestFunctionExecutionErrorPreservesRequestCorrelation(t *testing.T) {
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "req_12345678"})
	cause := &infraErr.DependencyError{Body: []byte(`{"code":"Public.UnprocessableEntity","details":{"reason":"code_too_long","stage":"validation","actual":102401,"max":102400,"unit":"unicode_characters","session_id":"sess-1"}}`)}
	he := asHTTPError(t, classifyFunctionExecuteError(ctx, http.StatusUnprocessableEntity, cause))
	if he.Metadata["request_id"] != "req_12345678" || he.Metadata["session_id"] != "sess-1" {
		t.Fatalf("correlation lost: %v", he.Metadata)
	}
}
