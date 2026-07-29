// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

// MCPToolInfo 单个工具的对外说明（名称 / 描述 / 输入输出 schema）。
type MCPToolInfo struct {
	Name         string          `json:"name"`
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
// 供 info 端点容错使用。
func tryLoadToolSchemas(toolKey string) (input, output json.RawMessage) {
	data, err := schemasFS.ReadFile(fmt.Sprintf("schemas/%s.json", toolKey))
	if err != nil {
		return nil, nil
	}
	var wrapper toolSchemaFile
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, nil
	}
	return wrapper.InputSchema, wrapper.OutputSchema
}

// BuildMCPInfo 基于内嵌的 tools_meta.json + schemas/*.json 组装 MCP 自描述文档。
// endpoint 为本服务对外的 MCP Streamable HTTP 地址。
//
// 企业插座的两种改动都要在这里体现：装饰过的工具要带上新增的入参，企业独占
// 工具要出现在目录里。否则会出现「/mcp/info 说没有、tools/list 里有」的分叉，
// 而 /mcp/info 的用途正是让人不握手就看清能力面。
func BuildMCPInfo(endpoint string) (*MCPInfo, error) {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		return nil, fmt.Errorf("read tools_meta.json: %w", err)
	}
	var meta map[string]ToolMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse tools_meta.json: %w", err)
	}

	extras := mcptool.Extras()

	// 按工具 key 排序，而不是按对外的 name 排。目前 tools_meta.json 与
	// locales/en-US 里 name 恒等于 key，两种排法结果一样——但那是巧合，
	// locale 里改个 name，社区侧的顺序就跟着变了。
	type entry struct {
		key  string
		info MCPToolInfo
	}
	all := make([]entry, 0, len(meta)+len(extras))
	for key, m := range meta {
		in, out := tryLoadToolSchemas(key)
		if d, ok := mcptool.DecoratorFor(key); ok {
			in = d.Patch(in)
		}
		all = append(all, entry{key, MCPToolInfo{
			Name:         m.Name,
			Description:  m.Description,
			InputSchema:  in,
			OutputSchema: out,
		}})
	}
	// 社区二进制这里是空循环。
	for _, t := range extras {
		all = append(all, entry{t.Key, MCPToolInfo{
			Name:         t.Name,
			Description:  t.Desc,
			InputSchema:  t.Input,
			OutputSchema: t.Output,
		}})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].key < all[j].key })

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
