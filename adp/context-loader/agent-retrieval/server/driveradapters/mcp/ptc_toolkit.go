// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// PTC（代码化工具调用）工具包：把本服务的工具面渲染成「一段说明 + 一份 stub」。
//
// 客户端只给模型一个 run_code 工具，模型写 Python 交沙箱执行，脚本里直接调用
// 这里生成的函数；中间结果留在沙箱，只有 stdout 回到上下文。
//
// 两份产物都从 BuildMCPInfo 的工具目录渲染，与 tools/list 同源——条件注册未启用
// 的工具、按档位装饰过的参数，在这里的表现与实际可调用的工具面完全一致。
//
// 由服务端而非客户端渲染，有两个客户端做不到的地方：
//  1. schema 里没有 bkn_context（它是生命周期守卫在运行时向业务工具索取的），
//     从 tools/list 渲染的客户端会把它当成必填参数写进签名，让模型去填一个它
//     没有的值；
//  2. 只有服务端知道哪些工具真正注册了。
type PTCToolkit struct {
	// Version 是 digest 与 stub 的内容哈希，客户端据此缓存。
	Version string `json:"version"`
	// Digest 是给模型看的函数签名清单，用作 run_code 的工具描述。
	Digest string `json:"digest"`
	// Stub 是沙箱内的 Python 实现，随每次执行内联进脚本。
	Stub string `json:"stub"`
	// SandboxMCPURL 是沙箱回访本服务的地址。沙箱在集群内，用不了浏览器侧的
	// 网关地址；而集群内地址只有服务端知道，让浏览器去配置只会配错。
	SandboxMCPURL string `json:"sandbox_mcp_url"`
}

// 生命周期工具由调用方按轮次接管，沙箱沿用同一个 interaction，不自行开关——
// 否则一次任务会分裂成两条互不关联的证据链。
var ptcSkipTools = map[string]bool{
	"bkn_start_interaction":  true,
	"bkn_finish_interaction": true,
}

// bkn_context 是会话生命周期管道，由 stub 的 _call 自动注入，不该出现在签名里。
var ptcPlumbingParams = map[string]bool{"bkn_context": true}

// schema 默认 response_format=toon，那是为「直接喂给模型」优化的省 token 文本
// 格式。代码模式下返回值先经脚本处理，需要可下标访问的结构，故覆盖为 json。
var ptcDefaultOverrides = map[string]any{"response_format": "json"}

var ptcPyTypes = map[string]string{
	"string": "str", "array": "list", "object": "dict",
	"boolean": "bool", "integer": "int", "number": "float",
}

// 少数工具存在「不看完整 docstring 必写错」的调用约定。完整规则留在 docstring
// （第二级），这里只把最小可用示例提到签名清单（第一级）——实测中模型不会先
// help() 就动手。新增条目的依据应是实测失败，不是臆测。
var ptcHints = map[string][]string{
	"run_sql": {
		"表名必须写成 {{.<resource_id>}} 占位符，id 取自 search_schema 的 data_source.id",
		"或 list_resources 的 resource_id；不可原样写 'resource_id' 字面量。",
		"列名用物理列名。仅单条 SELECT，无 CTE/UNION。",
		`run_sql(sql="SELECT team_name, COUNT(*) c FROM {{.<resource_id>}} GROUP BY team_name")`,
	},
}

const defaultSandboxMCPPath = "/api/agent-retrieval/v1/mcp/"

// sandboxMCPURL 返回沙箱回访本服务的地址。
//
// 尾斜杠不能省：缺斜杠时网关 307 跳转，而沙箱侧用标准库 urllib，它不对 POST
// 跟随重定向——症状是一个没有报文的 400，排查代价远大于这行注释。
func sandboxMCPURL(port int) string {
	if v := strings.TrimSpace(os.Getenv("PTC_SANDBOX_MCP_URL")); v != "" {
		if !strings.HasSuffix(v, "/") {
			v += "/"
		}
		return v
	}
	host := strings.TrimSpace(os.Getenv("PTC_SANDBOX_MCP_HOST"))
	if host == "" {
		host = "agent-retrieval"
	}
	return fmt.Sprintf("http://%s:%d%s", host, port, defaultSandboxMCPPath)
}

type ptcParam struct {
	name     string
	pyType   string
	required bool
	defVal   any
	desc     string
}

