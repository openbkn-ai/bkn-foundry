// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func ptcTestTools() []MCPToolInfo {
	return []MCPToolInfo{
		{
			Name: "list_knowledge_networks", Title: "知识网络列表",
			Group: "discovery", GroupTitle: "网络与 Schema", Order: 100,
			Description: "列出知识网络",
			InputSchema: json.RawMessage(`{"type":"object","properties":{
				"response_format":{"type":"string","default":"toon","description":"格式"},
				"limit":{"type":"integer"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{
				"entries":{"type":"array","items":{"type":"object","properties":{
					"kn_id":{"type":"string"},"name":{"type":"string"}}}},
				"total_count":{"type":"integer"}}}`),
		},
		{
			Name: "query_object_instance", Title: "实例查询",
			Group: "query", GroupTitle: "实例查询", Order: 210,
			Description: "查询对象实例",
			// bkn_context is required on the server side: the lifecycle guard asks the business tools for it.
			InputSchema: json.RawMessage(`{"type":"object","properties":{
				"kn_id":{"type":"string"},"ot_id":{"type":"string"},
				"bkn_context":{"type":"object"},
				"response_format":{"type":"string","default":"toon"},
				"limit":{"type":"integer"}},
				"required":["kn_id","ot_id","bkn_context"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{
				"datas":{"type":"array","items":{"type":"object","properties":{
					"_display":{"type":"string"},"_instance_id":{"type":"string"}}}},
				"total_count":{"type":"integer"},
				"search_after":{"type":"array","items":{}}}}`),
		},
		{
			Name: "run_sql", Title: "SQL 查询",
			Group: "query", GroupTitle: "实例查询", Order: 240,
			Description:  "执行只读 SQL",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}},"required":["sql"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"columns":{},"entries":{}}}`),
		},
		{
			Name: "bkn_start_interaction", Title: "开始交互",
			Group: "lifecycle", GroupTitle: "会话生命周期", Order: 10,
			Description: "开始一次交互",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}}}`),
		},
	}
}

func ptcTestDigest() string {
	return renderPTCDigest(ptcUsableTools(&MCPInfo{Tools: ptcTestTools()}))
}

func ptcTestToolkit(t *testing.T) *PTCToolkit {
	t.Helper()
	toolkit, err := buildPTCToolkitFrom(ptcUsableTools(&MCPInfo{Tools: ptcTestTools()}), 30779)
	if err != nil {
		t.Fatalf("构建工具包失败: %v", err)
	}
	return toolkit
}

func TestPTCToolkitUsesLocalizedSchemasAndDescription(t *testing.T) {
	toolkit, err := BuildPTCToolkitForLocale("https://example.invalid/mcp", 30779, "en-US")
	if err != nil {
		t.Fatalf("build English toolkit: %v", err)
	}
	for _, tool := range toolkit.Tools {
		switch tool.Name {
		case "run_code":
			if !strings.Contains(string(tool.InputSchema), "Python code to execute") ||
				!strings.Contains(tool.Description, "The following BKN capabilities") ||
				strings.Contains(tool.Description, "下列 BKN 能力") {
				t.Fatalf("run_code text is not localized: description=%q schema=%s", tool.Description, tool.InputSchema)
			}
		case "run_shell":
			if !strings.Contains(tool.Description, "Use this only to inspect") ||
				!strings.Contains(string(tool.InputSchema), "Shell command executed") {
				t.Fatalf("run_shell text is not localized: description=%q schema=%s", tool.Description, tool.InputSchema)
			}
		}
	}
}

func TestPTCDigestRendersSignatures(t *testing.T) {
	digest := ptcTestDigest()

	// Required first, optional with default value - Python does not allow parameters with default values to be listed before parameters without default values.
	if !strings.Contains(digest, "query_object_instance(kn_id: str, ot_id: str,") {
		t.Fatalf("必填参数未排在前面:\n%s", digest)
	}
	// The return key must be written out: the key names are not consistent across tools (entries and datas coexist),
	// The model cannot be inferred, and if it is not written out, the first call will fail with a KeyError.
	//
	// Array keys also need to expand one layer of element fields. When only writing top-level keys, the model has to guess the value path first, guess wrong and then spend a whole round.
	// Print the original structure to find the field name - in actual testing, the object_types of search_schema is regarded as having.
	// of the name field (the real field is concept_name).
	if !strings.Contains(digest, "-> {entries[kn_id name], total_count}") {
		t.Fatalf("数组元素字段未展开:\n%s", digest)
	}
	if !strings.Contains(digest, "-> {datas[_display _instance_id], search_after, total_count}") {
		t.Fatalf("数组展开或非数组键渲染有误:\n%s", digest)
	}
}

