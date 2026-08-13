// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeExecutor 记录最后一次沙箱执行请求。
type fakeExecutor struct {
	interfaces.DrivenOperatorIntegration
	last *interfaces.ExecuteFunctionRequest
	resp *interfaces.ExecuteFunctionResponse
	err  error
}

func (f *fakeExecutor) ExecuteFunction(
	_ context.Context, req *interfaces.ExecuteFunctionRequest,
) (*interfaces.ExecuteFunctionResponse, error) {
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &interfaces.ExecuteFunctionResponse{Stdout: "ok"}, nil
}

func ptcCallRequest(name string, args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

func ptcToolByName(t *testing.T, name string) PTCTool {
	t.Helper()
	for _, tool := range ptcTestToolkit(t).Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("工具表里没有 %s", name)
	return PTCTool{}
}

// 工作目录规则在两处实现：stub 里的 Python（run_code 走）和这里的 Go（run_shell 走）。
// 两边算不出同一个路径，两个工具就落在不同目录，彼此看不见对方写的文件——而且不会
// 报错，只会表现为「文件莫名其妙不在」。
func TestPTCWorkdirMatchesStubRule(t *testing.T) {
	cases := []struct {
		conversation string
		want         string
	}{
		{"conv_3767f54b17db900b31e554d2e9103cb6", "/workspace/conv-conv_3767f54b17db900b31e554d2e9103cb6"},
		{"", "/workspace/shared"},
		{"  ", "/workspace/shared"},
		// 归一化：路径分隔符与其他字符一律换成 -，不能让 conversation_id 逃出目录。
		{"../../etc/passwd", "/workspace/conv-------etc-passwd"},
		{"a/b", "/workspace/conv-a-b"},
		// Python 的 isalnum() 认 Unicode 而 Go 只认 ASCII；stub 因此写死 ASCII 白名单，
		// 两边对中文必须同样换成 -。
		{"名字", "/workspace/conv---"},
	}
	for _, c := range cases {
		got := ptcWorkdir(map[string]any{"conversation_id": c.conversation})
		if got != c.want {
			t.Errorf("conversation_id=%q: got %q want %q", c.conversation, got, c.want)
		}
	}

	// 截断到 64 个字符，与 stub 的 [:64] 对齐。
	long := strings.Repeat("x", 200)
	got := ptcWorkdir(map[string]any{"conversation_id": long})
	if got != "/workspace/conv-"+strings.Repeat("x", 64) {
		t.Fatalf("超长 conversation_id 未按 64 截断: %s", got)
	}

	// 拿不到 bkn_context 时不能 panic，退到共用目录。
	if got := ptcWorkdir(nil); got != "/workspace/shared" {
		t.Fatalf("空上下文应退到 shared，得到 %s", got)
	}
}

// 目录名会原样拼进 shell 命令，只允许 [A-Za-z0-9_-] 与路径前缀。
func TestPTCWorkdirIsShellSafe(t *testing.T) {
	for _, hostile := range []string{
		"a; rm -rf /", "$(whoami)", "`id`", "a && echo pwned", "a|b", "a\nb", "'x'", `"y"`,
	} {
		got := ptcWorkdir(map[string]any{"conversation_id": hostile})
		suffix := strings.TrimPrefix(got, "/workspace/conv-")
		for _, r := range suffix {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
			if !ok {
				t.Fatalf("conversation_id=%q 产出了不安全的目录名 %q", hostile, got)
			}
		}
	}
}

func TestPTCRunCodeWrapsIntoHandler(t *testing.T) {
	toolkit := ptcTestToolkit(t)
	executor := &fakeExecutor{}
	handler := handlePTCExecute(executor, toolkit, ptcToolByName(t, "run_code"))

	ctx := common.SetRawTokenToCtx(context.Background(), "tok-123")
	_, err := handler(ctx, ptcCallRequest("run_code", map[string]any{
		"code": "print(1)\n\nprint(2)",
		"bkn_context": map[string]any{
			"conversation_id": "conv_a", "interaction_id": "int_b",
		},
	}))
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	if executor.last.Language != "python" {
		t.Fatalf("language 应为 python: %s", executor.last.Language)
	}
	// 沙箱按 Lambda 规范执行，入口必须是单参数 handler，模型代码要缩进进去。
	if !strings.Contains(executor.last.Code, "def handler(event):") ||
		!strings.Contains(executor.last.Code, "    print(1)") ||
		!strings.Contains(executor.last.Code, "    print(2)") {
		t.Fatalf("代码未正确包成 handler:\n%s", executor.last.Code)
	}
	if !strings.Contains(executor.last.Code, "_configure(event)") {
		t.Fatal("未调用 _configure，工作目录与凭据都不会注入")
	}
	// 凭据与会话上下文走 event 而非 env_vars：沙箱会话池化复用，env 会把上一个
	// 调用方的值留在容器里。
	if executor.last.Event["token"] != "tok-123" {
		t.Fatalf("未下发调用方令牌: %v", executor.last.Event["token"])
	}
	if executor.last.Event["mcp"] != toolkit.SandboxMCPURL {
		t.Fatalf("未下发沙箱回访地址: %v", executor.last.Event["mcp"])
	}
}

// shell 不回访 MCP，就不该拿到令牌——沙箱里少一份可被读出来的凭据。
func TestPTCRunShellGetsNoToken(t *testing.T) {
	executor := &fakeExecutor{}
	handler := handlePTCExecute(executor, ptcTestToolkit(t), ptcToolByName(t, "run_shell"))

	ctx := common.SetRawTokenToCtx(context.Background(), "tok-123")
	if _, err := handler(ctx, ptcCallRequest("run_shell", map[string]any{
		"command": "ls -la",
		"bkn_context": map[string]any{
			"conversation_id": "conv_a", "interaction_id": "int_b",
		},
	})); err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	if _, leaked := executor.last.Event["token"]; leaked {
		t.Fatal("run_shell 不回访 MCP，不应下发令牌")
	}
	if executor.last.Language != "shell" {
		t.Fatalf("language 应为 shell: %s", executor.last.Language)
	}
	// 与 run_code 落在同一个目录，否则看不见对方写的文件。
	if !strings.HasPrefix(executor.last.Code, "mkdir -p /workspace/conv-conv_a && cd /workspace/conv-conv_a\n") {
		t.Fatalf("未先切到本次对话的工作目录:\n%s", executor.last.Code)
	}
	if !strings.HasSuffix(executor.last.Code, "ls -la") {
		t.Fatalf("命令未拼在后面:\n%s", executor.last.Code)
	}
}

// 退出码非 0 要标成工具错误，且报文照常带回——吞掉 stderr 调用方只能盲目重试。
func TestPTCExecuteSurfacesFailure(t *testing.T) {
	executor := &fakeExecutor{resp: &interfaces.ExecuteFunctionResponse{
		Stdout: "部分输出", Stderr: "ToolError: 字段不存在", ExitCode: 1,
	}}
	handler := handlePTCExecute(executor, ptcTestToolkit(t), ptcToolByName(t, "run_code"))

	result, err := handler(context.Background(), ptcCallRequest("run_code", map[string]any{
		"code":        "print(1)",
		"bkn_context": map[string]any{"conversation_id": "c", "interaction_id": "i"},
	}))
	if err != nil {
		t.Fatalf("不该返回传输层错误: %v", err)
	}
	if !result.IsError {
		t.Fatal("退出码非 0 应标为工具错误")
	}
	structured, _ := result.StructuredContent.(map[string]any)
	if structured["stderr"] != "ToolError: 字段不存在" {
		t.Fatalf("stderr 未带回: %v", structured)
	}
	if structured["stdout"] != "部分输出" {
		t.Fatalf("失败时 stdout 也要带回: %v", structured)
	}
}

// 空入参要在打沙箱之前拒掉，白跑一次执行既慢又占会话。
func TestPTCExecuteRejectsEmptyInput(t *testing.T) {
	for _, c := range []struct{ tool, key string }{
		{"run_code", "code"}, {"run_shell", "command"},
	} {
		executor := &fakeExecutor{}
		handler := handlePTCExecute(executor, ptcTestToolkit(t), ptcToolByName(t, c.tool))
		result, err := handler(context.Background(), ptcCallRequest(c.tool, map[string]any{
			c.key:         "   ",
			"bkn_context": map[string]any{"conversation_id": "c", "interaction_id": "i"},
		}))
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		if !result.IsError {
			t.Fatalf("%s: 空入参应报错", c.tool)
		}
		if executor.last != nil {
			t.Fatalf("%s: 不该打到沙箱", c.tool)
		}
	}
}

// MCP 客户端没有 studio 那层前端替它管会话，bkn_context 必须出现在入参 schema 里，
// 且是必填——否则模型不会传，每次调用都被生命周期守卫拦下。
func TestPTCSchemaRequiresBusinessContext(t *testing.T) {
	for _, tool := range ptcTestToolkit(t).Tools {
		var schema map[string]any
		if err := json.Unmarshal(ptcToolInputSchemaWithContext(tool.InputSchema), &schema); err != nil {
			t.Fatalf("%s: schema 不是合法 JSON: %v", tool.Name, err)
		}
		properties, _ := schema["properties"].(map[string]any)
		if _, ok := properties["bkn_context"]; !ok {
			t.Fatalf("%s: 入参缺少 bkn_context", tool.Name)
		}
		required, _ := schema["required"].([]any)
		found := false
		for _, item := range required {
			if item == "bkn_context" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: bkn_context 应为必填，required=%v", tool.Name, required)
		}
		// 原有必填项不能被顶掉。
		if tool.Name == "run_code" && len(required) < 2 {
			t.Fatalf("run_code 的 code 必填项丢了: %v", required)
		}
	}
}

// 工具包给 studio 用的 schema 里不该有 bkn_context（那边前端管会话），
// 补 bkn_context 只发生在 MCP 端点上。两者混淆会让 studio 的模型去填一个它没有的值。
func TestPTCToolkitSchemaStaysContextFree(t *testing.T) {
	for _, tool := range ptcTestToolkit(t).Tools {
		if strings.Contains(string(tool.InputSchema), "bkn_context") {
			t.Fatalf("%s 的工具包 schema 不应含 bkn_context: %s", tool.Name, tool.InputSchema)
		}
	}
}
