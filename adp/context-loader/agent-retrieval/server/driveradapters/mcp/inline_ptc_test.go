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

// run_code / run_shell 与业务工具并列在 /mcp 上，而不是只在 /mcp/ptc 那个独立
// 端点上。communityTools 基线已经钉住了「在」，这里钉的是「以什么形态在」。
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

// 并列版的 run_code 描述不该再带一份 Python 签名清单：那些工具的完整 schema 就
// 在同一个工具面上，重渲染一遍是纯重复，实测多花约 5.5k 字符。
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
	// 但函数名要留着。模型得知道脚本里能调哪些名字，而 tools/list 的 schema 是
	// 按工具分开的，没有任何一处告诉它「这些在脚本作用域内」。
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

// digest 是给沙箱内脚本看的可调函数表。把 run_code 写进去等于告诉模型可以在
// 代码里再开一层沙箱，而那个函数在 stub 里根本不存在。
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

// /mcp/info 与 tools/list 对这两个工具必须说同一套话。前者的用途是让人不握手
// 就看清能力面，广播一份和模型看到的不一样的说明比不广播更糟。
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
		// schema 也走的是同一条路：两处各拼一份的话，照 /mcp/info 集成的人会
		// 拿到一份调不通的声明。
		if !schemaEquivalent(t, wireInputSchema(t, tool), got.InputSchema) {
			t.Fatalf("%s 的入参 schema 在两个端点上不一致", tool.Name)
		}
	}
}

// run_code 的入参声明必须带 bkn_context——生命周期守卫向每个业务工具要它，
// 少了这一项模型会照着 schema 调，然后拿到 conversation_required。
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

// 不受 MCP_EXECUTE_SKILL_ENABLED 约束。那个开关按语义是技能执行的闸，而这两个
// 是另一种能力；共用一个开关会让想开技能执行的人被迫连任意代码执行一起开。
// 这与 /mcp/ptc 端点上的判断一致，代价写在 rest_public_handler.go 上。
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

// 光在 tools/list 里出现不算接上——handler 挂错的话，模型会看到工具、调用时
// 却拿到「无此工具」。这里走真实的 tools/call 路径：缺 bkn_context 时应该被
// 生命周期守卫拦下，而不是被 mcp-go 当作未知工具拒掉。
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

// wireInputSchema 取工具真正上线的那份入参声明。
//
// 不能读 mcp.Tool.InputSchema：schema 是以 RawInputSchema 传进去的，那个结构化
// 字段在这条路上恒为空，照它断言会得到一个永远通过的测试。
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
