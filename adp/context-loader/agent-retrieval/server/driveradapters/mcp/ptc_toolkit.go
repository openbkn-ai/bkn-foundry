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
	// PTC 暴露的两个工具名。并进业务工具面时要按名字挑出 run_code 换描述，
	// 字面量散在两处容易改漏。
	toolKeyRunCode  = "run_code"
	toolKeyRunShell = "run_shell"

	// ptcWrapHandler 见 PTCTool.Wrap。
	ptcWrapHandler = "handler"
	// ptcWrapCdWorkdir 见 PTCTool.Wrap。
	ptcWrapCdWorkdir = "cd_workdir"
)

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
// ptcReturnKeys 渲染返回结构，数组键再往下展开一层元素字段。
//
// 只写顶层键是不够的。代码模式下调用方必须**先写出取值路径再执行**，取不到就得
// 多花一轮把原始结构打出来找字段名——实测中 search_schema 的 object_types 因此被
// 当成有 name 字段（实际是 concept_name），一整轮浪费在探查上，而探查本身又要把
// 原始数据 print 回上下文，正好抵消了代码模式省上下文的意义。
//
// 只展开一层：再深就把签名清单撑成 schema 全文了，而第二层往下可以在脚本里
// help() 或直接看值。
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

	rendered := make([]string, 0, len(names))
	for _, name := range names {
		if inner := ptcItemKeys(schema.Properties[name]); inner != "" {
			rendered = append(rendered, name+"["+inner+"]")
			continue
		}
		rendered = append(rendered, name)
	}
	return "{" + strings.Join(rendered, ", ") + "}"
}

// ptcItemKeys 取数组元素的字段名。非数组、或元素没声明字段时返回空串，
// 调用方按普通键渲染。
func ptcItemKeys(raw json.RawMessage) string {
	var field struct {
		Type  string `json:"type"`
		Items struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &field) != nil || field.Type != "array" {
		return ""
	}
	if len(field.Items.Properties) == 0 {
		return ""
	}
	names := make([]string, 0, len(field.Items.Properties))
	for name := range field.Items.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " ")
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
	return "other"
}

func ptcGroupTitle(locale *mcpLocaleBundle, tool MCPToolInfo) string {
	if group := ptcGroupOf(tool); group != "other" {
		return group
	}
	return locale.PTCResource("ptc_group_other.txt")
}

func renderPTCDigest(tools []MCPToolInfo) string {
	return renderPTCDigestForLocale(loadMCPLocaleBundle(defaultMCPLocale), tools, true)
}

// renderPTCDigestForLocale 渲染 run_code 的工具描述。
//
// withSignatures 决定要不要带那份函数签名清单，它占整份 digest 的 55%：
//
//   - 独立的 PTC 端点上工具面只有 run_code / run_shell，模型没有别处可看，必须带；
//   - run_code 与业务工具并列在同一个工具面上时不带——那些工具的完整 schema 就在
//     工具面里，再渲染一遍 Python 签名等于同一批工具描述两次。
//
// 不带签名时改用另一份前言：它不列函数，只说清「工具面上的工具已在作用域内」以及
// 两处与 schema 不符的地方（bkn_context 由运行时注入、response_format 固定 json）。
// 那两条靠「参数与 schema 一致」打发不掉——schema 里 bkn_context 是必填。
func renderPTCDigestForLocale(locale *mcpLocaleBundle, tools []MCPToolInfo, withSignatures bool) string {
	var b strings.Builder
	if !withSignatures {
		b.WriteString(locale.PTCResource("ptc_digest_prefix_inline.txt"))
		b.WriteString(locale.PTCResource("ptc_digest_suffix.txt"))
		return b.String()
	}

	b.WriteString(locale.PTCResource("ptc_digest_prefix.txt"))
	group := ""
	for _, tool := range tools {
		if next := ptcGroupOf(tool); next != group {
			if group != "" {
				b.WriteString("```\n")
			}
			fmt.Fprintf(&b, "\n### %s\n\n```python\n", ptcGroupTitle(locale, tool))
			group = next
		}
		fmt.Fprintf(&b, "%s -> %s\n", ptcSignature(tool), ptcReturnKeys(tool))
		if tool.Title != "" {
			fmt.Fprintf(&b, "    # %s\n", tool.Title)
		}
		for _, hint := range locale.PTCHints(tool.Name) {
			fmt.Fprintf(&b, "    #   %s\n", hint)
		}
	}
	if group != "" {
		b.WriteString("```\n")
	}
	b.WriteString(locale.PTCResource("ptc_digest_suffix.txt"))
	return b.String()
}

func renderPTCStub(tools []MCPToolInfo) string {
	var b strings.Builder
	b.WriteString(mustReadMCPStaticResource("ptc_stub.py"))
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
	return BuildPTCToolkitForLocale(endpoint, port, mcpLocaleFromEnv())
}

// BuildPTCToolkitForLocale renders the PTC toolkit from the effective locale.
func BuildPTCToolkitForLocale(endpoint string, port int, locale string) (*PTCToolkit, error) {
	info, err := BuildMCPInfoForLocale(endpoint, locale)
	if err != nil {
		return nil, err
	}
	return buildPTCToolkitFromLocale(ptcUsableTools(info), port, loadMCPLocaleBundle(locale))
}

// InlinePTCTools 返回可以并进业务工具面的 PTC 工具（run_code / run_shell）。
//
// 与 BuildPTCToolkit 的差别只在 run_code 的描述：那份签名清单被省掉了，因为并列时
// 那些工具的完整 schema 就在同一个工具面上，再渲染一遍 Python 签名是重复。
// 实测两种渲染 8852 vs 约 3800 字符。
//
// 其余部分（stub、沙箱回访地址、组装方式）完全一致——两条路最终拼出的脚本相同。
func InlinePTCTools(port int, locale string) ([]PTCTool, error) {
	info, err := BuildMCPInfoForLocale("", locale)
	if err != nil {
		return nil, err
	}
	bundle := loadMCPLocaleBundle(locale)
	kit, err := buildPTCToolkitFromLocale(ptcUsableTools(info), port, bundle)
	if err != nil {
		return nil, err
	}
	inline := make([]PTCTool, 0, len(kit.Tools))
	for _, tool := range kit.Tools {
		if tool.Name == toolKeyRunCode {
			tool.Description = renderPTCDigestForLocale(bundle, nil, false)
		}
		inline = append(inline, tool)
	}
	return inline, nil
}

// buildPTCToolkitFrom 从已筛好的工具目录渲染工具包。与 BuildPTCToolkit 分开，
// 是为了让测试不必起一个真实端点就能覆盖工具表与版本号。
func buildPTCToolkitFrom(tools []MCPToolInfo, port int) (*PTCToolkit, error) {
	return buildPTCToolkitFromLocale(tools, port, loadMCPLocaleBundle(defaultMCPLocale))
}

func buildPTCToolkitFromLocale(tools []MCPToolInfo, port int, locale *mcpLocaleBundle) (*PTCToolkit, error) {
	digest := renderPTCDigestForLocale(locale, tools, true)
	stub := renderPTCStub(tools)
	exposed := []PTCTool{
		{
			Name:        toolKeyRunCode,
			Description: digest,
			InputSchema: json.RawMessage(locale.PTCResource("ptc_run_code_schema.json")),
			Language:    "python",
			Wrap:        ptcWrapHandler,
		},
		{
			Name:        toolKeyRunShell,
			Description: locale.PTCResource("ptc_run_shell_description.txt"),
			InputSchema: json.RawMessage(locale.PTCResource("ptc_run_shell_schema.json")),
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
