// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// fakeExecutor records the last sandbox execution request.
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
	if got := ptcWorkdir(nil); !strings.HasPrefix(got, "/workspace/adhoc-") {
		t.Fatalf("空上下文必须隔离，得到 %s", got)
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
	if !strings.Contains(executor.last.Code, "def handler(event):") ||
		!strings.Contains(executor.last.Code, "from sandbox_sdk import _bkn_tools") ||
		strings.Contains(executor.last.Code, "print(1)") {
		t.Fatalf("run_code must use the fixed installed-toolkit bootstrap: %s", executor.last.Code)
	}
	if executor.last.Event["_ptc_source"] != "print(1)\n\nprint(2)" {
		t.Fatalf("user source changed: %v", executor.last.Event["_ptc_source"])
	}
	if !strings.Contains(executor.last.Code, "_configure(event)") {
		t.Fatal("未调用 _configure，工作目录与凭据都不会注入")
	}
	// The sandbox scopes artifact collection to the working directory it is given.
	// Leaving it empty makes the executor scan the shared workspace root and report
	// other conversations' files as this execution's output.
	if executor.last.WorkingDirectory != "conv-conv_a" {
		t.Fatalf("未下发会话工作目录: %q", executor.last.WorkingDirectory)
	}
	// Credentials and session context use event instead of env_vars: sandbox session pooling and reuse, env will use the previous.
	// The caller's value remains in the container.
	if executor.last.Event["token"] != "tok-123" {
		t.Fatalf("未下发调用方令牌: %v", executor.last.Event["token"])
	}
	if executor.last.Event["mcp"] != toolkit.SandboxMCPURL {
		t.Fatalf("未下发沙箱回访地址: %v", executor.last.Event["mcp"])
	}
	if executor.last.Event["locale"] != common.AmericanEnglish {
		t.Fatalf("未下发有效语言: %v", executor.last.Event["locale"])
	}
	if !strings.Contains(toolkit.Stub, `"Accept-Language": _CFG.get("locale", "zh-CN")`) {
		t.Fatal("PTC sandbox stub does not forward the effective locale to MCP")
	}
}