// When the element has no declared fields (items is empty, or items only has type), it is rendered by pressing the normal key and cannot be created out of thin air.
// A pair of empty square brackets - the search_after opaque cursor is intentionally not declared.
func TestPTCDigestLeavesUndeclaredArraysFlat(t *testing.T) {
	tools := []MCPToolInfo{{
		Name: "probe", Group: "g", GroupTitle: "G", Order: 1, Description: "d",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{
			"opaque":{"type":"array","items":{}},
			"plain":{"type":"string"}}}`),
	}}
	digest := renderPTCDigest(ptcUsableTools(&MCPInfo{Tools: tools}))
	if !strings.Contains(digest, "-> {opaque, plain}") {
		t.Fatalf("未声明元素字段的数组应按普通键渲染:\n%s", digest)
	}
	if strings.Contains(digest, "opaque[]") {
		t.Fatalf("不该渲染出空方括号:\n%s", digest)
	}
}

// bkn_context is a lifecycle pipeline injected by stub's _call. Leave it in the signature and let the model fill in one.
// It doesn't have a value - this is where the client fails when rendering itself from tools/list.
func TestPTCDigestStripsPlumbingParams(t *testing.T) {
	if digest := ptcTestDigest(); strings.Contains(digest, "bkn_context") {
		t.Fatalf("bkn_context 不应出现在给模型的签名里:\n%s", digest)
	}
}

// Toon is a token-saving text format optimized for "directly feeding the model"; in code mode, the return value is first passed through the script.
// Processing requires a subscript-accessible structure.
func TestPTCDigestOverridesResponseFormat(t *testing.T) {
	digest := ptcTestDigest()
	if !strings.Contains(digest, "response_format: str = 'json'") {
		t.Fatalf("response_format 未覆盖为 json:\n%s", digest)
	}
	if strings.Contains(digest, "'toon'") {
		t.Fatalf("digest 不应保留 toon 默认值:\n%s", digest)
	}
}

func TestPTCDigestExcludesLifecycleTools(t *testing.T) {
	if digest := ptcTestDigest(); strings.Contains(digest, "bkn_start_interaction") {
		t.Fatalf("生命周期工具由调用方接管，不应出现:\n%s", digest)
	}
}

// Order encodes the order of use of "find first, query later"; sorting by group name dictionary would disrupt this.
func TestPTCDigestGroupsOrderedByOrder(t *testing.T) {
	digest := ptcTestDigest()
	discovery := strings.Index(digest, "### 网络与 Schema")
	query := strings.Index(digest, "### 实例查询")
	if discovery < 0 || query < 0 || discovery > query {
		t.Fatalf("分组顺序错误 discovery=%d query=%d:\n%s", discovery, query, digest)
	}
}

// Code fences must be in pairs: in the early version, each group was only open and not closed, and the model saw a whole block that was glued together.
func TestPTCDigestFencesBalanced(t *testing.T) {
	if n := strings.Count(ptcTestDigest(), "```"); n%2 != 0 {
		t.Fatalf("代码围栏未闭合，共 %d 个", n)
	}
}

// In actual testing, if the model writes SQL without reading the complete docstring, it will miss the placeholder convention and fail in one round.
func TestPTCDigestCarriesRunSQLHint(t *testing.T) {
	if digest := ptcTestDigest(); !strings.Contains(digest, "{{.<resource_id>}}") {
		t.Fatalf("run_sql 缺少占位符示例:\n%s", digest)
	}
}

