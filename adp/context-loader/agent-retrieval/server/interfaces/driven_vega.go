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

// DrivenVega vega 数据目录后端访问接口（只读查询）。
type DrivenVega interface {
	// RawQuery 执行只读 SQL。调用方（MCP 工具层）须自行保证 SELECT-only，本接口不做语句校验。
	RawQuery(ctx context.Context, req *VegaRawQueryReq) (*VegaRawQueryResp, error)
	// GetResourceConnectorType 按 resource_id 解析其所属 catalog 的连接器类型，
	// 用于自动填充 RawQueryReq.ResourceType。
	GetResourceConnectorType(ctx context.Context, resourceID string) (string, error)
}
