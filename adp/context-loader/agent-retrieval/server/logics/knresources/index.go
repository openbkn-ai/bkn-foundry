// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knresources 提供数据层「资源直查」能力（脱离本体）：list_resources / describe_resource。
// 与 search_schema（本体/语义入口）互补，二者都喂给 run_sql。
// 授权由下游 vega 在其 /in resource 端点按账户 view_detail 强制（空账户 fail-closed）。
package knresources

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// ErrResourceIDRequired describe_resource 的 resource_id 入参为空。
var ErrResourceIDRequired = errors.New("resource_id is required")

// ErrKnBackendUnavailable 按 kn_id 查询需要本体侧依赖，但它没有注入。
var ErrKnBackendUnavailable = errors.New("knowledge network backend is not configured")

const (
	// dataSourceTypeResource 对象类绑定里唯一能直接映射到 vega resource 的形态。
	dataSourceTypeResource = "resource"
	// knResourceFetchConcurrency 按 id 取资源的并发上限。绑定是几十张表的量级，
	// 串行会到秒级；再高的并发对 vega 只是无谓压力。
	knResourceFetchConcurrency = 8
)

// ListResourcesReq list_resources 入参（MCP 工具与内部 REST 端点共用）。
type ListResourcesReq struct {
	KnID      string `json:"kn_id"`      // 可选，限定某知识网络已绑定的资源；在场时忽略 catalog_id/offset/limit
	CatalogID string `json:"catalog_id"` // 可选，限定某 catalog
	Type      string `json:"type"`       // 可选，资源类别（table / file / ...），映射 vega category
	Offset    int    `json:"offset"`     // 可选，分页偏移
	Limit     int    `json:"limit"`      // 可选，分页大小
}

// UnresolvedBinding 一条没能解析成资源的对象类绑定。三种成因分开上报，
// 因为调用方要做的事完全不同：去建模 / 去重绑 / 去要权限。
type UnresolvedBinding struct {
	ObjectTypeID string `json:"object_type_id"`
	ResourceID   string `json:"resource_id,omitempty"`
	SourceType   string `json:"source_type,omitempty"` // stale_binding：绑定声明的 data_source.type
	Reason       string `json:"reason,omitempty"`      // missing：下游返回的原因
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
// Unbound / StaleBinding / Missing 仅在按 kn_id 查询时可能非空。
type ListResourcesResp struct {
	Entries    []ResourceLite `json:"entries"`
	TotalCount int64          `json:"total_count"`
	// Unbound 对象类压根没绑数据源（data_source 缺失或 id 为空）。
	Unbound []UnresolvedBinding `json:"unbound,omitempty"`
	// StaleBinding 绑的是已废弃的数据源形态（如 data_view），不是 vega resource。
	StaleBinding []UnresolvedBinding `json:"stale_binding,omitempty"`
	// Missing 绑定的 resource_id 取不回来：资源已删，或调用账户无权。
	Missing []UnresolvedBinding `json:"missing,omitempty"`
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
	bkn  interfaces.BknBackendAccess
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
			bkn:  drivenadapters.NewBknBackendAccess(),
		}
	})
	return instance
}

// NewKnResourcesServiceWith 注入依赖创建（测试用）。
func NewKnResourcesServiceWith(vega interfaces.DrivenVega, bkn interfaces.BknBackendAccess) KnResourcesService {
	return &knResourcesService{vega: vega, bkn: bkn}
}