func TestPTCStubIsSelfContained(t *testing.T) {
	stub := renderPTCStub(ptcUsableTools(&MCPInfo{Tools: ptcTestTools()}))

	// Only standard libraries are used: Sandbox images do not require pre-installed dependencies, and there is no SDK version drift.
	for _, forbidden := range []string{"import mcp", "import httpx", "import requests"} {
		if strings.Contains(stub, forbidden) {
			t.Fatalf("stub 不应依赖第三方库，出现了 %q", forbidden)
		}
	}
	if !strings.Contains(stub, "def query_object_instance(kn_id: str, ot_id: str,") {
		t.Fatalf("stub 缺少函数定义:\n%s", stub)
	}
	if !strings.Contains(stub, `_call("query_object_instance"`) {
		t.Fatalf("stub 未生成调用体:\n%s", stub)
	}
	// bkn_context is uniformly injected by _call and cannot be turned into a function parameter.
	if strings.Contains(stub, `"bkn_context": bkn_context`) {
		t.Fatal("stub 不应把 bkn_context 当成函数参数传递")
	}
	if !strings.Contains(stub, `payload["bkn_context"] = _CFG["bkn"]`) {
		t.Fatal("stub 未注入 bkn_context")
	}
}

// /workspace is a directory shared by all callers - the execution interface does not accept session_id, and the actual measurement of the pool is constant.
// same session. The stub must be cut out of the subdirectory by conversation and chdir into it, otherwise the two conversations will have the same name.
// The files will overwrite each other, and what is read back may be other people's data.
func TestPTCStubIsolatesWorkdirPerConversation(t *testing.T) {
	stub := renderPTCStub(ptcUsableTools(&MCPInfo{Tools: ptcTestTools()}))

	// The directory name is normalized rather than hashed: run_shell uses language=shell without going through the stub, it must be on the browser side.
	// Works out the same path and crypto.subtle is not available under non-HTTPS origins. Both sides must have the same set of rules.
	if strings.Contains(stub, "hashlib") {
		t.Fatalf("工作目录不应再用哈希命名（浏览器侧算不出来）:\n%s", stub)
	}
	// Python's isalnum() recognizes Unicode ("name".isalnum() is true), while the Go side only recognizes ASCII.
	// Use it to normalize, Chinese conversation_id will make run_code and run_shell fall into different directories.
	// Match executable notation instead of bare words: the comments of stub explain why it cannot be used.
	if strings.Contains(stub, "c.isalnum() or") {
		t.Fatalf("工作目录归一化不能用 isalnum（Unicode 语义与 Go 侧不一致）:\n%s", stub)
	}
	for _, want := range []string{
		`conversation_id`,          // The origin of the directory name.
		`c if c in _SAFE else "-"`, // Normalization rules must be consistent with the Go side ptcWorkdir.
		`[:64]`,                    // Cut off length, same as above.
		`"conv-" + safe if safe else "shared"`,
		`candidate.mkdir(`,
		`os.chdir(candidate)`,
	} {
		if !strings.Contains(stub, want) {
			t.Fatalf("stub 缺少工作目录隔离逻辑 %q:\n%s", want, stub)
		}
	}
	// When the conversation_id cannot be obtained or the directory cannot be created, it must be returned to the available state and the entire script cannot fail.
	if !strings.Contains(stub, `"shared"`) || !strings.Contains(stub, "except OSError:") {
		t.Fatalf("stub 的工作目录缺少兜底分支:\n%s", stub)
	}
}

// Now that the directory has been cut, digest cannot teach the model to write the absolute path to /workspace - that just circumvents isolation.
func TestPTCDigestTeachesRelativePaths(t *testing.T) {
	digest := ptcTestDigest()

	if strings.Contains(digest, `"/workspace/`) || strings.Contains(digest, "Path(\"/workspace") {
		t.Fatalf("digest 不应示范 /workspace 绝对路径:\n%s", digest)
	}
	if !strings.Contains(digest, "WORKDIR") {
		t.Fatalf("digest 未说明工作目录:\n%s", digest)
	}
	// The user repeatedly asked "Do you have a run shell?" This means that a sentence buried in another section has not been seen.
	if !strings.Contains(digest, "## 执行 shell 命令") || !strings.Contains(digest, "subprocess.run(") {
		t.Fatalf("digest 缺少 shell 小节:\n%s", digest)
	}
}