func TestPTCRunCodeChildrenUseExecutingOperationAsParent(t *testing.T) {
	for _, originalParent := range []string{"", "op_grandparent"} {
		for _, attempt := range []int{1, 2} {
			executor := &fakeExecutor{}
			businessContext := map[string]any{
				"conversation_id": "conv_a", "interaction_id": "int_b",
				"causation_event_ids": []any{"evt_recorded"},
				"business_refs":       []any{map[string]any{"kind": "recorded"}},
			}
			if originalParent != "" {
				businessContext["parent_operation_id"] = originalParent
			}
			before, _ := json.Marshal(businessContext)
			ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
				ConversationID: "conv_a", InteractionID: "int_b", OperationID: "op_run_code", Attempt: attempt,
			})
			handler := handlePTCExecute(executor, ptcTestToolkit(t), ptcToolByName(t, "run_code"))
			result, err := handler(ctx, ptcCallRequest("run_code", map[string]any{
				"code": "query_object_instance(kn_id='n', ot_id='m')", "bkn_context": businessContext,
			}))
			if err != nil || result == nil || result.IsError || executor.last == nil {
				t.Fatalf("run_code did not execute: %v", err)
			}
			childContext := executor.last.Event["bkn"].(map[string]any)
			if childContext["parent_operation_id"] != "op_run_code" {
				t.Fatalf("parent=%q attempt=%d: child parent=%v, want executing operation", originalParent, attempt, childContext["parent_operation_id"])
			}
			for key, value := range businessContext {
				if key != "parent_operation_id" && !reflect.DeepEqual(value, childContext[key]) {
					t.Fatalf("changed business context %s", key)
				}
			}
			after, _ := json.Marshal(businessContext)
			if string(before) != string(after) {
				t.Fatal("mutated outer run_code request context")
			}
			if _, exists := childContext["attempt"]; exists {
				t.Fatal("invented parent attempt field")
			}
		}
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
	// The cd inside the command only moves the shell. The executor needs the same
	// directory declared on the request to scope artifact collection to it.
	if executor.last.WorkingDirectory != "conv-conv_a" {
		t.Fatalf("未下发会话工作目录: %q", executor.last.WorkingDirectory)
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
		if found {
			t.Fatalf("%s: bkn_context 不得阻止业务，required=%v", tool.Name, required)
		}
		// The original required fields cannot be deleted.
		if tool.Name == "run_code" && (len(required) != 1 || required[0] != "code") {
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

func TestPTCRunCodeCharacterBudget(t *testing.T) {
	for _, character := range []string{"x", "中", "😀"} {
		for _, count := range []int{ptcMaxCodeCharacters - 1, ptcMaxCodeCharacters, ptcMaxCodeCharacters + 1} {
			t.Run(fmt.Sprintf("%s/%d", character, count), func(t *testing.T) {
				executor := &fakeExecutor{}
				handler := handlePTCExecuteForLocale(executor, ptcTestToolkit(t), ptcToolByName(t, "run_code"), loadMCPLocaleBundle("en-US"))
				source := strings.Repeat(character, count)
				result, err := handler(context.Background(), ptcCallRequest("run_code", map[string]any{"code": source}))
				if err != nil {
					t.Fatal(err)
				}
				if count > ptcMaxCodeCharacters {
					if !result.IsError || executor.last != nil {
						t.Fatal("over-limit source reached executor")
					}
					encoded, _ := json.Marshal(result)
					if !strings.Contains(string(encoded), "102401") || !strings.Contains(string(encoded), "102400") || !strings.Contains(string(encoded), "Public.UnprocessableEntity") || !strings.Contains(string(encoded), "code_too_long") || !strings.Contains(string(encoded), "unicode_characters") {
						t.Fatalf("missing actual/max character counts: %s", encoded)
					}
				} else {
					if result.IsError || executor.last == nil || executor.last.Event["_ptc_source"] != source {
						t.Fatal("valid source rejected or modified")
					}
					if len(executor.last.Code) > 4096 {
						t.Fatalf("internal bootstrap is not bounded: %d", len(executor.last.Code))
					}
				}
			})
		}
	}
}

func TestPTCPythonBootstrapPreservesSourceSemantics(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	version, err := ImageToolkitVersion()
	if err != nil {
		t.Fatal(err)
	}
	runPython := func(program string) ([]byte, error) {
		command := exec.Command(python, "-")
		command.Stdin = strings.NewReader(program)
		return command.CombinedOutput()
	}
	// Only toolkit side effects are replaced; Python really compiles the user AST.
	setup := fmt.Sprintf(`import sys, types, json, io, contextlib, inspect
package = types.ModuleType("sandbox_sdk")
tools = types.ModuleType("sandbox_sdk._bkn_tools")
tools.__toolkit_version__ = %q
configured = []
tools._configure = lambda event: configured.append(event)
def query(value: str = "default"):
    """Example tool help."""
    return value
tools.query = query
package._bkn_tools = tools
sys.modules["sandbox_sdk"] = package
`, version)
	cases := []struct{ name, source, want string }{
		{"return", "return 42", "42"},
		{"comment_only", "# comment", "None"},
		{"multiline", "text = '''first\nsecond\n  third'''\nreturn text", "first\nsecond\n  third"},
		{"unicode", "return '中文😀'", "中文😀"},
		{"empty_lines", "\n\nreturn 9\n\n", "9"},
		{"tool_signature", "import inspect\nreturn str(inspect.signature(query))", "(value: str = 'default')"},
		{"help", "return query.__doc__", "Example tool help."},
		{"global_tool_call", "global custom\ncustom = 'value'\nreturn query(custom)", "value"},
		{"module_identity", "return __name__", "__main__"},
	}
	for _, length := range []int{51254, 58554} {
		for _, lines := range []int{1, 113} {
			source := "return query('snapshot')" + strings.Repeat("\n#", lines-1)
			source += " #" + strings.Repeat("x", length-len(source)-2)
			cases = append(cases, struct{ name, source, want string }{fmt.Sprintf("reported_length_%d_lines_%d", length, lines), source, "snapshot"})
		}
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(test.source)
			program := setup + fmt.Sprintf(ptcPythonBootstrap, version) + "\nresult = handler({'_ptc_source': " + string(body) + ", 'token': 'caller'})\nassert configured[0]['token'] == 'caller'\nprint(result, end='')\n"
			output, err := runPython(program)
			if err != nil || string(output) != test.want {
				t.Fatalf("got %q, err %v, want %q", output, err, test.want)
			}
		})
	}
	program := setup + fmt.Sprintf(ptcPythonBootstrap, version) + `
import os, subprocess
source = "import subprocess,sys,os\nassert '_ptc_source' not in event\nassert '_ptc_source' not in os.environ['EVENT_JSON']\nsubprocess.run([sys.executable, '-c', 'print(123)'], check=True)\n#" + "中" * 90000
event = {'_ptc_source': source, 'token': 'caller'}
os.environ['EVENT_JSON'] = json.dumps(event)
handler(event)
`
	output, err := runPython(program)
	if err != nil || string(output) != "123\n" {
		t.Fatalf("source leaked into child environment: %s %v", output, err)
	}
	program = setup + fmt.Sprintf(ptcPythonBootstrap, version) + `
try:
    handler({'_ptc_source': "\ninvalid =\n"})
except SyntaxError as error:
    assert error.lineno == 2
    assert error.filename == '<run_code>'
else:
    raise AssertionError('invalid syntax accepted')
stdout, stderr = io.StringIO(), io.StringIO()
with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
    handler({'_ptc_source': "import sys\nprint('out')\nprint('err', file=sys.stderr)\n"})
assert stdout.getvalue() == 'out\n'
assert stderr.getvalue() == 'err\n'
`
	output, err = runPython(program)
	if err != nil {
		t.Fatalf("source diagnostics or output streams changed: %s %v", output, err)
	}
	program = setup + fmt.Sprintf(ptcPythonBootstrap, "stale-version") + "\nhandler({'_ptc_source': 'raise Exception(123)'})\n"
	output, err = runPython(program)
	if err == nil || !strings.Contains(string(output), "sandbox toolkit version mismatch") || strings.Contains(string(output), "Exception: 123") {
		t.Fatalf("stale toolkit must fail before user source: %s %v", output, err)
	}
}

