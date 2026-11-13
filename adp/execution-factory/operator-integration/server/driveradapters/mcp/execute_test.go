// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

func TestCallMCPToolPreservesLargeIntegerArguments(t *testing.T) {
	service := mocks.NewMockIMCPService(gomock.NewController(t))
	service.EXPECT().CallMCPTool(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *interfaces.MCPProxyCallToolRequest) (*interfaces.MCPProxyCallToolResponse, error) {
			value := req.Parameters["payload"].(map[string]any)["large_integer"]
			if value != json.Number("9007199254740993") {
				t.Fatalf("MCP argument large_integer = %#v (%T)", value, value)
			}
			return &interfaces.MCPProxyCallToolResponse{}, nil
		})
	handler := &mcpHandle{mcpService: service}
	engine := gin.New()
	engine.POST("/mcp/proxy/:mcp_id/tool/call", handler.CallMCPTool)
	request := httptest.NewRequest(http.MethodPost, "/mcp/proxy/mcp-1/tool/call",
		strings.NewReader(`{"tool_name":"echo","parameters":{"payload":{"large_integer":9007199254740993}}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("user_id", "user-1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

// The proxy listing reads the draft flag from the query string (#1523); without the flag, or with
// any other query parameter a client already sends, the request is the default listing.
func TestGetMCPToolsBindsTheDraftFlag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[string]bool{
		"/mcp/proxy/mcp-1/tools?draft=true":                 true,
		"/mcp/proxy/mcp-1/tools?draft=false":                false,
		"/mcp/proxy/mcp-1/tools":                            false,
		"/mcp/proxy/mcp-1/tools?page=1&page_size=100&all=1": false,
	}
	for target, wantDraft := range cases {
		t.Run(target, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			service := mocks.NewMockIMCPService(ctrl)
			var got *interfaces.MCPProxyToolListRequest
			service.EXPECT().GetMCPTools(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req *interfaces.MCPProxyToolListRequest) (*interfaces.MCPProxyToolListResponse, error) {
					got = req
					return &interfaces.MCPProxyToolListResponse{}, nil
				})
			handler := &mcpHandle{mcpService: service}
			engine := gin.New()
			engine.GET("/mcp/proxy/:mcp_id/tools", handler.GetMCPTools)

			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			if got == nil || got.MCPID != "mcp-1" || got.Draft != wantDraft {
				t.Fatalf("request = %+v, want mcp_id mcp-1 and draft %v", got, wantDraft)
			}
		})
	}
}