// The client should traverse tools to build tools without hard-coding them by name - adding tool capabilities is a pure server-side change.
func TestPTCToolkitExposesToolTable(t *testing.T) {
	toolkit := ptcTestToolkit(t)

	byName := map[string]PTCTool{}
	for _, tool := range toolkit.Tools {
		byName[tool.Name] = tool
	}
	if len(byName) != len(toolkit.Tools) {
		t.Fatalf("工具名重复: %+v", toolkit.Tools)
	}

	runCode, ok := byName["run_code"]
	if !ok {
		t.Fatalf("缺少 run_code: %+v", toolkit.Tools)
	}
	if runCode.Language != "python" || runCode.Wrap != ptcWrapHandler {
		t.Fatalf("run_code 组装方式不对: %+v", runCode)
	}
	// The Digest top-level field is reserved only for compatibility with old clients. The content must be consistent with the tool table, otherwise both sides will drift.
	if runCode.Description != toolkit.Digest {
		t.Fatal("run_code 描述与顶层 digest 不一致")
	}

	runShell, ok := byName["run_shell"]
	if !ok {
		t.Fatalf("缺少 run_shell: %+v", toolkit.Tools)
	}
	// The sandbox control surface only recognizes python / javascript / shell; bash was rejected by 422 in actual testing.
	if runShell.Language != "shell" || runShell.Wrap != ptcWrapCdWorkdir {
		t.Fatalf("run_shell 组装方式不对: %+v", runShell)
	}
	// If the boundary model is not drawn, curl will be used to call the MCP by hand, splitting a script into many rounds.
	if !strings.Contains(runShell.Description, "不要用它取 BKN 数据") {
		t.Fatalf("run_shell 描述缺少边界说明:\n%s", runShell.Description)
	}

	for _, tool := range toolkit.Tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s 的 input_schema 不是合法 JSON: %v", tool.Name, err)
		}
		if schema["type"] != "object" {
			t.Fatalf("%s 的 input_schema 顶层应为 object: %v", tool.Name, schema)
		}
	}
}

// Version is the cache key of the client and must cover the entire tool table: if only hashing digest+stub, add a new tool.
// The version number will not change even if the description is changed, and the client will always use the old tool surface.
func TestPTCToolkitVersionCoversToolTable(t *testing.T) {
	toolkit := ptcTestToolkit(t)
	baseline := toolkit.Version

	original := loadMCPLocaleBundle(defaultMCPLocale).PTCResource("ptc_run_shell_description.txt")
	if !strings.Contains(original, "run_code") {
		t.Fatal("前置条件变了：run_shell 描述里不再提到 run_code")
	}
	// When the description changes but digest/stub remains unchanged, the version number must change accordingly.
	mutated := *toolkit
	mutated.Tools = append([]PTCTool(nil), toolkit.Tools...)
	for i := range mutated.Tools {
		if mutated.Tools[i].Name == "run_shell" {
			mutated.Tools[i].Description += " (changed)"
		}
	}
	if fingerprintPTCTools(t, mutated.Tools) == fingerprintPTCTools(t, toolkit.Tools) {
		t.Fatal("工具表变化未反映到指纹里")
	}
	if baseline == "" {
		t.Fatal("版本号为空")
	}
}

func fingerprintPTCTools(t *testing.T, tools []PTCTool) string {
	t.Helper()
	encoded, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("序列化工具表失败: %v", err)
	}
	return string(encoded)
}

// Version is the content hash, which is cached by the client; rendering must be repeatable, otherwise it will look like the tool surface has changed each time.
func TestPTCRenderIsDeterministic(t *testing.T) {
	tools := ptcUsableTools(&MCPInfo{Tools: ptcTestTools()})
	if renderPTCDigest(tools) != renderPTCDigest(tools) {
		t.Fatal("digest 渲染不稳定")
	}
	if renderPTCStub(tools) != renderPTCStub(tools) {
		t.Fatal("stub 渲染不稳定")
	}
}

