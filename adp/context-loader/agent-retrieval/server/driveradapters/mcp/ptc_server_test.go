// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestPTCMCPInitializeUsesRequestLocale(t *testing.T) {
	handler, err := NewPTCMCPHandlerWith(nil, &fakeExecutor{}, defaultPTCServicePort)
	if err != nil {
		t.Fatalf("create PTC MCP handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, ptcEndpointPath, strings.NewReader(`{
		"jsonrpc":"2.0",
		"id":1,
		"method":"initialize",
		"params":{
			"protocolVersion":"2025-06-18",
			"capabilities":{},
			"clientInfo":{"name":"locale-test","version":"1.0"}
		}
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get(server.HeaderKeySessionID) == "" {
		t.Fatal("initialize response did not return Mcp-Session-Id")
	}
	var payload struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if !strings.HasPrefix(payload.Result.Instructions, "This endpoint provides two execution tools") {
		t.Fatalf("instructions = %q, want English PTC instructions", payload.Result.Instructions)
	}
}

// fakeExecutor records the last sandbox execution request.
type fakeExecutor struct {
	interfaces.DrivenOperatorIntegration
	last     *interfaces.ExecuteFunctionRequest
	lastTool *interfaces.ExecutePublishedToolRequest
	resp     *interfaces.ExecuteFunctionResponse
	toolResp map[string]any
	err      error
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

func (f *fakeExecutor) ExecutePublishedTool(
	_ context.Context, req *interfaces.ExecutePublishedToolRequest,
) (map[string]any, error) {
	f.lastTool = req
	if f.err != nil {
		return nil, f.err
	}
	if f.toolResp != nil {
		return f.toolResp, nil
	}
	return map[string]any{"body": map[string]any{"exit_code": 0}}, nil
}

func TestPTCPublishedToolUsesCallerContext(t *testing.T) {
	executor := &fakeExecutor{toolResp: map[string]any{"body": map[string]any{"result": map[string]any{"leadtime_days": 14}}}}
	handler := handlePTCPublishedTool(executor, loadMCPLocaleBundle("en-US"))
	ctx := common.SetRawTokenToCtx(context.Background(), "caller-appkey")

	result, err := handler(ctx, ptcCallRequest("execute_published_tool", map[string]any{
		"toolbox_id": "box-1",
		"tool_id":    "tool-1",
		"arguments":  map[string]any{"material_code": "606-000989"},
		"bkn_context": map[string]any{
			"conversation_id": "conv-1", "interaction_id": "int-1",
		},
	}))
	if err != nil {
		t.Fatalf("call published tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("published-tool response marked error: %#v", result)
	}
	if executor.lastTool == nil {
		t.Fatal("published tool was not invoked")
	}
	if executor.lastTool.ToolboxID != "box-1" || executor.lastTool.ToolID != "tool-1" {
		t.Fatalf("wrong published tool target: %#v", executor.lastTool)
	}
	if executor.lastTool.BKNConversationID != "conv-1" || executor.lastTool.BKNInteractionID != "int-1" {
		t.Fatalf("managed context missing from published tool request: %#v", executor.lastTool)
	}
	if executor.lastTool.Parameters["material_code"] != "606-000989" {
		t.Fatalf("business parameters changed: %#v", executor.lastTool.Parameters)
	}
}

func ptcCallRequest(name string, args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	// When using JSON-RPC, mcp-go also fills in RawArguments, and GetRawArguments() returns it first.
	// (json.RawMessage, not map). Just assume that the Arguments request does not exist in the actual traffic, as usual.
	// The structure will allow bugs such as "getting the wrong field" to pass silently - that's how they were missed in the first place.
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	req.Params.RawArguments = raw
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

// Working directory rules are implemented in two places: Python in the stub (run_code goes) and Go here (run_shell goes).
// The two sides cannot calculate the same path, so the two tools are located in different directories, and each other cannot see the files written by the other - and they cannot.
// The error will only appear as "the file is somehow missing".
func TestPTCWorkdirMatchesStubRule(t *testing.T) {
	cases := []struct {
		conversation string
		want         string
	}{
		{"conv_3767f54b17db900b31e554d2e9103cb6", "/workspace/conv-conv_3767f54b17db900b31e554d2e9103cb6"},
		{"", "/workspace/shared"},
		{"  ", "/workspace/shared"},
		// Normalization: The path separator and other characters must be replaced by -, and conversation_id cannot escape from the directory.
		{"../../etc/passwd", "/workspace/conv-------etc-passwd"},
		{"a/b", "/workspace/conv-a-b"},
		// Python's isalnum() recognizes Unicode but Go only recognizes ASCII; stub therefore hardcodes the ASCII whitelist.
		// Chinese must be replaced with - on both sides.
		{"名字", "/workspace/conv---"},
	}
	for _, c := range cases {
		got := ptcWorkdir(map[string]any{"conversation_id": c.conversation})
		if got != c.want {
			t.Errorf("conversation_id=%q: got %q want %q", c.conversation, got, c.want)
		}
	}

	// Truncated to 64 characters, aligned with stub's [:64].
	long := strings.Repeat("x", 200)
	got := ptcWorkdir(map[string]any{"conversation_id": long})
	if got != "/workspace/conv-"+strings.Repeat("x", 64) {
		t.Fatalf("超长 conversation_id 未按 64 截断: %s", got)
	}

	// When you cannot get bkn_context, you cannot panic and retreat to the shared directory.
	if got := ptcWorkdir(nil); got != "/workspace/shared" {
		t.Fatalf("空上下文应退到 shared，得到 %s", got)
	}
}

// The directory name will be spelled into the shell command as is, only [A-Za-z0-9_-] and the path prefix are allowed.
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

	ctx := common.SetLanguageToCtx(common.SetRawTokenToCtx(context.Background(), "tok-123"), "en-US")
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
	// The sandbox is executed according to Lambda specifications. The entry must be a single-parameter handler, and the model code must be indented.
	if !strings.Contains(executor.last.Code, "def handler(event):") ||
		!strings.Contains(executor.last.Code, "    print(1)") ||
		!strings.Contains(executor.last.Code, "    print(2)") {
		t.Fatalf("代码未正确包成 handler:\n%s", executor.last.Code)
	}
	if !strings.Contains(executor.last.Code, "_configure(event)") {
		t.Fatal("未调用 _configure，工作目录与凭据都不会注入")
	}
	// Credentials and session context use event instead of env_vars: sandbox session pooling and reuse, env will use the previous.
	// The caller's value remains in the container.
	if executor.last.Event["token"] != "tok-123" {
		t.Fatalf("未下发调用方令牌: %v", executor.last.Event["token"])
	}
	if executor.last.Event["mcp"] != toolkit.SandboxMCPURL {
		t.Fatalf("未下发沙箱回访地址: %v", executor.last.Event["mcp"])
	}
	if executor.last.Event["locale"] != "en-US" {
		t.Fatalf("未下发有效语言: %v", executor.last.Event["locale"])
	}
	if !strings.Contains(toolkit.Stub, `"Accept-Language": _CFG.get("locale", "zh-CN")`) {
		t.Fatal("PTC sandbox stub does not forward the effective locale to MCP")
	}
}

// If the shell doesn't call back to the MCP, it shouldn't get the token - there's one less credential in the sandbox that can be read.
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
	// It falls in the same directory as run_code, otherwise the files written by the other party will not be visible.
	if !strings.HasPrefix(executor.last.Code, "mkdir -p /workspace/conv-conv_a && cd /workspace/conv-conv_a\n") {
		t.Fatalf("未先切到本次对话的工作目录:\n%s", executor.last.Code)
	}
	if !strings.HasSuffix(executor.last.Code, "ls -la") {
		t.Fatalf("命令未拼在后面:\n%s", executor.last.Code)
	}
}

// If the exit code is non-0, it will be marked as a tool error, and the message will be brought back as usual - the caller that swallows stderr can only retry blindly.
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

// Empty input parameters must be rejected before sandboxing. Running the execution in vain is slow and takes up the session.
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

func TestPTCExecuteUsesPinnedLocaleForValidationError(t *testing.T) {
	handler := handlePTCExecuteForLocale(
		&fakeExecutor{}, ptcTestToolkit(t), ptcToolByName(t, "run_code"), loadMCPLocaleBundle("en-US"),
	)
	result, err := handler(common.SetLanguageToCtx(context.Background(), "zh-CN"), ptcCallRequest("run_code", map[string]any{
		"code":        "   ",
		"bkn_context": map[string]any{"conversation_id": "c", "interaction_id": "i"},
	}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok || content.Text != "The code parameter is required." {
		t.Fatalf("localized validation error = %#v", result.Content)
	}
}

// The MCP client does not have a studio front-end to manage sessions for it, so bkn_context must appear in the input parameter schema.
// And it is required - otherwise the model will not be passed and every call will be blocked by the lifecycle guard.
func TestPTCSchemaRequiresBusinessContext(t *testing.T) {
	for _, tool := range ptcTestToolkit(t).Tools {
		var schema map[string]any
		if err := json.Unmarshal(ptcToolInputSchemaWithContext(
			tool.InputSchema, loadMCPLocaleBundle(defaultMCPLocale).PTCResource("ptc_bkn_context_description.txt"),
		), &schema); err != nil {
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
		// The original required fields cannot be deleted.
		if tool.Name == "run_code" && len(required) < 2 {
			t.Fatalf("run_code 的 code 必填项丢了: %v", required)
		}
	}
}

// There should not be bkn_context in the schema used by the toolkit for studio (where the front end manages the session).
// Complementing bkn_context only occurs on MCP endpoints. Confusing the two will cause the studio model to fill in a value that it does not have.
func TestPTCToolkitSchemaStaysContextFree(t *testing.T) {
	for _, tool := range ptcTestToolkit(t).Tools {
		if strings.Contains(string(tool.InputSchema), "bkn_context") {
			t.Fatalf("%s 的工具包 schema 不应含 bkn_context: %s", tool.Name, tool.InputSchema)
		}
	}
}