func TestPTCExecutePreservesPublicFailureClassification(t *testing.T) {
	for _, test := range []struct {
		status   int
		code     string
		metadata map[string]any
	}{
		{422, "Public.UnprocessableEntity", map[string]any{"reason": "code_too_long", "stage": "sandbox_validation", "actual": 102401, "max": 102400, "unit": "unicode_characters"}},
		{504, "Public.GatewayTimeout", map[string]any{"reason": "execution_timeout", "stage": "sandbox_execution", "timeout_seconds": 60}},
	} {
		t.Run(test.code, func(t *testing.T) {
			failure := httperrors.NewHTTPError(context.Background(), test.status, test.code).WithErrorDetails("safe execution failure").WithMetadata(test.metadata)
			executor := &fakeExecutor{err: failure}
			result, err := handlePTCExecute(executor, ptcTestToolkit(t), ptcToolByName(t, "run_code"))(context.Background(), ptcCallRequest("run_code", map[string]any{"code": "return 1"}))
			if err != nil || !result.IsError {
				t.Fatalf("failure not surfaced: %v %v", result, err)
			}
			if len(result.Content) != 1 {
				t.Fatalf("unexpected error contents: %v", result.Content)
			}
			content, ok := result.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("error is not textual: %v", result.Content)
			}
			var got httperrors.BaseError
			if err := json.Unmarshal([]byte(content.Text), &got); err != nil {
				t.Fatal(err)
			}
			wantJSON := []byte(failure.Error())
			var want httperrors.BaseError
			if err := json.Unmarshal(wantJSON, &want); err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("public classification changed: %s != %s", wantJSON, gotJSON)
			}
		})
	}
}

func TestPTCExecuteMarksProcessLaunchFailure(t *testing.T) {
	executor := &fakeExecutor{resp: &interfaces.ExecuteFunctionResponse{ExitCode: -1, Stderr: "Argument list too long"}}
	result, err := handlePTCExecute(executor, ptcTestToolkit(t), ptcToolByName(t, "run_code"))(context.Background(), ptcCallRequest("run_code", map[string]any{"code": "return 1"}))
	encoded, _ := json.Marshal(result)
	if err != nil || !result.IsError || !strings.Contains(string(encoded), "Argument list too long") || !strings.Contains(string(encoded), "-1") {
		t.Fatalf("process failure lost: %s %v", encoded, err)
	}
}
