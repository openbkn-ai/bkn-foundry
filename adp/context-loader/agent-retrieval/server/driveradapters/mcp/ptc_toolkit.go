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
	// Tools 是要暴露给模型的工具全表。客户端应当遍历它建工具，不要按名字硬编码：
	// 这样以后加工具是纯服务端改动。Digest/Stub 是 run_code 那一项的展开，保留
	// 为顶层字段只为兼容先于本字段上线的客户端。
	Tools []PTCTool `json:"tools"`
}

// PTCTool 一个要暴露给模型的工具。客户端据此建工具、组装执行请求。
type PTCTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Language 直接填进执行工厂 /function/execute 的 language 字段。
	// 沙箱控制面只认 python / javascript / shell，bash 会被 422 拒掉。
	Language string `json:"language"`
	// Wrap 说明客户端该如何把模型的入参组装成 code：
	//   handler    —— 取 stub，拼上 "def handler(event):"，模型代码缩进进函数体，
	//                 凭据与 bkn_context 走 event 下发（沿用既有 run_code 逻辑）。
	//   cd_workdir —— 在模型给的命令前面拼一行 cd 到本次对话的工作目录。
	// 新增取值必须同步客户端，故取值集合刻意保持极小。
	Wrap string `json:"wrap"`
}

const (
	// ptcWrapHandler 见 PTCTool.Wrap。
	ptcWrapHandler = "handler"
	// ptcWrapCdWorkdir 见 PTCTool.Wrap。
	ptcWrapCdWorkdir = "cd_workdir"
)

// ptcRunCodeSchema run_code 的入参。
const ptcRunCodeSchema = `{
  "type": "object",
  "properties": {
    "code": {"type": "string", "description": "要执行的 Python 代码。只有 print 的内容会返回。"},
    "timeout": {"type": "integer", "default": 60, "description": "执行超时秒数"}
  },
  "required": ["code"]
}`

// ptcRunShellSchema run_shell 的入参。
const ptcRunShellSchema = `{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "要执行的 shell 命令，交给 sh -c 执行，可以用管道和 &&。"},
    "timeout": {"type": "integer", "default": 60, "description": "执行超时秒数"}
  },
  "required": ["command"]
}`

// ptcRunShellDescription run_shell 的工具描述。
//
// 刻意把边界写死：run_shell 一行就能发，run_code 要构思一段脚本，模型天然偏向
// 前者。不划清楚，它会用 curl 手搓 MCP 调用，把「一段脚本解决整个问题」重新拆成
// 一轮一次调用——那正是 PTC 要消灭的东西。
const ptcRunShellDescription = `在沙箱里执行一条 shell 命令，返回 stdout 与 stderr。

工作目录与 ` + "`run_code`" + ` 相同，两个工具看到的是同一批文件：` + "`run_code`" + ` 写下的
结果这里能直接 ` + "`wc`" + `、` + "`head`" + `、` + "`grep`" + `。

**只用于查看环境与文件**：看目录、查行数、抽取片段、看磁盘占用、确认某个命令存在。

**不要用它取 BKN 数据。** 知识网络、对象类、实例、SQL 一律走 ` + "`run_code`" + `——那边有
现成的函数，这里没有凭据，用 ` + "`curl`" + ` 手搓 MCP 调用会把一次任务拆成很多轮，
既慢又容易错。

沙箱是完整的 Linux：coreutils、` + "`grep`" + `、` + "`awk`" + `、` + "`sed`" + `、` + "`jq`" + `、` + "`curl`" + `、
` + "`python3`" + ` 都在。

示例：
` + "```" + `
ls -la && wc -l *.json
head -c 400 rows.json
du -sh . && df -h /workspace
` + "```" + `
`

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

## 能下推的聚合一律下推

计数、求和、分组、排序、Top-N 这类，**优先写一条 ` + "`run_sql`" + `**，让数据库算完只回结果，
不要用 ` + "`query_object_instance`" + ` 把行拉进沙箱再统计——那既慢又受 ` + "`limit`" + ` 截断，
统计口径会悄悄错。

