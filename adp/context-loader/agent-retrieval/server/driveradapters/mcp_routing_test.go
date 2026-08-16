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
	// 本用例测的是分流顺序，不是执行总闸；闸另有专门用例。
	t.Setenv("EXECUTE_SKILL_ENABLED", "true")

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
	// 闸开着但装配失败——那是故障，要 503；闸关着是另一回事，见 TestPTCRouteHonorsExecutionGate。
	t.Setenv("EXECUTE_SKILL_ENABLED", "true")
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

// run_code / run_shell 是一条沙箱执行通道，且比 execute_skill 更宽——后者只能跑
// 已注册技能的入口命令，这两个跑的是调用方现写的任意 Python 与 shell。所以它必须
// 服从同一道总闸 EXECUTE_SKILL_ENABLED（默认关），否则一个从没设过它的存量部署
// 升级后，凭 bearer 令牌就白拿了命令执行能力。
func TestPTCRouteHonorsExecutionGate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 默认（未设环境变量）即为关闭。
	t.Setenv("EXECUTE_SKILL_ENABLED", "")
	t.Setenv("MCP_EXECUTE_SKILL_ENABLED", "")
	if ptcExecutionEnabled() {
		t.Fatal("未设环境变量时执行总闸应为关")
	}
	// 关闭时不该装配出 handler 来。
	if newPTCMCPHandlerOrNil(nil) != nil {
		t.Fatal("总闸关闭时不应装配 PTC 端点")
	}

	var ptcHit, mcpHit bool
	handler := &restPublicHandler{
		MCPHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mcpHit = true
			w.WriteHeader(http.StatusOK)
		}),
		// 即便有人把 handler 塞了进来，闸关着也不能放行。
		PTCMCPHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			ptcHit = true
			w.WriteHeader(http.StatusOK)
		}),
	}
	engine := gin.New()
	engine.Any("/api/agent-retrieval/v1/mcp/*path", handler.handleMCP)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/agent-retrieval/v1/mcp/ptc", nil))
	// 404 而不是 503：503 等于向探测者承认这里本该有一条执行通道。
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("总闸关闭时应返回 404，得到 %d", recorder.Code)
	}
	if ptcHit {
		t.Fatal("总闸关闭时不应走到 PTC handler")
	}
	if mcpHit {
		t.Fatal("总闸关闭时不应回落到主 MCP Server")
	}

	// 打开后恢复正常。
	t.Setenv("EXECUTE_SKILL_ENABLED", "true")
	if !ptcExecutionEnabled() {
		t.Fatal("EXECUTE_SKILL_ENABLED=true 时总闸应为开")
	}
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/agent-retrieval/v1/mcp/ptc", nil))
	if !ptcHit || recorder.Code != http.StatusOK {
		t.Fatalf("总闸打开后应正常放行，code=%d hit=%v", recorder.Code, ptcHit)
	}
}

// 工具包与 info 是文档，不执行任何东西，所以不随总闸关闭——studio 的 PTC 模式自己
// 打执行工厂（那侧有 #345 的 execute 权限判定），把这两个也关掉会平白弄坏它。
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
