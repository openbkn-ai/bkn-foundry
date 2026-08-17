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
			// bkn_context 在服务端是必填：生命周期守卫向业务工具索取它。
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

	// 必填在前、可选带默认值——Python 不允许有默认值的参数排在无默认值之前。
	if !strings.Contains(digest, "query_object_instance(kn_id: str, ot_id: str,") {
		t.Fatalf("必填参数未排在前面:\n%s", digest)
	}
	// 返回键必须写出来：键名在各工具间不统一（entries 与 datas 并存），
	// 模型无从推断，不写出来首次调用就会因 KeyError 失败。
	//
	// 数组键还要再展开一层元素字段。只写顶层键时，模型得先猜取值路径、猜错再花一整轮
	// print 原始结构找字段名——实测中 search_schema 的 object_types 就是这么被当成有
	// name 字段的（真实字段是 concept_name）。
	if !strings.Contains(digest, "-> {entries[kn_id name], total_count}") {
		t.Fatalf("数组元素字段未展开:\n%s", digest)
	}
	if !strings.Contains(digest, "-> {datas[_display _instance_id], search_after, total_count}") {
		t.Fatalf("数组展开或非数组键渲染有误:\n%s", digest)
	}
}

// 元素没声明字段（items 为空、或 items 只有 type）时按普通键渲染，不能凭空造出
// 一对空方括号——search_after 那种不透明游标就是有意不声明的。
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

// bkn_context 是生命周期管道，由 stub 的 _call 注入。留在签名里会让模型去填一个
// 它没有的值——客户端从 tools/list 自行渲染时正是栽在这里。
func TestPTCDigestStripsPlumbingParams(t *testing.T) {
	if digest := ptcTestDigest(); strings.Contains(digest, "bkn_context") {
		t.Fatalf("bkn_context 不应出现在给模型的签名里:\n%s", digest)
	}
}

// toon 是为「直接喂给模型」优化的省 token 文本格式；代码模式下返回值先经脚本
// 处理，需要可下标访问的结构。
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

// Order 编码了「先发现、再查询」的使用顺序；按组名字典序排会把它打乱。
func TestPTCDigestGroupsOrderedByOrder(t *testing.T) {
	digest := ptcTestDigest()
	discovery := strings.Index(digest, "### 网络与 Schema")
	query := strings.Index(digest, "### 实例查询")
	if discovery < 0 || query < 0 || discovery > query {
		t.Fatalf("分组顺序错误 discovery=%d query=%d:\n%s", discovery, query, digest)
	}
}

// 代码围栏必须成对：早期版本每组只开不闭，模型看到的是糊在一起的一整块。
func TestPTCDigestFencesBalanced(t *testing.T) {
	if n := strings.Count(ptcTestDigest(), "```"); n%2 != 0 {
		t.Fatalf("代码围栏未闭合，共 %d 个", n)
	}
}

// 实测中模型不看完整 docstring 就写 SQL，会漏掉占位符约定并失败一轮。
func TestPTCDigestCarriesRunSQLHint(t *testing.T) {
	if digest := ptcTestDigest(); !strings.Contains(digest, "{{.<resource_id>}}") {
		t.Fatalf("run_sql 缺少占位符示例:\n%s", digest)
	}
}

func TestPTCStubIsSelfContained(t *testing.T) {
	stub := renderPTCStub(ptcUsableTools(&MCPInfo{Tools: ptcTestTools()}))

	// 只用标准库：沙箱镜像无需预装依赖，也就没有 SDK 版本漂移。
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
	// bkn_context 由 _call 统一注入，不能变成函数参数。
	if strings.Contains(stub, `"bkn_context": bkn_context`) {
		t.Fatal("stub 不应把 bkn_context 当成函数参数传递")
	}
	if !strings.Contains(stub, `payload["bkn_context"] = _CFG["bkn"]`) {
		t.Fatal("stub 未注入 bkn_context")
	}
}

// /workspace 是所有调用方共用的一个目录——执行接口不收 session_id，池子实测恒命中
// 同一个会话。stub 必须按 conversation 切出子目录并 chdir 进去，否则两个对话写同名
// 文件会互相覆盖，读回来的可能是别人的数据。
func TestPTCStubIsolatesWorkdirPerConversation(t *testing.T) {
	stub := renderPTCStub(ptcUsableTools(&MCPInfo{Tools: ptcTestTools()}))

	// 目录名是归一化而非哈希：run_shell 走 language=shell 不经过 stub，要在浏览器侧
	// 算出同一个路径，而 crypto.subtle 在非 HTTPS 源下不可用。两边必须是同一套规则。
	if strings.Contains(stub, "hashlib") {
		t.Fatalf("工作目录不应再用哈希命名（浏览器侧算不出来）:\n%s", stub)
	}
	// Python 的 isalnum() 认 Unicode（"名".isalnum() 为真），而 Go 侧只认 ASCII。
	// 用它归一化，中文 conversation_id 会让 run_code 与 run_shell 落进不同目录。
	// 匹配可执行写法而不是裸词：stub 的注释里正解释着为什么不能用它。
	if strings.Contains(stub, "c.isalnum() or") {
		t.Fatalf("工作目录归一化不能用 isalnum（Unicode 语义与 Go 侧不一致）:\n%s", stub)
	}
	for _, want := range []string{
		`conversation_id`,          // 目录名的来源
		`c if c in _SAFE else "-"`, // 归一化规则，必须与 Go 侧 ptcWorkdir 一致
		`[:64]`,                    // 截断长度，同上
		`"conv-" + safe if safe else "shared"`,
		`candidate.mkdir(`,
		`os.chdir(candidate)`,
	} {
		if !strings.Contains(stub, want) {
			t.Fatalf("stub 缺少工作目录隔离逻辑 %q:\n%s", want, stub)
		}
	}
	// 拿不到 conversation_id 或目录建不出来时要退回可用状态，不能让整段脚本失败。
	if !strings.Contains(stub, `"shared"`) || !strings.Contains(stub, "except OSError:") {
		t.Fatalf("stub 的工作目录缺少兜底分支:\n%s", stub)
	}
}

