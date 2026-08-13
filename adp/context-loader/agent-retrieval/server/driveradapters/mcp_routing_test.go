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

// mcp catch-all 之下的分流有一处顺序陷阱：/ptc 是**前缀**匹配（mcp-go 自己也按前缀
// 吃路径），把它排在精确匹配之前，/mcp/ptc/toolkit 会被当成一次 MCP 调用交给 PTC
// Server，然后 404——静默的，看起来就像端点没上线。
//
// 这里不构造真的 MCP Server：装配它要连下游。用探针替掉两个 handler，只验分流。
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
			// handlePTCToolkit 会去渲染工具包，这里只关心分流有没有走到它。
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
		// 精确匹配必须先于 /ptc 前缀，否则这两条被 PTC Server 吞掉后 404。
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/ptc/toolkit", "toolkit"},
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/ptc/info", "ptc-info"},
		// 旧别名：studio 已在用，摘掉会让线上前端直接坏。
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/toolkit", "toolkit"},
		{http.MethodGet, "/api/agent-retrieval/v1/mcp/info", "mcp-info"},
		// 两台 MCP Server。POST 是 JSON-RPC 主通道。
		{http.MethodPost, "/api/agent-retrieval/v1/mcp/ptc", "ptc"},
		{http.MethodPost, "/api/agent-retrieval/v1/mcp/", "mcp"},
		// Streamable HTTP 还会用 GET 开 SSE 流、DELETE 终止会话，不能只放行 POST。
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

// PTC 装配失败只该让 PTC 路由报 503：主工具面与全部 REST 端点跟它无关，
// 不能一起拖垮。
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
	// 关键：不能退回主 MCP Server。那边工具面完全不同，客户端会拿到二十个业务
	// 工具而不是 run_code，静默地换掉了接入语义。
	if mcpHit {
		t.Fatal("PTC 不可用时不应回落到主 MCP Server")
	}

	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/agent-retrieval/v1/mcp/", nil))
	if !mcpHit {
		t.Fatal("PTC 装配失败不应影响主 MCP 端点")
	}
}