func ptcParams(raw json.RawMessage) []ptcParam {
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Default     any    `json:"default"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}
	// JSON 对象无序，按名字排序保证两次渲染字节一致——Version 是内容哈希，
	// 顺序抖动会让客户端每次都以为工具面变了。
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		if !ptcPlumbingParams[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	params := make([]ptcParam, 0, len(names))
	for _, name := range names {
		spec := schema.Properties[name]
		def := spec.Default
		if override, ok := ptcDefaultOverrides[name]; ok {
			def = override
		}
		pyType, ok := ptcPyTypes[spec.Type]
		if !ok {
			pyType = "object"
		}
		params = append(params, ptcParam{
			name: name, pyType: pyType, required: required[name],
			defVal: def, desc: spec.Description,
		})
	}
	// 必填在前：Python 不允许有默认值的参数排在无默认值的之前。
	sort.SliceStable(params, func(i, j int) bool { return params[i].required && !params[j].required })
	return params
}

func pyLiteral(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "\\'") + "'"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func ptcSignature(tool MCPToolInfo) string {
	parts := make([]string, 0, 8)
	for _, p := range ptcParams(tool.InputSchema) {
		if p.required {
			parts = append(parts, fmt.Sprintf("%s: %s", p.name, p.pyType))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s = %s", p.name, p.pyType, pyLiteral(p.defVal)))
	}
	return fmt.Sprintf("%s(%s)", tool.Name, strings.Join(parts, ", "))
}

// ptcReturnKeys 渲染返回值顶层键。键名在各工具间并不统一（列表类有的叫 entries、
// 有的叫 datas），模型无从推断——不写出来首次调用就会因 KeyError 失败。
func ptcReturnKeys(tool MCPToolInfo) string {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if len(tool.OutputSchema) == 0 || json.Unmarshal(tool.OutputSchema, &schema) != nil {
		return "dict"
	}
	if len(schema.Properties) == 0 {
		return "dict"
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return "{" + strings.Join(names, ", ") + "}"
}

func ptcUsableTools(info *MCPInfo) []MCPToolInfo {
	tools := make([]MCPToolInfo, 0, len(info.Tools))
	for _, t := range info.Tools {
		if t.Name == "" || ptcSkipTools[t.Name] {
			continue
		}
		tools = append(tools, t)
	}
	// 分组按组内最小 Order 排，而非组名字典序：Order 编码了「先发现、再查询、
	// 后执行」的使用顺序，字典序会把它打乱。
	groupRank := map[string]int{}
	for _, t := range tools {
		g := ptcGroupOf(t)
		if cur, ok := groupRank[g]; !ok || t.Order < cur {
			groupRank[g] = t.Order
		}
	}
	sort.SliceStable(tools, func(i, j int) bool {
		gi, gj := ptcGroupOf(tools[i]), ptcGroupOf(tools[j])
		if gi != gj {
			return groupRank[gi] < groupRank[gj]
		}
		return tools[i].Order < tools[j].Order
	})
	return tools
}

func ptcGroupOf(t MCPToolInfo) string {
	if t.GroupTitle != "" {
		return t.GroupTitle
	}
	if t.Group != "" {
		return t.Group
	}
	return "其他"
}

func renderPTCDigest(tools []MCPToolInfo) string {
	var b strings.Builder
	b.WriteString(`下列 BKN 能力已在作用域内，直接调用即可，无需 import。
只有 stdout 会返回给你——中间结果不进上下文，因此请在脚本内完成过滤与聚合，
只 print 你真正需要的内容。调用失败抛 ` + "`ToolError`" + `。

签名末尾的 ` + "`-> {…}`" + ` 是返回值顶层键。**其中部分键可能不出现**
（如 ` + "`total_count`" + ` 在带过滤的查询里就没有），一律用 ` + "`.get()`" + ` 取，不要下标。
过滤字段必须是该对象类真实的数据属性名——先用 ` + "`get_object_types`" + ` 查
` + "`data_properties`" + `，不要按语义猜。

## 可用函数
`)
	group := ""
	for _, t := range tools {
		if g := ptcGroupOf(t); g != group {
			if group != "" {
				b.WriteString("```\n")
			}
			fmt.Fprintf(&b, "\n### %s\n\n```python\n", g)
			group = g
		}
		fmt.Fprintf(&b, "%s -> %s\n", ptcSignature(t), ptcReturnKeys(t))
		if t.Title != "" {
			fmt.Fprintf(&b, "    # %s\n", t.Title)
		}
		for _, hint := range ptcHints[t.Name] {
			fmt.Fprintf(&b, "    #   %s\n", hint)
		}
	}
	if group != "" {
		b.WriteString("```\n")
	}
	b.WriteString(`
## 一段脚本解决整个问题

每发起一次 run_code 就是一次往返。先跑一次看结构、再跑一次取数、再跑一次聚合，
与逐个调用工具没有区别——探查与求解要用变量串在同一段脚本里：

` + "```python" + `
kn = "<当前知识网络 id>"

# 1) 先拿结构，结果直接喂给下一步，不要单独跑一轮回来看
detail = get_kn_detail(kn_id=kn)
ot = next(o for o in detail.get("object_types", []) if "进球" in o.get("name", ""))
fields = [p.get("name") for p in
          get_object_types(kn_id=kn, ids=[ot["id"]])["object_types"][0].get("data_properties", [])]

# 2) 用刚拿到的真实字段名过滤，不要按语义猜
team_field = next(f for f in fields if "team" in f)
rows = query_object_instance(kn_id=kn, ot_id=ot["id"], limit=1000)

# 3) 聚合也在这里做完，只 print 结论
import collections
top = collections.Counter(r.get(team_field) for r in rows.get("datas", [])).most_common(3)
print(top)
` + "```" + `

沙箱是完整的 Python 3.11：pandas、numpy、scipy、requests、httpx、sqlite3 与全部
标准库都在，分组、连接、统计交给它们，不要为此多跑一轮。

需要执行命令时用 ` + "`subprocess.run(cmd, shell=True, capture_output=True, text=True)`" + `，
沙箱是一台完整的 Linux，无需另找工具。

## 大结果写文件，只 print 摘要

回到你面前的只有 stdout，因此**不要把大结果打印出来**：写进 ` + "`/workspace`" + ` 下的文件，
再打印头部若干行、行数、字段名这类足以判断下一步的信息。需要细看时读取文件的某一段，
而不是整份倒出来。

` + "```python" + `
import json, pathlib
rows = query_object_instance(kn_id=kn, ot_id=ot_id, limit=5000).get("datas", [])
path = pathlib.Path("/workspace/rows.json")
path.write_text(json.dumps(rows, ensure_ascii=False))
print(f"{len(rows)} 行已写入 {path}，字段：{sorted(rows[0])[:12] if rows else []}")
print(json.dumps(rows[:3], ensure_ascii=False)[:600])   # 只看头部三行
` + "```" + `

` + "`/workspace`" + ` 里的文件在同一沙箱会话内跨次调用通常仍在，但会话是池化复用的、
不保证命中同一个，所以再次使用前先 ` + "`Path(...).exists()`" + ` 判断，不存在就重算。

不确定的地方用代码兜住而不是回到对话：取值一律 ` + "`.get()`" + `，可能失败的分支用
try/except 包住并 print 出关键中间信息，让一次执行既拿到答案、又带回排查线索。

## 参数写不准时

完整 schema 在每个函数的 docstring 里，**在同一段脚本里** ` + "`help(fn)`" + ` 自查后接着往下写，
不要为了看文档单独跑一轮：

` + "```python" + `
help(query_object_instance)
` + "```" + `

特别是 ` + "`condition`" + ` 的 ` + "`operation`" + `：` + "`match` / `knn`" + ` 能否使用取决于该属性的
` + "`condition_operations`" + `（见 ` + "`get_object_types`" + ` 返回），从 ` + "`type`" + ` 推不出来。

## 错误处理

调用失败抛 ` + "`ToolError`" + `，message 为服务端原文的 JSON，形如：

` + "```json" + `
{"error":{"code":"...","message":"...","required_action":"...","retryable":false}}
` + "```" + `

**先读 ` + "`retryable`" + ` 再决定下一步。** 为 false 表示同样的请求再发一次仍会失败——
换参数、换工具，或者把这条错误原样 print 出来交回，不要重试。原地重试三次只是把
同一个失败抄三遍，既拖慢一轮又什么都没换来。

` + "`required_action`" + ` 有值时按它做（例如提示先查某个工具）。属于参数写错的（字段名
不存在、算子不支持），在同一段脚本里改完接着跑，不必回到对话轮次。

不要用 ` + "`try/except` + `pass`" + ` 把错误吞掉：你看不到的东西没法修，而调用方只会
收到一段没有解释的空输出。
`)
	return b.String()
}

