// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

// MCPToolInfo 单个工具的对外说明（名称 / 展示元数据 / 描述 / 输入输出 schema）。
//
// title / group / group_title / order 与 tools/list 同源：那边 title 落在协议
// 自己的字段上、其余三个落在工具的 `_meta`，这边一律是平铺字段。两处必须给出
// 同一份答案——这个端点存在的理由就是「不握手也能看清能力面」。
type MCPToolInfo struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Group        string          `json:"group,omitempty"`
	GroupTitle   string          `json:"group_title,omitempty"`
	Order        int             `json:"order,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// MCPInfo MCP 服务自描述文档：端点、协议、鉴权、工具目录、客户端配置示例。
// 供 Agent / 人通过 GET 一次性了解如何集成，无需先走 MCP 握手。
type MCPInfo struct {
	Service             string          `json:"service"`
	Endpoint            string          `json:"endpoint"`
	Protocol            string          `json:"protocol"`
	Transport           string          `json:"transport"`
	Auth                string          `json:"auth"`
	ToolCount           int             `json:"tool_count"`
	Tools               []MCPToolInfo   `json:"tools"`
	ClientConfigExample json.RawMessage `json:"client_config_example"`
}

// tryLoadToolSchemas 与 loadToolSchemas 同源，但读不到/解析失败时返回 nil 而非 panic，
// 供 info 端点容错使用。取到之后按 locale 叠一层，与 tools/list 同一份答案。
func tryLoadToolSchemas(locale *mcpLocaleBundle, toolKey string) (input, output json.RawMessage) {
	if input, output, ok := lifecycleToolSchemas(toolKey); ok {
		return locale.OverlaySchemas(toolKey, input, output)
	}
	data, err := schemasFS.ReadFile(fmt.Sprintf("schemas/%s.json", toolKey))
	if err != nil {
		return nil, nil
	}
	var wrapper toolSchemaFile
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, nil
	}
	if isBusinessTool(toolKey) {
		wrapper.InputSchema = offerBKNContext(wrapper.InputSchema)
	}
	return locale.OverlaySchemas(toolKey, wrapper.InputSchema, wrapper.OutputSchema)
}

// BuildMCPInfo builds the MCP self-description with the process default locale.
// New request-facing callers should use BuildMCPInfoForLocale.
func BuildMCPInfo(endpoint string) (*MCPInfo, error) {
	return BuildMCPInfoForLocale(endpoint, mcpLocaleFromEnv())
}

// BuildMCPInfoForLocale builds the MCP self-description using the requested
// effective locale. endpoint is the public MCP Streamable HTTP address.
func BuildMCPInfoForLocale(endpoint, localeName string) (*MCPInfo, error) {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		return nil, fmt.Errorf("read tools_meta.json: %w", err)
	}
	var meta map[string]ToolMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse tools_meta.json: %w", err)
	}
	// Resolve localized text from the same resource bundle as tools/list.

	// 与 tools/list 用同一套按当前档位的判定：装饰过的工具带上付费参数、
	// 未授权的企业工具不出现。两处若不一致，这个端点就比不存在更糟——它的
	// 用途正是让人不握手就看清能力面。
	type entry struct {
		key  string
		info MCPToolInfo
	}
	locale := loadMCPLocaleBundle(localeName)
	all := make([]entry, 0, len(meta))
	for key := range meta {
		// 未装配的工具不能出现在这里：这个端点的用途是「不握手就看清能力面」，
		// 广播一条 tools/call 会答「无此工具」的条目比不广播更糟。
		if key == toolKeyExecuteSkill && !knskills.ExecuteEnabled() {
			continue
		}
		m := locale.ToolMeta(key)
		in, out := tryLoadToolSchemas(locale, key)
		if d, ok := mcptool.DecoratorFor(key); ok && d.Allowed() {
			in = d.Patch(in)
		}
		all = append(all, entry{key, MCPToolInfo{
			Name:         m.Name,
			Title:        m.Title,
			Group:        m.Group,
			GroupTitle:   m.GroupTitle,
			Order:        m.Order,
			Description:  m.Description,
			InputSchema:  in,
			OutputSchema: out,
		}})
	}
	// 社区二进制这里是空循环。
	for _, t := range mcptool.Extras() {
		if !t.Allowed() {
			continue
		}
		all = append(all, entry{t.Key, MCPToolInfo{
			Name:        t.Name,
			Title:       t.Title,
			Group:       t.Group,
			GroupTitle:  t.GroupTitle,
			Order:       t.Order,
			Description: t.Desc,
			// 与 tools/list 同样施加（assemble.go 的 addExtras）：企业工具按本服务
			// 自己的定义就是业务工具，生命周期守卫会向它要 bkn_context。这个端点
			// 存在的理由是「不握手就看清能力面」，广播一份调不通的 schema 比不广播
			// 更糟——照它集成会直接拿到 conversation_required。
			InputSchema:  offerBKNContext(t.Input),
			OutputSchema: t.Output,
		}})
	}
	// 按工具 key 排，不是按对外 name 排。目前 tools_meta.json 与 locales/en-US
	// 里 name 恒等于 key，两种排法结果一样——但那是巧合，locale 里改个 name
	// 社区侧的顺序就跟着变了。
	// 稳定排序：装配期已保证 key 互不重复(toolBuilder.claimName)，稳定排序是
	// 第二道保险——真出现重复时顺序至少不会在两次进程之间抖动。
	sort.SliceStable(all, func(i, j int) bool { return all[i].key < all[j].key })

	tools := make([]MCPToolInfo, 0, len(all))
	for _, e := range all {
		tools = append(tools, e.info)
	}

	cfg, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			serverName: map[string]any{
				"url": endpoint,
				"headers": map[string]string{
					"Authorization": "Bearer <access-token>",
				},
			},
		},
	})

	return &MCPInfo{
		Service:             serverName,
		Endpoint:            endpoint,
		Protocol:            "MCP / JSON-RPC 2.0 (initialize → tools/list → tools/call)",
		Transport:           "Streamable HTTP",
		Auth:                "Bearer credential via Authorization header — an OAuth access token, or a long-lived user-issued AppKey (prefix bak_). No other headers required.",
		ToolCount:           len(tools),
		Tools:               tools,
		ClientConfigExample: cfg,
	}, nil
}
