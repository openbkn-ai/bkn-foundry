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
				"entries":{},"total_count":{}}}`),
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
				"datas":{},"total_count":{},"search_after":{}}}`),
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

func TestPTCDigestRendersSignatures(t *testing.T) {
	digest := ptcTestDigest()

	// 必填在前、可选带默认值——Python 不允许有默认值的参数排在无默认值之前。
	if !strings.Contains(digest, "query_object_instance(kn_id: str, ot_id: str,") {
		t.Fatalf("必填参数未排在前面:\n%s", digest)
	}
	// 返回键必须写出来：键名在各工具间不统一（entries 与 datas 并存），
	// 模型无从推断，不写出来首次调用就会因 KeyError 失败。
	if !strings.Contains(digest, "-> {entries, total_count}") ||
		!strings.Contains(digest, "-> {datas, search_after, total_count}") {
		t.Fatalf("返回键缺失:\n%s", digest)
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
