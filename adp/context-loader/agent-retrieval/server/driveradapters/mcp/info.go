// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
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
func BuildMCPInfo(endpoint string) (*MCPInfo, error) {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		return nil, fmt.Errorf("read tools_meta.json: %w", err)
	}
	var meta map[string]ToolMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse tools_meta.json: %w", err)
	}

	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tools := make([]MCPToolInfo, 0, len(keys))
	for _, key := range keys {
		m := meta[key]
		in, out := tryLoadToolSchemas(key)
		tools = append(tools, MCPToolInfo{
			Name:         m.Name,
			Description:  m.Description,
			InputSchema:  in,
			OutputSchema: out,
		})
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