// ptcStubPreamble 是沙箱侧运行时。只用标准库：MCP streamable HTTP 就是 JSON-RPC
// over POST，urllib 足够，沙箱镜像无需预装任何依赖，也就没有 SDK 版本漂移。
const ptcStubPreamble = `"""BKN 能力的沙箱侧 stub —— 由 context-loader 生成，请勿手工编辑。

凭据与会话上下文经 _configure(event) 注入，由调用方在发起执行时下发。
"""

import json
import urllib.request

_CFG = {}
_SESSION = {}

# 显式不走代理：MCP 端点是集群内地址，任何继承来的代理配置都只会让请求发不出去。
# 且 urllib 一旦认定要走代理就改发 absolute-form 请求行（POST http://host/path），
# 网关对此直接 400。
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


class ToolError(RuntimeError):
    """工具调用失败。message 为服务端原文，供模型据此修正参数后重试。"""


def _configure(event):
    """由执行入口调用，注入 MCP 端点、凭据与生命周期上下文。"""
    _CFG.update(event)
    _SESSION.clear()


def _rpc(method, params=None, notify=False):
    body = {"jsonrpc": "2.0", "method": method}
    if params is not None:
        body["params"] = params
    if not notify:
        body["id"] = _SESSION.get("seq", 0) + 1
        _SESSION["seq"] = body["id"]
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "Authorization": "Bearer " + _CFG["token"],
    }
    if _SESSION.get("id"):
        headers["Mcp-Session-Id"] = _SESSION["id"]
    request = urllib.request.Request(
        _CFG["mcp"], data=json.dumps(body).encode(),
        headers=headers, method="POST",
    )
    response = _OPENER.open(request, timeout=_CFG.get("timeout", 120))
    if not _SESSION.get("id"):
        _SESSION["id"] = response.headers.get("Mcp-Session-Id")
    raw = response.read().decode()
    if not raw.strip():
        return None
    for line in raw.splitlines():
        if line.startswith("data: "):
            return json.loads(line[6:])
    return json.loads(raw)


def _ensure_session():
    """MCP 会话在模块级复用，一次执行内 initialize 只发生一次。"""
    if _SESSION.get("ready"):
        return
    _rpc("initialize", {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "bkn-sandbox", "version": "1"},
    })
    _rpc("notifications/initialized", {}, notify=True)
    _SESSION["ready"] = True


def _call(tool, args):
    """调用 MCP 工具。None 值不下发，交由服务端使用 schema 默认值。"""
    _ensure_session()
    payload = {k: v for k, v in args.items() if v is not None}
    # 业务类工具受会话守卫约束，缺 bkn_context 会被拒（conversation_required）。
    # 该上下文由调用方透传，模型无需感知，故不出现在函数签名里。
    if _CFG.get("bkn"):
        payload["bkn_context"] = _CFG["bkn"]

    result = _rpc("tools/call", {"name": tool, "arguments": payload})["result"]
    text = "".join(c["text"] for c in result["content"] if c["type"] == "text")
    if result.get("isError") or result.get("is_error"):
        raise ToolError(tool + ": " + text)
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        # response_format=toon 等非 JSON 形态按原文返回
        return text
`

