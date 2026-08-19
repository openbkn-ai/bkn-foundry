package mcp

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"
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

// TestMCPErrorCodesHaveDescription reproduces "The missing i18n entry causes the response body to spit out desc.<Key> directly".
func TestMCPErrorCodesHaveDescription(t *testing.T) {
	codes := []errors.ErrorCode{
		errors.ErrExtMCPInstanceNotFound,
		errors.ErrExtMCPInstanceAlreadyExists,
		errors.ErrExtMCPServerEndpointUnsupported,
		errors.ErrExtMCPServerNotAccessible,
	}

	for _, lang := range []string{"zh_CN", "en_US"} {
		tr := localize.NewI18nTranslator(lang)
		for _, code := range codes {
			key := "desc." + code.String()
			if got := tr.Trans(key); got == key {
				t.Errorf("[%s] %s 缺少文案，接口会直接返回 key", lang, key)
			}
		}
	}
}