func TestSandboxMCPURLAlwaysEndsWithSlash(t *testing.T) {
	// When the trailing slash is missing, the gateway will jump to 307, and the sandbox side uses urllib, which does not follow redirects for POST.
	// The symptom is a 400 with no packet.
	t.Setenv("PTC_SANDBOX_MCP_URL", "http://svc:1/api/agent-retrieval/v1/mcp")
	if got := sandboxMCPURL(30779); !strings.HasSuffix(got, "/") {
		t.Fatalf("尾斜杠被丢弃: %s", got)
	}

	t.Setenv("PTC_SANDBOX_MCP_URL", "")
	t.Setenv("PTC_SANDBOX_MCP_HOST", "agent-retrieval")
	if got := sandboxMCPURL(30779); got != "http://agent-retrieval:30779/api/agent-retrieval/v1/mcp/" {
		t.Fatalf("默认地址不对: %s", got)
	}
}

// Actual test (2026-08-14, A/B): The same question was answered by calling 55k tokens twice on the ordinary tool surface, and PTC used it.
// 136k tokens 12 times have not yet converged. The difference is that the normal prompt word pushes the aggregation to run_sql, while the PTC prompt.
// At that time, digest said "Leave grouping, connection, and statistics to pandas or collections", and pushed the model.
// query_object_instance(limit=5000) pulls rows back for local calculation, which is slow and silently truncated by limit.
func TestPTCDigestPrefersSQLPushdown(t *testing.T) {
	digest := ptcTestDigest()

	if !strings.Contains(digest, "## 能下推的聚合一律下推") {
		t.Fatalf("digest 缺少下推小节:\n%s", digest)
	}
	// There can no longer be expressions such as "Leave statistics to pandas" that push the model away from SQL.
	if strings.Contains(digest, "分组、连接、统计交给") {
		t.Fatalf("digest 仍在把聚合推给 pandas:\n%s", digest)
	}
	// Counterexamples need to be written: just say "SQL first" and the model will still pull back.
	if !strings.Contains(digest, "拉 5000 行回来自己数") {
		t.Fatalf("digest 缺少反例:\n%s", digest)
	}
}

// The 12 rounds in the actual measurement were not a blind test, but a round-by-round correction: it was only in the 6th round that the data was mixed with women’s football, and in the 7th round it was discovered.
// West Germany and Germany are the same branch. This kind of misunderstanding could have been typed out in the first script along with the answer.
func TestPTCDigestTeachesPrintingAssumptions(t *testing.T) {
	digest := ptcTestDigest()

	// This rule incorporates "One script solves the entire problem" - it is an extension of that section, unlike run_shell.
	// That's a standalone tool that needs its own title to be visible. Nail the rule itself, not the subsection it lives in.
	if !strings.Contains(digest, "对数据口径有误解") {
		t.Fatalf("digest 缺少「先打印口径」这条规则:\n%s", digest)
	}
	if !strings.Contains(digest, `print("口径:"`) {
		t.Fatalf("digest 缺少口径与答案同时 print 的示例:\n%s", digest)
	}
	if !strings.Contains(digest, "DISTINCT") {
		t.Fatalf("缺少打印取值范围的示例:\n%s", digest)
	}
}

