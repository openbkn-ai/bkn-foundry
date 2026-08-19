// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// run_code / run_shell are listed on /mcp alongside business tools, rather than just in /mcp/ptc.
// on the endpoint. The communityTools baseline has already nailed "being", what is nailed here is "in what form".
func TestInlinePTCToolsAreOnTheBusinessSurface(t *testing.T) {
	noExtensions(t)

	byName := map[string]bool{}
	for _, tool := range assembledTools(t) {
		byName[tool.Name] = true
	}
	for _, name := range []string{toolKeyRunCode, toolKeyRunShell} {
		if !byName[name] {
			t.Fatalf("tools/list 缺 %s——并进业务工具面的整个意义就是模型能在一次选择里同时看到它和业务工具", name)
		}
	}
}

// The side-by-side run_code description should no longer carry a list of Python signatures: the complete schema for those tools is.
// On the same tool surface, re-rendering is pure repetition, and it is measured that it takes about 5.5k more characters.
func TestInlineRunCodeDescriptionOmitsSignatures(t *testing.T) {
	noExtensions(t)

	var inline string
	for _, tool := range assembledTools(t) {
		if tool.Name == toolKeyRunCode {
			inline = tool.Description
		}
	}
	if inline == "" {
		t.Fatal("tools/list 上没有 run_code")
	}
	if strings.Contains(inline, "def ") {
		t.Fatalf("并列版描述不该带函数签名:\n%s", inline)
	}
	// But the function name should be kept. The model needs to know which names can be called in the script, and the schema of tools/list is.
	// Separated by tool, nowhere does it tell "these are in script scope".
	if !strings.Contains(inline, toolKeySearchSchema) {
		t.Fatalf("并列版描述应列出可用函数名:\n%s", inline)
	}

	kit, err := BuildPTCToolkitForLocale("", 30779, defaultMCPLocale)
	if err != nil {
		t.Skipf("需要内嵌工具元数据: %v", err)
	}
	if len(inline) >= len(kit.Digest) {
		t.Fatalf("并列版应比独立端点那版短: %d vs %d", len(inline), len(kit.Digest))
	}
}

// digest is a table of callable functions for scripts in the sandbox to see. Writing run_code is equivalent to telling the model that it can.
// Another layer of sandbox is opened in the code, and that function does not exist in the stub at all.
func TestInlineDigestDoesNotListTheExecutionToolsThemselves(t *testing.T) {
	noExtensions(t)

	kit, err := InlinePTCToolkit(30779, defaultMCPLocale)
	if err != nil {
		t.Skipf("需要内嵌工具元数据: %v", err)
	}
	for _, name := range []string{toolKeyRunCode, toolKeyRunShell} {
		if strings.Contains(kit.Stub, "def "+name+"(") {
			t.Fatalf("stub 不该定义 %s", name)
		}
	}
}

// /mcp/info and tools/list must say the same thing about these two tools. The purpose of the former is to prevent people from shaking hands.
// In terms of ability, broadcasting a description that is different from what the model sees is worse than not broadcasting it at all.
func TestInfoAndToolsListAgreeOnTheExecutionTools(t *testing.T) {
	noExtensions(t)

	info, err := BuildMCPInfoForLocale("", defaultMCPLocale)
	if err != nil {
		t.Fatalf("build info: %v", err)
	}
	advertised := map[string]MCPToolInfo{}
	for _, tool := range info.Tools {
		advertised[tool.Name] = tool
	}

	for _, tool := range assembledTools(t) {
		if tool.Name != toolKeyRunCode && tool.Name != toolKeyRunShell {
			continue
		}
		got, ok := advertised[tool.Name]
		if !ok {
			t.Fatalf("/mcp/info 没有 %s", tool.Name)
		}
		if got.Description != tool.Description {
			t.Fatalf("%s 两个端点的描述不一致", tool.Name)
		}
		if got.Title == "" || got.Group == "" || got.Order == 0 {
			t.Fatalf("%s 的展示元数据不全: %+v", tool.Name, got)
		}
		// schema is also taking the same path: if you put together a copy of each place, people who integrate it according to /mcp/info will.
		// Got a statement that didn't make sense.
		if !schemaEquivalent(t, wireInputSchema(t, tool), got.InputSchema) {
			t.Fatalf("%s 的入参 schema 在两个端点上不一致", tool.Name)
		}
	}
}

