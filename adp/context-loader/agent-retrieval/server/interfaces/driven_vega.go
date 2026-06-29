// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// VegaRawQueryReq vega 原始 SQL 查询请求（只读）。
// Query 为 Trino 方言 SQL，表名用 {{.resource_id}} 占位符引用，由 vega 解析成真实表名。
type VegaRawQueryReq struct {
	Query        string `json:"query"`                   // Trino 方言 SQL
	ResourceType string `json:"resource_type"`           // 连接器类型：mysql / mariadb / postgresql
	QueryType    string `json:"query_type,omitempty"`    // standard / stream，默认 standard
	QueryTimeout int    `json:"query_timeout,omitempty"` // 查询超时（秒），1-3600
}

// VegaColumn vega 查询返回的列信息。
type VegaColumn struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// VegaRawQueryResp vega 原始查询响应。
type VegaRawQueryResp struct {
	Columns    []VegaColumn     `json:"columns"`
	Entries    []map[string]any `json:"entries"`
	Stats      map[string]any   `json:"stats,omitempty"`
	TotalCount int64            `json:"total_count"`
	Warnings   []string         `json:"warnings,omitempty"`
}

// VegaListResourcesReq vega 资源列表查询入参（数据层直查，脱离本体）。
// 空字段不参与过滤；Offset/Limit 为 0 时由 vega 取默认值（offset=0, limit=20）。
type VegaListResourcesReq struct {
	CatalogID string // 限定某 catalog
	Category  string // 资源类别：table / file / fileset / api / metric / topic / index / logicview / dataset
	Offset    int
	Limit     int
}

// VegaResourceColumn vega 资源的物理列（取自 schema_definition）。
type VegaResourceColumn struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

// VegaResource vega 数据资源（list/get 共用，list 时 SchemaDefinition 一般为空）。
type VegaResource struct {
	ID               string               `json:"id"`
	CatalogID        string               `json:"catalog_id"`
	Name             string               `json:"name"`
	Category         string               `json:"category"`
	Status           string               `json:"status"`
	SchemaDefinition []VegaResourceColumn `json:"schema_definition"`
}

// VegaListResourcesResp vega 资源列表响应（entries 信封 + 总数）。
type VegaListResourcesResp struct {
	Entries    []VegaResource `json:"entries"`
	TotalCount int64          `json:"total_count"`
}

// DrivenVega vega 数据目录后端访问接口（只读查询）。
type DrivenVega interface {
	// RawQuery 执行只读 SQL。调用方（MCP 工具层）须自行保证 SELECT-only，本接口不做语句校验。
	RawQuery(ctx context.Context, req *VegaRawQueryReq) (*VegaRawQueryResp, error)
	// GetResourceConnectorType 按 resource_id 解析其所属 catalog 的连接器类型，
	// 用于自动填充 RawQueryReq.ResourceType。
	GetResourceConnectorType(ctx context.Context, resourceID string) (string, error)
	// ListResources 列出可查询的数据资源（按账户 view_detail 授权过滤，由 vega 强制）。
	ListResources(ctx context.Context, req *VegaListResourcesReq) (*VegaListResourcesResp, error)
	// GetResource 取单个资源（含物理列 schema_definition）；资源不存在或无权时返回错误。
	GetResource(ctx context.Context, resourceID string) (*VegaResource, error)
}