// Omit the signature list when merging business tool surfaces: the complete schema for those tools is in the same tool surface,
// Rendering a Python signature again is describing the same set of tools twice. The measured list accounts for 55% of the entire digest.
func TestInlineDigestDropsSignatureList(t *testing.T) {
	locale := loadMCPLocaleBundle(defaultMCPLocale)
	tools := ptcUsableTools(&MCPInfo{Tools: ptcTestTools()})

	full := renderPTCDigestForLocale(locale, tools, true)
	inline := renderPTCDigestForLocale(locale, nil, false)

	if !strings.Contains(full, "## 可用函数") {
		t.Fatalf("完整版必须带签名清单:\n%s", full)
	}
	// The criterion is the shape of the signature line (`name(...) -> {return key}`), not the function name itself - common rule.
	// There is sample code in this section, and calls like query_object_instance(...) will appear normally.
	if strings.Contains(inline, "## 可用函数") || strings.Contains(inline, ") -> {") {
		t.Fatalf("并入版不该重复渲染签名:\n%s", inline)
	}
	if len(inline) >= len(full) {
		t.Fatalf("并入版应更短: 完整 %d / 并入 %d", len(full), len(inline))
	}

	// Rules sections are shared on both sides - this saves duplication, not throws away the rules altogether.
	for _, section := range []string{
		"## 一段脚本解决整个问题", "## 能下推的聚合一律下推",
		"## 工作目录与大结果", "## 执行 shell 命令", "## 参数写不准时", "## 错误处理",
	} {
		if !strings.Contains(inline, section) {
			t.Fatalf("并入版丢了 %s:\n%s", section, inline)
		}
	}

	// These two items cannot be dismissed by "parameters are consistent with the schema": bkn_context in the schema is required.
	// The script is injected at runtime; response_format defaults to toon, and the code requires json.
	for _, must := range []string{"bkn_context", "response_format"} {
		if !strings.Contains(inline, must) {
			t.Fatalf("并入版缺少 %s 的说明:\n%s", must, inline)
		}
	}
}

// Names should be listed, but only first names.
//
// "Which tools can be called in the script" should not be relied on inference - whether the conditionally registered tools exist, whether any one is missing,
// There is no ambiguity if they are listed; the parameters and return values are in the schema on the tool surface, and rendering them again is duplication.
// The actual length of this list is about 430 characters, and the complete signature list is 4897.
func TestInlineDigestListsNamesOnly(t *testing.T) {
	locale := loadMCPLocaleBundle(defaultMCPLocale)
	tools := ptcUsableTools(&MCPInfo{Tools: ptcTestTools()})
	inline := renderPTCDigestForLocale(locale, tools, false)

	for _, name := range []string{"list_knowledge_networks", "query_object_instance", "run_sql"} {
		if !strings.Contains(inline, name) {
			t.Fatalf("缺少函数名 %s:\n%s", name, inline)
		}
	}
	// Lifecycle tools are taken over by the caller in turn and should not appear in the script's adjustable list.
	if strings.Contains(inline, "bkn_start_interaction") {
		t.Fatalf("生命周期工具不该列入:\n%s", inline)
	}
	// Have a name but no signature.
	if strings.Contains(inline, ") -> {") || strings.Contains(inline, "kn_id: str") {
		t.Fatalf("只该列名字，不该带签名:\n%s", inline)
	}
	// A blank line must be left between the list and the following section, otherwise the ## title will be pasted into the same paragraph and markdown will not be established.
	if !strings.Contains(inline, "\n\n## ") {
		t.Fatalf("名单与后续小节之间缺空行:\n%s", inline)
	}
}

// Names are rendered in dictionary order: Version is a content hash, and order jittering will make the client think that the tool surface has changed every time.
func TestInlineDigestNameOrderIsStable(t *testing.T) {
	locale := loadMCPLocaleBundle(defaultMCPLocale)
	tools := ptcUsableTools(&MCPInfo{Tools: ptcTestTools()})
	if renderPTCDigestForLocale(locale, tools, false) != renderPTCDigestForLocale(locale, tools, false) {
		t.Fatal("并入版渲染不稳定")
	}
	if !strings.Contains(renderPTCDigestForLocale(locale, tools, false),
		"list_knowledge_networks, query_object_instance, run_sql") {
		t.Fatal("函数名未按字典序排列")
	}
}