` + "```python" + `
# 对：一条 SQL 回三行
run_sql(sql="SELECT team_name, COUNT(*) c FROM {{.<resource_id>}} GROUP BY team_name ORDER BY c DESC LIMIT 3")

# 错：拉 5000 行回来自己数，还可能没拉全
rows = query_object_instance(kn_id=kn, ot_id="goals", limit=5000)
` + "```" + `

沙箱里的 pandas、numpy、scipy、sqlite3 与全部标准库，留给 SQL 表达不了的部分：
跨数据源关联、非 SQL 数据源、按中间结果分支、扇出。那些才是代码模式真正的用武之地。

## 一次执行同时产出答案与依据

多跑一轮，最常见的原因不是想不出解法，而是**对数据口径有误解**：以为只有男足却混着女足，
以为球队名唯一却有 West Germany 与 Germany 两条。这类问题撞上一次修一次，就变成了
一轮一次调用。

对策是在**同一段脚本里**把你依赖的口径先打出来，再给答案：

` + "```python" + `
print("取值范围:", run_sql(sql="SELECT DISTINCT tournament_name FROM {{.<resource_id>}}")["entries"][:20])
print("答案:", run_sql(sql="SELECT team_name, COUNT(*) c FROM {{.<resource_id>}} GROUP BY team_name ORDER BY c DESC LIMIT 5")["entries"])
` + "```" + `

这样即使口径判断错了，你也已经拿到了改对它所需的全部信息，下一轮直接给最终答案，
而不是再花一轮"看一眼"。

不确定的地方用代码兜住而不是回到对话：取值一律 ` + "`.get()`" + `，可能失败的分支用
try/except 包住并 print 出关键中间信息，让一次执行既拿到答案、又带回排查线索。

## 执行 shell 命令

另有一个 ` + "`run_shell`" + ` 工具，和这里共用工作目录，用来单独看一眼环境或文件
（` + "`ls`" + `、` + "`wc`" + `、` + "`head`" + `）。

但**只要 shell 是解题过程的一环，就写在这段脚本里**，用 ` + "`subprocess`" + ` 调，
不要为它单开一轮——那就退回一次调用一轮了：

` + "```python" + `
import subprocess
r = subprocess.run("wc -l rows.jsonl && head -c 300 rows.jsonl",
                   shell=True, capture_output=True, text=True)
print(r.stdout or r.stderr)          # 非零退出时诊断在 stderr 里
` + "```" + `

` + "`curl`" + `、` + "`jq`" + `、` + "`awk`" + `、` + "`sort`" + ` 这类都在。但凡 Python 能直接做的（读文件、
统计、JSON 处理）就别绕 shell，省一层引号转义。

## 工作目录与大结果

回到你面前的只有 stdout，因此**不要把大结果打印出来**：写进文件，再打印行数、字段名、
头部若干行这类足以判断下一步的信息。需要细看时读取文件的某一段，而不是整份倒出来。

脚本启动时当前目录已经切到本次会话专属的工作目录（` + "`WORKDIR`" + ` 变量指向它），
**直接用相对路径读写**。不要写 ` + "`/workspace/xxx`" + ` 的绝对路径：那是所有会话共用的
根目录，会和别人撞名。

` + "```python" + `
import json, pathlib
rows = query_object_instance(kn_id=kn, ot_id=ot_id, limit=5000).get("datas", [])
pathlib.Path("rows.json").write_text(json.dumps(rows, ensure_ascii=False))
print(f"{len(rows)} 行已落盘，字段：{sorted(rows[0])[:12] if rows else []}")
print(json.dumps(rows[:3], ensure_ascii=False)[:600])   # 只看头部三行
` + "```" + `

文件在同一对话的多次执行之间是保留的，下一段脚本可以直接接着用，不必重新取数：

` + "```python" + `
import json, pathlib
p = pathlib.Path("rows.json")
rows = json.loads(p.read_text()) if p.exists() else query_object_instance(...)["datas"]
` + "```" + `

` + "`exists()`" + ` 判断还是要写：换了对话、或者上一段脚本在落盘前就失败了，文件就不在。