func renderPTCStub(tools []MCPToolInfo) string {
	var b strings.Builder
	b.WriteString(ptcStubPreamble)
	for _, t := range tools {
		params := ptcParams(t.InputSchema)
		fmt.Fprintf(&b, "\n\ndef %s -> dict:\n", ptcSignature(t))
		b.WriteString(`    """` + strings.TrimSpace(t.Description) + "\n")
		for _, p := range params {
			if p.desc != "" {
				fmt.Fprintf(&b, "\n    %s: %s\n", p.name, strings.TrimSpace(p.desc))
			}
		}
		b.WriteString(`    """` + "\n")
		args := make([]string, 0, len(params))
		for _, p := range params {
			args = append(args, fmt.Sprintf("%q: %s", p.name, p.name))
		}
		fmt.Fprintf(&b, "    return _call(%q, {%s})\n", t.Name, strings.Join(args, ", "))
	}
	b.WriteString("\n\n__all__ = [\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "    %q,\n", t.Name)
	}
	b.WriteString("    \"ToolError\",\n]\n")
	return b.String()
}

// BuildPTCToolkit 渲染 PTC 工具包。endpoint 与 BuildMCPInfo 一致（仅用于自描述），
// port 是本服务监听端口，用于推导沙箱回访地址。
func BuildPTCToolkit(endpoint string, port int) (*PTCToolkit, error) {
	info, err := BuildMCPInfo(endpoint)
	if err != nil {
		return nil, err
	}
	tools := ptcUsableTools(info)
	digest := renderPTCDigest(tools)
	stub := renderPTCStub(tools)
	sum := sha256.Sum256([]byte(digest + "\x00" + stub))
	return &PTCToolkit{
		Version:       "sha256:" + hex.EncodeToString(sum[:]),
		Digest:        digest,
		Stub:          stub,
		SandboxMCPURL: sandboxMCPURL(port),
	}, nil
}