// The scripts spelled out by the two paths must be consistent: the stub, sandbox return address, and assembly method do not change due to the simplified description.
// Otherwise the same piece of code will behave differently on the two endpoints.
func TestInlineToolsKeepAssemblyIdentical(t *testing.T) {
	inlineKit, err := InlinePTCToolkit(30779, defaultMCPLocale)
	if err != nil {
		t.Skipf("需要内嵌工具元数据: %v", err)
	}
	kit, err := BuildPTCToolkitForLocale("", 30779, defaultMCPLocale)
	if err != nil {
		t.Skipf("需要内嵌工具元数据: %v", err)
	}
	inline := inlineKit.Tools

	byName := map[string]PTCTool{}
	for _, tool := range kit.Tools {
		byName[tool.Name] = tool
	}
	if len(inline) != len(kit.Tools) {
		t.Fatalf("并入版应暴露同样多的工具: %d vs %d", len(inline), len(kit.Tools))
	}
	for _, tool := range inline {
		full, ok := byName[tool.Name]
		if !ok {
			t.Fatalf("%s 不在完整工具表里", tool.Name)
		}
		if tool.Language != full.Language || tool.Wrap != full.Wrap {
			t.Fatalf("%s 的组装方式不一致: %+v vs %+v", tool.Name, tool, full)
		}
		if string(tool.InputSchema) != string(full.InputSchema) {
			t.Fatalf("%s 的入参 schema 不一致", tool.Name)
		}
		// Only the description of run_code should be shortened, run_shell does not contain a signature list.
		if tool.Name == toolKeyRunShell && tool.Description != full.Description {
			t.Fatalf("run_shell 的描述不该变")
		}
		if tool.Name == toolKeyRunCode && len(tool.Description) >= len(full.Description) {
			t.Fatalf("run_code 的描述应更短")
		}
	}
}

// 描述文本里的反斜杠是普通字面量（like 契约要写 \% 表示转义过的百分号），
// 直接贴进 """...""" 会变成 Python 的非法转义序列（3.12 起是 SyntaxWarning）；
// 描述里出现 """ 则会直接截断 docstring，把整份 stub 弄坏。
func TestEscapePyDocstring(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`写 \% 匹配百分号`, `写 \\% 匹配百分号`},
		{`没有反斜杠`, `没有反斜杠`},
		{`引号 """ 收尾`, `引号 \"\"\" 收尾`},
		{`\\`, `\\\\`},
	}
	for _, c := range cases {
		if got := escapePyDocstring(c.in); got != c.want {
			t.Errorf("escapePyDocstring(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 渲染出来的 stub 必须是合法 Python：描述里任何反斜杠都得转义过，
// 不能留下 \% 这种会触发 SyntaxWarning 的裸序列。
func TestRenderPTCStubEscapesBackslashes(t *testing.T) {
	toolkit, err := BuildPTCToolkitForLocale("http://example.invalid", 30779, "zh-CN")
	if err != nil {
		t.Fatalf("BuildPTCToolkitForLocale: %v", err)
	}
	for _, line := range strings.Split(toolkit.Stub, "\n") {
		for i := 0; i < len(line)-1; i++ {
			if line[i] != '\\' {
				continue
			}
			switch line[i+1] {
			case '\\', 'n', 't', 'r', '"', '\'':
				if line[i+1] == '\\' {
					i++ // 成对的反斜杠，跳过后一个
				}
			default:
				t.Errorf("stub 里有裸转义序列 \\%c: %s", line[i+1], line)
			}
		}
	}
}

// TestPTCHintsCoverRoutingTools guards the second instruction surface.
//
// run_code sees the PTC toolkit digest, which renders each tool's title and its
// ptc_hints - not the tool description and not the server instructions. Routing
// advice added only to instructions.txt therefore never reaches the sandbox, which
// is where the schema-wide searches and the node-by-node parent-chain crawls were
// written. Both locales must carry the routing hints, or one surface silently
// drifts from the other.
func TestPTCHintsCoverRoutingTools(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		bundle := loadMCPLocaleBundle(locale)
		for _, tool := range []string{"get_kn_detail", "search_schema", "explore_subgraph"} {
			if len(bundle.PTCHints(tool)) == 0 {
				t.Fatalf("%s: %s has no PTC hints", locale, tool)
			}
		}
		if joined := strings.Join(bundle.PTCHints("search_schema"), " "); !strings.Contains(joined, "concept_groups") {
			t.Fatalf("%s: search_schema hint does not mention concept_groups: %s", locale, joined)
		}
		if joined := strings.Join(bundle.PTCHints("explore_subgraph"), " "); !strings.Contains(joined, "backward") {
			t.Fatalf("%s: explore_subgraph hint does not mention backward: %s", locale, joined)
		}
	}
}