` + "`run_shell`" + ` 落在同一个目录里，两个工具看到的是同一批文件。

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
import os
import pathlib
import urllib.request

_CFG = {}
_SESSION = {}

# 本次会话的工作目录。沙箱 /workspace 是所有调用方共用的一个目录（执行接口不接受
# session_id，池子实测恒命中同一个会话），直接在根上写 rows.json 这类名字，既会被
# 别的会话覆盖，也可能读到别人的数据。这里按 conversation 切出独立子目录并 chdir
# 进去，脚本用相对路径即可，无需关心隔离。
WORKDIR = pathlib.Path("/workspace")

# 显式不走代理：MCP 端点是集群内地址，任何继承来的代理配置都只会让请求发不出去。
# 且 urllib 一旦认定要走代理就改发 absolute-form 请求行（POST http://host/path），
# 网关对此直接 400。
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


class ToolError(RuntimeError):
    """工具调用失败。message 为服务端原文，供模型据此修正参数后重试。"""


def _configure(event):
    """由执行入口调用，注入 MCP 端点、凭据与生命周期上下文，并准备工作目录。"""
    global WORKDIR
    _CFG.update(event)
    _SESSION.clear()

    # 目录名由 conversation_id 归一化而来，不做哈希：run_shell 走 language=shell，
    # 不经过本 stub，得在浏览器侧算出同一个路径才能进到同一个目录。而浏览器的
    # crypto.subtle 在非 HTTPS 源下不可用（部署常是裸 HTTP），算不了 sha1。
    # 归一化两边都能实现，且 ls /workspace 时还能直接看出是哪个对话。
    # 字符集写死成 ASCII 白名单，不用 c.isalnum()：Python 的 isalnum() 认 Unicode，
    # "名".isalnum() 为真，于是中文 conversation_id 在这里被原样保留，而 Go 侧
    # （run_shell 走那条路）只认 ASCII，会换成 -。两边就落到不同目录了。
    _SAFE = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
    conversation = str((event.get("bkn") or {}).get("conversation_id") or "").strip()
    safe = "".join(c if c in _SAFE else "-" for c in conversation)[:64]
    candidate = pathlib.Path("/workspace") / ("conv-" + safe if safe else "shared")
    try:
        candidate.mkdir(parents=True, exist_ok=True)
        os.chdir(candidate)
        WORKDIR = candidate
    except OSError:
        # 沙箱换了镜像、/workspace 只读之类的情况下不要连累整段脚本，退回当前目录。
        WORKDIR = pathlib.Path(os.getcwd())


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
	return buildPTCToolkitFrom(ptcUsableTools(info), port)
}

// buildPTCToolkitFrom 从已筛好的工具目录渲染工具包。与 BuildPTCToolkit 分开，
// 是为了让测试不必起一个真实端点就能覆盖工具表与版本号。
func buildPTCToolkitFrom(tools []MCPToolInfo, port int) (*PTCToolkit, error) {
	digest := renderPTCDigest(tools)
	stub := renderPTCStub(tools)
	exposed := []PTCTool{
		{
			Name:        "run_code",
			Description: digest,
			InputSchema: json.RawMessage(ptcRunCodeSchema),
			Language:    "python",
			Wrap:        ptcWrapHandler,
		},
		{
			Name:        "run_shell",
			Description: ptcRunShellDescription,
			InputSchema: json.RawMessage(ptcRunShellSchema),
			Language:    "shell",
			Wrap:        ptcWrapCdWorkdir,
		},
	}
	// Version 覆盖工具全表：客户端按它缓存，只要模型看到的工具面变了就得失效，
	// 光哈希 digest+stub 会让新增工具、改描述这类改动悄悄用着旧缓存。
	fingerprint, err := json.Marshal(exposed)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append(append([]byte(digest), 0), append([]byte(stub), fingerprint...)...))
	return &PTCToolkit{
		Version:       "sha256:" + hex.EncodeToString(sum[:]),
		Digest:        digest,
		Stub:          stub,
		SandboxMCPURL: sandboxMCPURL(port),
		Tools:         exposed,
	}, nil
}