// ListResources 列出可查询的数据资源（输出精简字段；type 即 vega category）。
// 带 kn_id 时走本体绑定按 id 直取，不带时是账户级资源池分页。
func (s *knResourcesService) ListResources(ctx context.Context, req *ListResourcesReq) (*ListResourcesResp, error) {
	if req == nil {
		req = &ListResourcesReq{}
	}
	if knID := strings.TrimSpace(req.KnID); knID != "" {
		return s.listByKnowledgeNetwork(ctx, knID, strings.TrimSpace(req.Type))
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

// listByKnowledgeNetwork 列出某知识网络已绑定的数据资源。
//
// 走「本体拿绑定 -> 按 id 逐个取资源」，刻意不碰 vega 的列表端点：那是账户级
// 资源池的分页，绑定的表在大池子里按 update_time 排到几千名开外，取任何一页再
// 求交集都会漏（#781）。按 id 直取与池子大小无关。
//
// 绑定天然是几十张表的量级，因此一次全返、不分页；分页只会把同一个坑重挖一遍。
func (s *knResourcesService) listByKnowledgeNetwork(ctx context.Context, knID, typeFilter string) (*ListResourcesResp, error) {
	if s.bkn == nil {
		return nil, ErrKnBackendUnavailable
	}
	detail, err := s.bkn.GetKnowledgeNetworkDetail(ctx, knID)
	if err != nil {
		return nil, err
	}

	out := &ListResourcesResp{Entries: make([]ResourceLite, 0)}
	if detail == nil {
		return out, nil
	}

	// 绑定分流：能取的排成 targets（按对象类顺序去重，输出稳定），取不了的
	// 按成因分到三个字段里。
	type target struct {
		objectTypeID string
		resourceID   string
	}
	targets := make([]target, 0, len(detail.ObjectTypes))
	seen := make(map[string]struct{}, len(detail.ObjectTypes))
	for _, ot := range detail.ObjectTypes {
		if ot == nil {
			continue
		}
		ds := ot.DataSource
		if ds == nil || strings.TrimSpace(ds.ID) == "" {
			out.Unbound = append(out.Unbound, UnresolvedBinding{ObjectTypeID: ot.ID})
			continue
		}
		resourceID := strings.TrimSpace(ds.ID)
		// 空 type 按 resource 处理（老数据没写全）；其余非 resource 的形态（如已
		// 废弃的 data_view）不能拿去调 vega 的 resource 端点，那条路必然 500。
		if sourceType := strings.TrimSpace(ds.Type); sourceType != "" &&
			!strings.EqualFold(sourceType, dataSourceTypeResource) {
			out.StaleBinding = append(out.StaleBinding, UnresolvedBinding{
				ObjectTypeID: ot.ID,
				ResourceID:   resourceID,
				SourceType:   sourceType,
			})
			continue
		}
		if _, dup := seen[resourceID]; dup {
			continue // 多个对象类共用一张表是常态，资源只返一次
		}
		seen[resourceID] = struct{}{}
		targets = append(targets, target{objectTypeID: ot.ID, resourceID: resourceID})
	}

	// 限并发取回。单条失败只落进 missing，不能让整次调用失败——一个悬空绑定
	// 不该把其余几十张表一起拖垮。
	type fetched struct {
		resource *interfaces.VegaResource
		err      error
	}
	results := make([]fetched, len(targets))
	slots := make(chan struct{}, knResourceFetchConcurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, resourceID string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			res, err := s.vega.GetResource(ctx, resourceID)
			results[i] = fetched{resource: res, err: err}
		}(i, t.resourceID)
	}
	wg.Wait()

	for i, t := range targets {
		r := results[i]
		if r.err != nil || r.resource == nil {
			out.Missing = append(out.Missing, UnresolvedBinding{
				ObjectTypeID: t.objectTypeID,
				ResourceID:   t.resourceID,
				Reason:       unresolvedReason(r.err),
			})
			continue
		}
		if typeFilter != "" && !strings.EqualFold(r.resource.Category, typeFilter) {
			continue
		}
		resourceID := r.resource.ID
		if resourceID == "" {
			resourceID = t.resourceID
		}
		out.Entries = append(out.Entries, ResourceLite{
			ResourceID: resourceID,
			Name:       r.resource.Name,
			Type:       r.resource.Category,
			Status:     r.resource.Status,
			CatalogID:  r.resource.CatalogID,
		})
	}
	out.TotalCount = int64(len(out.Entries))
	return out, nil
}

// unresolvedReason 把下游错误压成一行放进 missing.reason；无错时说明资源为空。
func unresolvedReason(err error) string {
	if err == nil {
		return "resource not found"
	}
	return err.Error()
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
