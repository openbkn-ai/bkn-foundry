package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// TestGenerateExternalConnectionInfo reproduces "The custom MCP was sent a certain 404 access address".
func TestGenerateExternalConnectionInfo(t *testing.T) {
	const mcpID = "43454db8-60c0-4f10-875f-29b3b42f6ae9"
	s := &mcpServiceImpl{}

	t.Run("tool imported 型返回平台侧接入地址", func(t *testing.T) {
		info := s.generateExternalConnectionInfo(mcpID, interfaces.MCPCreationTypeToolImported)
		if info == nil {
			t.Fatal("connection info = nil, want stream/sse url")
		}
		wantStream := "/api/agent-operator-integration/v1/mcp/app/" + mcpID + "/mcp"
		wantSSE := "/api/agent-operator-integration/v1/mcp/app/" + mcpID + "/sse"
		if info.StreamURL != wantStream {
			t.Errorf("StreamURL = %q, want %q", info.StreamURL, wantStream)
		}
		if info.SSEURL != wantSSE {
			t.Errorf("SSEURL = %q, want %q", info.SSEURL, wantSSE)
		}
	})

	t.Run("custom 型不返回接入地址", func(t *testing.T) {
		if info := s.generateExternalConnectionInfo(mcpID, interfaces.MCPCreationTypeCustom); info != nil {
			t.Errorf("connection info = %+v, want nil（代理型没有平台侧实例可服务）", info)
		}
	})

	t.Run("未知创建类型同样不返回接入地址", func(t *testing.T) {
		if info := s.generateExternalConnectionInfo(mcpID, interfaces.MCPCreationType("builtin")); info != nil {
			t.Errorf("connection info = %+v, want nil", info)
		}
	})
}

// TestMCPErrorCodesHaveDescription verifies registered MCP messages in both languages.
func TestMCPErrorCodesHaveDescription(t *testing.T) {
	codes := []errors.ErrorCode{
		errors.ErrExtMCPInstanceNotFound,
		errors.ErrExtMCPInstanceAlreadyExists,
		errors.ErrExtMCPServerEndpointUnsupported,
		errors.ErrExtMCPServerNotAccessible,
	}

	for _, lang := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		for _, code := range codes {
			err := errors.NewHTTPError(i18n.WithLanguage(context.Background(), lang), http.StatusBadRequest, code, nil, "endpoint")
			if renderErr := err.Render(); renderErr != nil {
				t.Fatalf("[%s] %s: %v", lang, code, renderErr)
			}
			if err.BaseError.Description == "" {
				t.Errorf("[%s] %s: empty description", lang, code)
			}
		}
	}
}