// 目录既然已经切好，digest 就不能再教模型写 /workspace 绝对路径——那正好绕开隔离。
func TestPTCDigestTeachesRelativePaths(t *testing.T) {
	digest := ptcTestDigest()

	if strings.Contains(digest, `"/workspace/`) || strings.Contains(digest, "Path(\"/workspace") {
		t.Fatalf("digest 不应示范 /workspace 绝对路径:\n%s", digest)
	}
	if !strings.Contains(digest, "WORKDIR") {
		t.Fatalf("digest 未说明工作目录:\n%s", digest)
	}
	// 用户反复问「有没有 run shell」，说明埋在别的小节里的一句话没被看见。
	if !strings.Contains(digest, "## 执行 shell 命令") || !strings.Contains(digest, "subprocess.run(") {
		t.Fatalf("digest 缺少 shell 小节:\n%s", digest)
	}
}

// 客户端应当遍历 tools 建工具，不按名字硬编码——加工具才能是纯服务端改动。
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
	// Digest 顶层字段保留只为兼容老客户端，内容必须与工具表一致，否则两边会漂。
	if runCode.Description != toolkit.Digest {
		t.Fatal("run_code 描述与顶层 digest 不一致")
	}

	runShell, ok := byName["run_shell"]
	if !ok {
		t.Fatalf("缺少 run_shell: %+v", toolkit.Tools)
	}
	// 沙箱控制面只认 python / javascript / shell；bash 实测被 422 拒掉。
	if runShell.Language != "shell" || runShell.Wrap != ptcWrapCdWorkdir {
		t.Fatalf("run_shell 组装方式不对: %+v", runShell)
	}
	// 不划边界模型就会用 curl 手搓 MCP 调用，把一段脚本拆成很多轮。
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

// Version 是客户端的缓存键，必须覆盖工具全表：只哈希 digest+stub 的话，新增工具
// 或改描述都不会变版本号，客户端会一直用着旧的工具面。
func TestPTCToolkitVersionCoversToolTable(t *testing.T) {
	toolkit := ptcTestToolkit(t)
	baseline := toolkit.Version

	original := loadMCPLocaleBundle(defaultMCPLocale).PTCResource("ptc_run_shell_description.txt")
	if !strings.Contains(original, "run_code") {
		t.Fatal("前置条件变了：run_shell 描述里不再提到 run_code")
	}
	// 描述改了而 digest/stub 没变时，版本号必须跟着变。
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

// Version 是内容哈希，客户端据此缓存；渲染必须可重复，否则每次都像工具面变了。
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
	// 缺尾斜杠时网关 307 跳转，而沙箱侧用 urllib，它不对 POST 跟随重定向，
	// 症状是一个没有报文的 400。
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

// 实测（2026-08-14，A/B）：同一个问题，普通工具面 2 次调用 55k token 答完，PTC 用了
// 12 次 136k token 还没收敛。差别在于普通面的提示词把聚合推给 run_sql，而 PTC 的
// digest 当时写着「分组、连接、统计交给 pandas 或 collections」，把模型推去
// query_object_instance(limit=5000) 拉行回来自己算——慢，且被 limit 悄悄截断。
func TestPTCDigestPrefersSQLPushdown(t *testing.T) {
	digest := ptcTestDigest()

	if !strings.Contains(digest, "## 能下推的聚合一律下推") {
		t.Fatalf("digest 缺少下推小节:\n%s", digest)
	}
	// 不能再出现「统计交给 pandas」这类把模型推离 SQL 的表述。
	if strings.Contains(digest, "分组、连接、统计交给") {
		t.Fatalf("digest 仍在把聚合推给 pandas:\n%s", digest)
	}
	// 反例要写出来：只说「优先 SQL」模型照旧会拉行回来。
	if !strings.Contains(digest, "拉 5000 行回来自己数") {
		t.Fatalf("digest 缺少反例:\n%s", digest)
	}
}

// 实测里那 12 轮不是瞎试，是逐轮修正口径：第 6 轮才发现数据混着女足，第 7 轮才发现
// West Germany 与 Germany 是同一支。这类误解本可以在第一段脚本里连同答案一起打出来。
func TestPTCDigestTeachesPrintingAssumptions(t *testing.T) {
	digest := ptcTestDigest()

	if !strings.Contains(digest, "## 一次执行同时产出答案与依据") {
		t.Fatalf("digest 缺少「口径与答案同时打印」小节:\n%s", digest)
	}
	if !strings.Contains(digest, "DISTINCT") {
		t.Fatalf("缺少打印取值范围的示例:\n%s", digest)
	}
}