// The input parameter declaration of run_code must contain bkn_context - the lifecycle guard requires it from each business tool.
// Without this item, the model will call according to the schema, and then get conversation_required.
func TestInlineExecutionToolsDeclareBKNContext(t *testing.T) {
	noExtensions(t)

	for _, tool := range assembledTools(t) {
		if tool.Name != toolKeyRunCode && tool.Name != toolKeyRunShell {
			continue
		}
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(wireInputSchema(t, tool), &schema); err != nil {
			t.Fatalf("decode %s: %v", tool.Name, err)
		}
		if _, ok := schema.Properties["bkn_context"]; !ok {
			t.Fatalf("%s 的 schema 没有 bkn_context", tool.Name)
		}
		var found bool
		for _, name := range schema.Required {
			if name == "bkn_context" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s 没有把 bkn_context 标为必填", tool.Name)
		}
	}
}

// Not subject to MCP_EXECUTE_SKILL_ENABLED. That switch is semantically the gate for skill execution, and these two.
// is another ability; sharing a switch forces anyone who wants to enable skill execution to enable arbitrary code execution as well.
// This is consistent with the judgment on the /mcp/ptc endpoint, and the cost is written on rest_public_handler.go.
func TestInlineExecutionToolsIgnoreTheSkillSwitch(t *testing.T) {
	noExtensions(t)
	t.Setenv("MCP_EXECUTE_SKILL_ENABLED", "")

	var runCode, executeSkill bool
	for _, tool := range assembledTools(t) {
		switch tool.Name {
		case toolKeyRunCode:
			runCode = true
		case toolKeyExecuteSkill:
			executeSkill = true
		}
	}
	if !runCode {
		t.Fatal("开关未开时 run_code 也该在工具面上")
	}
	if executeSkill {
		t.Fatal("开关未开时 execute_skill 不该出现——这条在这里是为了证明上面那条不是因为开关被打开了")
	}
}

// Just appearing in the tools/list does not count as being connected - if the handler is hanged incorrectly, the model will see the tool and call it.
// But I got "no such tool". Take the real tools/call path here: when bkn_context is missing, it should be.
// Lifecycle guards block it instead of being rejected by mcp-go as an unknown tool.
func TestInlineExecutionToolsAreCallable(t *testing.T) {
	noExtensions(t)

	for _, name := range []string{toolKeyRunCode, toolKeyRunShell} {
		got := errorText(t, callTool(t, name, map[string]any{"code": "print(1)", "command": "ls"}))
		if strings.Contains(got, "not found") {
			t.Fatalf("%s 在工具面上，但调用时是未知工具——handler 没挂上: %s", name, got)
		}
		if !strings.Contains(got, "conversation") {
			t.Fatalf("%s 缺 bkn_context 时应被生命周期守卫拦下，实际: %s", name, got)
		}
	}
}

// wireInputSchema gets the input parameter statement when the tool is actually online.
//
// Unable to read mcp.Tool.InputSchema: The schema is passed in as RawInputSchema. That structure.
// The field is always empty on this path, and asserting it results in a test that always passes.
func wireInputSchema(t *testing.T, tool mcp.Tool) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal tool %q: %v", tool.Name, err)
	}
	var wire struct {
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode tool %q: %v", tool.Name, err)
	}
	return wire.InputSchema
}

func schemaEquivalent(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if err := json.Unmarshal(b, &right); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	x, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("re-encode schema: %v", err)
	}
	y, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("re-encode schema: %v", err)
	}
	return string(x) == string(y)
}
