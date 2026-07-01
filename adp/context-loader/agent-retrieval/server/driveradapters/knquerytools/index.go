// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knquerytools provides HTTP handlers for the query tools that are also
// exposed as MCP tools: run_sql, list_knowledge_networks, get_kn_detail,
// list_resources, describe_resource.
// These internal REST endpoints back the operator-integration toolbox entries.
package knquerytools

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knrunsql"
)

// KnQueryToolsHandler 处理 run_sql / list_knowledge_networks / get_kn_detail /
// get_object_types / get_relation_types / list_resources / describe_resource 的内部 REST 入口。
type KnQueryToolsHandler interface {
	RunSQL(c *gin.Context)
	ListKnowledgeNetworks(c *gin.Context)
	GetKnDetail(c *gin.Context)
	GetObjectTypes(c *gin.Context)
	GetRelationTypes(c *gin.Context)
	ListResources(c *gin.Context)
	DescribeResource(c *gin.Context)
}

type knQueryToolsHandler struct {
	logger     interfaces.Logger
	runSQL     knrunsql.KnRunSQLService
	resources  knresources.KnResourcesService
	bknBackend interfaces.BknBackendAccess
}

var (
	once    sync.Once
	handler KnQueryToolsHandler
)

// NewKnQueryToolsHandler 创建 KnQueryToolsHandler 单例。
func NewKnQueryToolsHandler() KnQueryToolsHandler {
	once.Do(func() {
		conf := config.NewConfigLoader()
		handler = &knQueryToolsHandler{
			logger:     conf.GetLogger(),
			runSQL:     knrunsql.NewKnRunSQLService(),
			resources:  knresources.NewKnResourcesService(),
			bknBackend: drivenadapters.NewBknBackendAccess(),
		}
	})
	return handler
}

// RunSQL 对知识网络挂载的数据资源执行只读 SQL（强制 SELECT-only）。
func (h *knQueryToolsHandler) RunSQL(c *gin.Context) {
	ctx := c.Request.Context()
	req := &knrunsql.RunSQLReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}

	resp, err := h.runSQL.RunSQL(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#RunSQL] run sql failed: %v", err)
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ListKnowledgeNetworks 列出知识网络（发现 kn_id）。
func (h *knQueryToolsHandler) ListKnowledgeNetworks(c *gin.Context) {
	ctx := c.Request.Context()
	req := &interfaces.ListKnReq{}
	// body 可选；忽略空 body 的绑定错误。
	_ = c.ShouldBindJSON(req)
	if req.Limit == 0 {
		req.Limit = 20
	}

	resp, err := h.bknBackend.ListKnowledgeNetworks(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#ListKnowledgeNetworks] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// getKnDetailReq get_kn_detail 入参。
type getKnDetailReq struct {
	KnID        string `json:"kn_id" form:"kn_id"`
	DetailLevel string `json:"detail_level" form:"detail_level"` // summary（默认）| full
}

// GetKnDetail 获取知识网络详情（概念组 / 对象类 / 关系类 / 行动类）。
// detail_level=summary（默认）返回骨架 + 属性名，full 返回全量。
func (h *knQueryToolsHandler) GetKnDetail(c *gin.Context) {
	ctx := c.Request.Context()
	req := &getKnDetailReq{}
	_ = c.ShouldBindQuery(req)
	_ = c.ShouldBindJSON(req)
	if req.KnID == "" {
		req.KnID = c.GetHeader("X-Kn-ID")
	}
	if req.KnID == "" {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "kn_id is required"))
		return
	}

	resp, err := h.bknBackend.GetKnowledgeNetworkDetail(ctx, req.KnID)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetKnDetail] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	detailLevel := req.DetailLevel
	if detailLevel == "" {
		detailLevel = interfaces.DetailLevelSummary
	}
	resp.Slim(detailLevel)
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ListResources 数据层资源直查：列出账户有权查看的数据资源（配合 describe_resource + run_sql）。
func (h *knQueryToolsHandler) ListResources(c *gin.Context) {
	ctx := c.Request.Context()
	req := &knresources.ListResourcesReq{}
	// body 可选；忽略空 body 的绑定错误。
	_ = c.ShouldBindJSON(req)

	resp, err := h.resources.ListResources(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#ListResources] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// describeResourceReq describe_resource 入参。
type describeResourceReq struct {
	ResourceID string `json:"resource_id" form:"resource_id"`
}

// DescribeResource 取单个资源的物理 schema（列 + 连接器类型），写 run_sql 用。
func (h *knQueryToolsHandler) DescribeResource(c *gin.Context) {
	ctx := c.Request.Context()
	req := &describeResourceReq{}
	_ = c.ShouldBindQuery(req)
	_ = c.ShouldBindJSON(req)
	if req.ResourceID == "" {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "resource_id is required"))
		return
	}

	resp, err := h.resources.DescribeResource(ctx, req.ResourceID)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#DescribeResource] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// knDrillReq get_object_types / get_relation_types 共用入参。
type knDrillReq struct {
	KnID string   `json:"kn_id" form:"kn_id"`
	IDs  []string `json:"ids"`
}

func (r *knDrillReq) resolveKnID(c *gin.Context) string {
	if r.KnID != "" {
		return r.KnID
	}
	return c.GetHeader("X-Kn-ID")
}

// GetObjectTypes 按 id 批量取对象类完整定义（配合 get_kn_detail summary 下钻）。
func (h *knQueryToolsHandler) GetObjectTypes(c *gin.Context) {
	ctx := c.Request.Context()
	req := &knDrillReq{}
	_ = c.ShouldBindQuery(req)
	_ = c.ShouldBindJSON(req)
	knID := req.resolveKnID(c)
	if knID == "" {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "kn_id is required"))
		return
	}
	if len(req.IDs) == 0 {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "ids is required (object type ids from get_kn_detail)"))
		return
	}

	detail, err := h.bknBackend.GetKnowledgeNetworkDetail(ctx, knID)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetObjectTypes] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	matched, missing := detail.FilterObjectTypes(req.IDs)
	rest.ReplyOK(c, http.StatusOK, &interfaces.ObjectTypesResp{KnID: knID, ObjectTypes: matched, Missing: missing})
}

// GetRelationTypes 按 id 批量取关系类完整定义（含 mapping_rules）。
func (h *knQueryToolsHandler) GetRelationTypes(c *gin.Context) {
	ctx := c.Request.Context()
	req := &knDrillReq{}
	_ = c.ShouldBindQuery(req)
	_ = c.ShouldBindJSON(req)
	knID := req.resolveKnID(c)
	if knID == "" {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "kn_id is required"))
		return
	}
	if len(req.IDs) == 0 {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "ids is required (relation type ids from get_kn_detail)"))
		return
	}

	detail, err := h.bknBackend.GetKnowledgeNetworkDetail(ctx, knID)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetRelationTypes] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	matched, missing := detail.FilterRelationTypes(req.IDs)
	rest.ReplyOK(c, http.StatusOK, &interfaces.RelationTypesResp{KnID: knID, RelationTypes: matched, Missing: missing})
}
