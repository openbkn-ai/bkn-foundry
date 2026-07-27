package drivenadapters

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

// mcp-go v0.37.0 两种传输在鉴权被拒时产生的错误文本（含本仓 performHandshake 的包装前缀）
const (
	sseUnauthorizedErr    = "failed to start MCP client: unexpected status code: 401"
	streamForbiddenErr    = `failed to initialize MCP client: request failed with status 403: {"message":"forbidden"}`
	connectionRefusedErr  = "failed to start MCP client: failed to connect to SSE stream: Get \"http://127.0.0.1:1/sse\": dial tcp 127.0.0.1:1: connect: connection refused"
	proxyAuthRequiredErr  = "failed to start MCP client: unexpected status code: 407"
	upstreamServerFailErr = "failed to initialize MCP client: request failed with status 500: internal error"
)

func TestUpstreamHTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "sse transport", err: errStr(sseUnauthorizedErr), want: http.StatusUnauthorized},
		{name: "streamable http transport", err: errStr(streamForbiddenErr), want: http.StatusForbidden},
		{name: "no status in message", err: errStr(connectionRefusedErr), want: 0},
		{name: "nil error", err: nil, want: 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := upstreamHTTPStatus(c.err); got != c.want {
				t.Fatalf("upstreamHTTPStatus() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestClassifyMCPConnErrorAuthFailed 复现「外部 MCP 返回 401/403 被报成服务不可访问」的缺陷
func TestClassifyMCPConnErrorAuthFailed(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus string
	}{
		{name: "sse 401", err: errStr(sseUnauthorizedErr), wantStatus: "401"},
		{name: "stream 403", err: errStr(streamForbiddenErr), wantStatus: "403"},
		{name: "proxy 407", err: errStr(proxyAuthRequiredErr), wantStatus: "407"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			httpErr := requireHTTPError(t, classifyMCPConnError(context.Background(), "https://api.githubcopilot.com/mcp/", c.err))

			if httpErr.HTTPCode != http.StatusBadRequest {
				t.Errorf("HTTPCode = %d, want %d", httpErr.HTTPCode, http.StatusBadRequest)
			}
			if !strings.HasSuffix(httpErr.Code, string(errors.ErrExtMCPServerAuthFailed)) {
				t.Errorf("Code = %s, want suffix %s", httpErr.Code, errors.ErrExtMCPServerAuthFailed)
			}
			if strings.Contains(httpErr.Code, "InternalServerError") {
				t.Errorf("Code = %s, should not be classified as InternalServerError", httpErr.Code)
			}
			if !strings.Contains(httpErr.Description, c.wantStatus) {
				t.Errorf("Description = %q, want upstream status %s in it", httpErr.Description, c.wantStatus)
			}
			if strings.Contains(httpErr.Solution, "是否正常运行") {
				t.Errorf("Solution = %q, should not point at server liveness", httpErr.Solution)
			}
		})
	}
}

// TestClassifyMCPConnErrorNotAccessible 真正不可达/上游 5xx 时仍归类为不可访问，HTTP 状态码在 errCodeMap 内
func TestClassifyMCPConnErrorNotAccessible(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{name: "connection refused", err: errStr(connectionRefusedErr)},
		{name: "upstream 500", err: errStr(upstreamServerFailErr)},
	} {
		t.Run(c.name, func(t *testing.T) {
			httpErr := requireHTTPError(t, classifyMCPConnError(context.Background(), "http://127.0.0.1:1", c.err))

			if httpErr.HTTPCode != http.StatusServiceUnavailable {
				t.Errorf("HTTPCode = %d, want %d", httpErr.HTTPCode, http.StatusServiceUnavailable)
			}
			if !strings.Contains(httpErr.Code, "ServiceUnavailable") {
				t.Errorf("Code = %s, want ServiceUnavailable segment", httpErr.Code)
			}
			if !strings.HasSuffix(httpErr.Code, string(errors.ErrExtMCPServerNotAccessible)) {
				t.Errorf("Code = %s, want suffix %s", httpErr.Code, errors.ErrExtMCPServerNotAccessible)
			}
		})
	}
}

// TestNewMCPClientModeNotSupported 模式不支持属于参数错误，不应被折叠成不可访问
func TestNewMCPClientModeNotSupported(t *testing.T) {
	_, err := NewMCPClient(context.Background(), &interfaces.MCPCoreConfigInfo{
		Mode: interfaces.MCPModeStdioNpx,
		URL:  "http://127.0.0.1:1",
	})
	httpErr := requireHTTPError(t, err)

	if !strings.HasSuffix(httpErr.Code, string(errors.ErrExtMCPModeNotSupported)) {
		t.Errorf("Code = %s, want suffix %s", httpErr.Code, errors.ErrExtMCPModeNotSupported)
	}
	if httpErr.HTTPCode != http.StatusBadRequest {
		t.Errorf("HTTPCode = %d, want %d", httpErr.HTTPCode, http.StatusBadRequest)
	}
}

func requireHTTPError(t *testing.T, err error) *errors.HTTPError {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	httpErr, ok := err.(*errors.HTTPError)
	if !ok {
		t.Fatalf("error type = %T, want *errors.HTTPError", err)
	}
	return httpErr
}

type errStr string

func (e errStr) Error() string { return string(e) }
