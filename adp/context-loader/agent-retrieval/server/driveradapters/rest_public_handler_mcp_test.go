// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/mcp"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestMCPInfoBuildFailureIsLocalized(t *testing.T) {
	previous := buildMCPInfo
	buildMCPInfo = func(string, string) (*mcp.MCPInfo, error) { return nil, errors.New("embedded schema is invalid") }
	t.Cleanup(func() { buildMCPInfo = previous })
	gin.SetMode(gin.TestMode)

	for _, test := range []struct{ language, description string }{
		{"zh-CN", "MCP 服务信息暂不可用"},
		{"en-US", "MCP service information is unavailable"},
	} {
		t.Run(test.language, func(t *testing.T) {
			engine := gin.New()
			engine.Use(sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware())
			handler := &restPublicHandler{}
			engine.GET("/mcp/*path", handler.handleMCP)
			request := httptest.NewRequest(http.MethodGet, "/mcp/info", nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError || response.Header().Get(sharedrest.ContentLanguageHeader) != test.language || response.Header().Get("Vary") != sharedrest.AcceptLanguageHeader || response.Header().Get("Cache-Control") != "private, no-cache" {
				t.Fatalf("unexpected response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			if !contains(response.Body.String(), "MCPInfoBuildFailed") || !contains(response.Body.String(), test.description) || contains(response.Body.String(), "embedded schema is invalid") {
				t.Fatalf("unexpected error body: %s", response.Body.String())
			}
		})
	}
}

func TestPTCToolkitBuildFailureIsLocalized(t *testing.T) {
	previous := buildPTCToolkit
	buildPTCToolkit = func(string, int) (*mcp.PTCToolkit, error) { return nil, errors.New("embedded schema is invalid") }
	t.Cleanup(func() { buildPTCToolkit = previous })
	gin.SetMode(gin.TestMode)

	for _, test := range []struct{ language, description string }{
		{"zh-CN", "PTC MCP 工具包暂不可用"},
		{"en-US", "The PTC MCP toolkit is unavailable"},
	} {
		t.Run(test.language, func(t *testing.T) {
			engine := gin.New()
			engine.Use(sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware())
			handler := &restPublicHandler{}
			engine.GET("/mcp/*path", handler.handleMCP)
			request := httptest.NewRequest(http.MethodGet, "/mcp/ptc/toolkit", nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError || response.Header().Get(sharedrest.ContentLanguageHeader) != test.language || response.Header().Get("Vary") != sharedrest.AcceptLanguageHeader || response.Header().Get("Cache-Control") != "private, no-cache" {
				t.Fatalf("unexpected response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			if !contains(response.Body.String(), "MCPPTCToolkitBuildFailed") || !contains(response.Body.String(), test.description) || contains(response.Body.String(), "embedded schema is invalid") {
				t.Fatalf("unexpected error body: %s", response.Body.String())
			}
		})
	}
}

func contains(value, expected string) bool { return strings.Contains(value, expected) }
