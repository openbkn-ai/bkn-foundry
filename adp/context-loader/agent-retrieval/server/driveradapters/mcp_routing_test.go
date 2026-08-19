// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// There is a sequence trap in the diversion under mcp catch-all: /ptc is a **prefix** match (mcp-go itself also matches by prefix.
// path), ranking it before exact matching, /mcp/ptc/toolkit will be treated as an MCP call and handed over to PTC.
// Server, then 404 - silent, looks like the endpoint is not online.
//
// The real MCP Server is not constructed here: to assemble it, you need to connect to the downstream. Use probes to replace both handlers and only check the shunt.
func newRoutingProbe(t *testing.T) (*gin.Engine, *string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var hit string
	handler := &restPublicHandler{
		MCPHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hit = "mcp"
			w.WriteHeader(http.StatusOK)
		}),
		PTCMCPHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hit = "ptc"
			w.WriteHeader(http.StatusOK)
		}),
	}

	engine := gin.New()
	engine.Any("/api/agent-retrieval/v1/mcp/*path", func(c *gin.Context) {
		switch c.Param("path") {
		case ptcToolkitPath, legacyToolkitPath:
			// handlePTCToolkit will go to the rendering toolkit. Here we only care about whether the shunt has reached it.
			hit = "toolkit"
			c.Status(http.StatusOK)
		case ptcInfoPath:
			hit = "ptc-info"
			c.Status(http.StatusOK)
		case mcpInfoPath:
			hit = "mcp-info"
			c.Status(http.StatusOK)
		default:
			handler.handleMCP(c)
		}
	})
	return engine, &hit
}

func TestMCPRoutingDispatch(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
	}{
		// The exact match must precede the /ptc prefix, otherwise these two items will be swallowed by the PTC Server and a 404 will be issued.
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/ptc/toolkit", "toolkit"},
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/ptc/info", "ptc-info"},
		// The old alias: studio is already in use. If you remove it, the online front-end will be damaged directly.
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/toolkit", "toolkit"},
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/info", "mcp-info"},
		// Two MCP Servers. POST is the JSON-RPC main channel.
		{http.MethodPost, "/api/agent-retrieval/v1/mcp/ptc", "ptc"},
		{http.MethodPost, "/api/agent-retrieval/v1/mcp/", "mcp"},
		// Streamable HTTP also uses GET to open the SSE stream and DELETE to terminate the session. It cannot only allow POST.
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/ptc", "ptc"},
		{http.MethodDelete, "/api/agent-retrieval/v1/mcp/ptc", "ptc"},
	}

	for _, c := range cases {
		engine, hit := newRoutingProbe(t)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(c.method, c.path, nil))
		if *hit != c.want {
			t.Errorf("%s %s: 落到了 %q，期望 %q（状态码 %d）", c.method, c.path, *hit, c.want, recorder.Code)
		}
	}
}

// PTC assembly failure should only cause PTC routing to report 503: The main tool surface and all REST endpoints have nothing to do with it.
// They cannot be dragged down together.
func TestPTCRouteFailsClosedWithoutHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var mcpHit bool
	handler := &restPublicHandler{
		MCPHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mcpHit = true
			w.WriteHeader(http.StatusOK)
		}),
		PTCMCPHandler: nil,
	}
	engine := gin.New()
	engine.Any("/api/agent-retrieval/v1/mcp/*path", handler.handleMCP)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/agent-retrieval/v1/mcp/ptc", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("PTC 不可用时应返回 503，得到 %d", recorder.Code)
	}
	// Critical: Cannot fall back to the primary MCP Server. The tool interface there is completely different, the client will get twenty businesses.
	// Tools instead of run_code, silently swap out access semantics.
	if mcpHit {
		t.Fatal("PTC 不可用时不应回落到主 MCP Server")
	}

	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/agent-retrieval/v1/mcp/", nil))
	if !mcpHit {
		t.Fatal("PTC 装配失败不应影响主 MCP 端点")
	}
}

// The PTC endpoint is always open and is not subject to EXECUTE_SKILL_ENABLED.
//
// This is a clear product decision: the switch is semantically a gate for skill execution, while run_code / run_shell is another.
// Ability, sharing a switch will force people who want to enable skill execution to enable arbitrary code execution together, and vice versa.
// The trade-off is that there is no means of shutting down the PTC, so isolation on the sandbox side is a must.
func TestPTCRouteIsNotGatedByExecuteSkill(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("EXECUTE_SKILL_ENABLED", "")
	t.Setenv("MCP_EXECUTE_SKILL_ENABLED", "")

	engine, hit := newRoutingProbe(t)
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		*hit = ""
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(method, "/api/agent-retrieval/v1/mcp/ptc", nil))
		if *hit != "ptc" {
			t.Fatalf("%s 未放行到 PTC（落到 %q，状态 %d）", method, *hit, recorder.Code)
		}
	}
}

// The toolkit and info are documents and do not execute anything - nor the endpoints themselves, nor are they affected by any switches.
func TestPTCToolkitNotGatedByExecutionSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("EXECUTE_SKILL_ENABLED", "")
	t.Setenv("MCP_EXECUTE_SKILL_ENABLED", "")

	engine, hit := newRoutingProbe(t)
	for _, path := range []string{
		"/api/agent-retrieval/v1/mcp/ptc/toolkit",
		"/api/agent-retrieval/v1/mcp/toolkit",
	} {
		*hit = ""
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if *hit != "toolkit" {
			t.Fatalf("%s 不应受执行总闸影响，落到了 %q", path, *hit)
		}
	}
}
