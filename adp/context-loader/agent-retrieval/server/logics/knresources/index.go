// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knresources 提供数据层「资源直查」能力（脱离本体）：list_resources / describe_resource。
// 与 search_schema（本体/语义入口）互补，二者都喂给 run_sql。
// 授权由下游 vega 在其 /in resource 端点按账户 view_detail 强制（空账户 fail-closed）。
package knresources

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

// ErrResourceIDRequired describe_resource 的 resource_id 入参为空。
var ErrResourceIDRequired = errors.New("resource_id is required")

// ListResourcesReq list_resources 入参（MCP 工具与内部 REST 端点共用）。
type ListResourcesReq struct {
	CatalogID string `json:"catalog_id"` // 可选，限定某 catalog
	Type      string `json:"type"`       // 可选，资源类别（table / file / ...），映射 vega category
	Offset    int    `json:"offset"`     // 可选，分页偏移
	Limit     int    `json:"limit"`      // 可选，分页大小
}

// ResourceLite list_resources 的精简资源条目。
type ResourceLite struct {
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Type       string `json:"type"` // 资源类别（取自 vega category）
	Status     string `json:"status"`
	CatalogID  string `json:"catalog_id"`
}

// ListResourcesResp list_resources 响应。
type ListResourcesResp struct {
	Entries    []ResourceLite `json:"entries"`
	TotalCount int64          `json:"total_count"`
}

// ColumnLite describe_resource 的物理列（写 SQL 用）。
type ColumnLite struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// DescribeResourceResp describe_resource 响应。
type DescribeResourceResp struct {
	ResourceID    string       `json:"resource_id"`
	ConnectorType string       `json:"connector_type"`
	Columns       []ColumnLite `json:"columns"`
}

// KnResourcesService 数据层资源直查（list / describe），薄包装 vega resource 端点。
type KnResourcesService interface {
	ListResources(ctx context.Context, req *ListResourcesReq) (*ListResourcesResp, error)
	DescribeResource(ctx context.Context, resourceID string) (*DescribeResourceResp, error)
}

type knResourcesService struct {
	vega interfaces.DrivenVega
}

var (
	once     sync.Once
	instance KnResourcesService
)

// NewKnResourcesService 创建 KnResourcesService 单例。
func NewKnResourcesService() KnResourcesService {
	once.Do(func() {
		instance = &knResourcesService{
			vega: drivenadapters.NewVegaAccess(),
		}
	})
	return instance
}

// NewKnResourcesServiceWith 注入依赖创建（测试用）。
func NewKnResourcesServiceWith(vega interfaces.DrivenVega) KnResourcesService {
	return &knResourcesService{vega: vega}
}

// ListResources 列出可查询的数据资源（输出精简字段；type 即 vega category）。
func (s *knResourcesService) ListResources(ctx context.Context, req *ListResourcesReq) (*ListResourcesResp, error) {
	if req == nil {
		req = &ListResourcesReq{}
	}
	vegaResp, err := s.vega.ListResources(ctx, &interfaces.VegaListResourcesReq{
		CatalogID: strings.TrimSpace(req.CatalogID),
		Category:  strings.TrimSpace(req.Type),
		Offset:    req.Offset,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}

	out := &ListResourcesResp{
		Entries:    make([]ResourceLite, 0, len(vegaResp.Entries)),
		TotalCount: vegaResp.TotalCount,
	}
	for _, r := range vegaResp.Entries {
		out.Entries = append(out.Entries, ResourceLite{
			ResourceID: r.ID,
			Name:       r.Name,
			Type:       r.Category,
			Status:     r.Status,
			CatalogID:  r.CatalogID,
		})
	}
	return out, nil
}

// DescribeResource 取单个资源物理 schema + 连接器类型（写 run_sql 用）。
func (s *knResourcesService) DescribeResource(ctx context.Context, resourceID string) (*DescribeResourceResp, error) {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return nil, ErrResourceIDRequired
	}

	res, err := s.vega.GetResource(ctx, resourceID)
	if err != nil {
		return nil, err
	}

	connectorType, err := s.vega.GetResourceConnectorType(ctx, resourceID)
	if err != nil {
		return nil, err
	}

	columns := make([]ColumnLite, 0, len(res.SchemaDefinition))
	for _, c := range res.SchemaDefinition {
		columns = append(columns, ColumnLite{
			Name:        c.Name,
			Type:        c.Type,
			Description: c.Description,
		})
	}

	return &DescribeResourceResp{
		ResourceID:    res.ID,
		ConnectorType: connectorType,
		Columns:       columns,
	}, nil
}
