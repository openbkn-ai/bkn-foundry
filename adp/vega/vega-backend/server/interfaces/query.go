// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"fmt"
)

const (
	QueryType_Standard = "standard" // 标准查询
	QueryType_Stream   = "stream"   // 流式查询
)

// RawQueryRequest SQL查询请求
type RawQueryRequest struct {
	Query           any           `json:"query,omitempty"`
	QueryFormat     QueryFormat   `json:"query_format,omitempty"`
	InputDialect    string        `json:"input_dialect,omitempty"`
	Paging          PagingRequest `json:"paging,omitempty"`
	QueryTimeoutSec int           `json:"query_timeout_sec,omitempty"` // 查询超时时间（秒），默认60，最小1，最大3600

}

func (r RawQueryRequest) Contract() RawQueryContract {
	return RawQueryContract{
		Query:        r.Query,
		QueryFormat:  r.QueryFormat,
		InputDialect: r.InputDialect,
		Paging:       r.Paging,
	}
}

func (r RawQueryRequest) IsContinuation() bool {
	return r.Contract().IsContinuation()
}

func (r RawQueryRequest) EffectiveInputDialect() string {
	return r.Contract().EffectiveInputDialect()
}

func (r RawQueryRequest) ValidateContract() error {
	if r.IsContinuation() && r.QueryTimeoutSec != 0 {
		return fmt.Errorf("query_timeout_sec is only allowed for an initial request")
	}
	return r.Contract().Validate()
}

func (r *RawQueryRequest) NormalizePaging() {
	if !r.IsContinuation() {
		r.Paging = r.Paging.Normalized()
	}
}

// RawQueryResponse SQL查询响应
type RawQueryResponse struct {
	Columns     []ColumnInfo     `json:"columns"`            // 列信息
	Entries     []map[string]any `json:"entries"`            // 查询结果
	TotalCount  int64            `json:"total_count"`        // 总条数
	Warnings    []string         `json:"warnings,omitempty"` // 非阻断告警（如 deprecated 资源命中提示）
	Paging      *PagingResponse  `json:"paging,omitempty"`
	SearchAfter []any            `json:"-"` // OpenSearch internal cursor state
}

// ColumnInfo 列信息
type ColumnInfo struct {
	Name string `json:"name"` // 列名
	Type string `json:"type"` // 列类型
}

//go:generate mockgen -source ../interfaces/query.go -destination ../interfaces/mock/mock_query.go

// RawQueryService SQL查询服务接口
type RawQueryService interface {
	// Execute 执行SQL查询
	Execute(ctx context.Context, req *RawQueryRequest) (*RawQueryResponse, error)
}
