// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knrunsql

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

var (
	// ErrSQLRequired sql 入参为空。
	ErrSQLRequired = errors.New("sql is required")
	// ErrNoResourcePlaceholder SQL 未通过 {{.resource_id}} 占位符引用任何数据资源。
	ErrNoResourcePlaceholder = errors.New("sql must reference at least one data resource via the {{.resource_id}} placeholder")
)

// RunSQLReq run_sql 入参（MCP 工具与内部 REST 端点共用）。
type RunSQLReq struct {
	SQL          string `json:"sql"`           // Trino 方言 SQL，表名用 {{.resource_id}} 占位
	ResourceType string `json:"resource_type"` // 连接器类型，留空则按 resource_id 自动解析
	QueryTimeout int    `json:"query_timeout"` // 查询超时（秒），可选
}

// KnRunSQLService 对知识网络挂载的数据资源执行只读 SQL（强制 SELECT-only）。
type KnRunSQLService interface {
	RunSQL(ctx context.Context, req *RunSQLReq) (*interfaces.VegaRawQueryResp, error)
}

type knRunSQLService struct {
	vega interfaces.DrivenVega
}

var (
	once     sync.Once
	instance KnRunSQLService
)

// NewKnRunSQLService 创建 KnRunSQLService 单例。
func NewKnRunSQLService() KnRunSQLService {
	once.Do(func() {
		instance = &knRunSQLService{
			vega: drivenadapters.NewVegaAccess(),
		}
	})
	return instance
}

// NewKnRunSQLServiceWith 注入依赖创建（测试用）。
func NewKnRunSQLServiceWith(vega interfaces.DrivenVega) KnRunSQLService {
	return &knRunSQLService{vega: vega}
}

// RunSQL 守卫 → 提取 resource_id → 解析连接器类型 → 调 vega 原始查询。
func (s *knRunSQLService) RunSQL(ctx context.Context, req *RunSQLReq) (*interfaces.VegaRawQueryResp, error) {
	if req == nil || strings.TrimSpace(req.SQL) == "" {
		return nil, ErrSQLRequired
	}

	// 只读守卫：拒绝写入 / DDL / 多语句。
	if err := EnsureReadOnlySQL(req.SQL); err != nil {
		return nil, err
	}

	// 必须通过 {{.resource_id}} 占位符引用资源，否则 vega 无法定位数据源。
	resourceIDs := ExtractResourceIDs(req.SQL)
	if len(resourceIDs) == 0 {
		return nil, ErrNoResourcePlaceholder
	}

	// resource_type 未显式给出时，按第一个 resource_id 自动解析其连接器类型。
	resourceType := strings.TrimSpace(req.ResourceType)
	if resourceType == "" {
		rt, err := s.vega.GetResourceConnectorType(ctx, resourceIDs[0])
		if err != nil {
			return nil, err
		}
		resourceType = rt
	}

	return s.vega.RawQuery(ctx, &interfaces.VegaRawQueryReq{
		Query:        req.SQL,
		ResourceType: resourceType,
		QueryType:    "standard",
		QueryTimeout: req.QueryTimeout,
	})
}
