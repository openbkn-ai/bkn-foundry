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

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knrunsql"
)

// KnQueryToolsHandler handle run_sql / list_knowledge_networks / get_kn_detail /.
// Internal REST entry for get_object_types / get_relation_types / list_resources / describe_resource.
type KnQueryToolsHandler interface {
	RunSQL(c *gin.Context)
	ListKnowledgeNetworks(c *gin.Context)
	GetKnDetail(c *gin.Context)
	GetObjectTypes(c *gin.Context)
	GetRelationTypes(c *gin.Context)
	QueryMetric(c *gin.Context)
	ListResources(c *gin.Context)
	DescribeResource(c *gin.Context)
}

type knQueryToolsHandler struct {
	logger     interfaces.Logger
	runSQL     knrunsql.KnRunSQLService
	resources  knresources.KnResourcesService
	bknBackend interfaces.BknBackendAccess
	metrics    knmetrics.KnMetricsService
}

var (
	once    sync.Once
	handler KnQueryToolsHandler
)

// NewKnQueryToolsHandler create KnQueryToolsHandler singleton.
func NewKnQueryToolsHandler() KnQueryToolsHandler {
	once.Do(func() {
		conf := config.NewConfigLoader()
		handler = &knQueryToolsHandler{
			logger:     conf.GetLogger(),
			runSQL:     knrunsql.NewKnRunSQLService(),
			resources:  knresources.NewKnResourcesService(),
			bknBackend: drivenadapters.NewBknBackendAccess(),
			metrics:    knmetrics.NewKnMetricsService(),
		}
	})
	return handler
}

// RunSQL executes read-only SQL (forced SELECT-only) on data resources mounted on the knowledge network.
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

// ListKnowledgeNetworks Lists knowledge networks (discovered by kn_id).
func (h *knQueryToolsHandler) ListKnowledgeNetworks(c *gin.Context) {
	ctx := c.Request.Context()
	req := &interfaces.ListKnReq{}
	// The body is optional; ignore binding errors for an empty body.
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

// getKnDetailReq get_kn_detail input parameter.
type getKnDetailReq struct {
	KnID        string `json:"kn_id" form:"kn_id"`
	DetailLevel string `json:"detail_level" form:"detail_level"` // Summary (default)| full.
}

// GetKnDetail Gets knowledge network details (concept group/object type/relation type/action class).
// detail_level=summary (default) returns the skeleton + attribute name, full returns the full amount.
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
	// Only the count is attached but not the details: it is enough for the Agent to judge which object type is worthy of drill-down metrics.
	h.metrics.AttachRelatedMetricCounts(ctx, req.KnID, resp.ObjectTypes)
	detailLevel := req.DetailLevel
	if detailLevel == "" {
		detailLevel = interfaces.DetailLevelSummary
	}
	resp.Slim(detailLevel)
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ListResources Data layer resource direct query: List the data resources that the account has the right to view (with describe_resource + run_sql).
func (h *knQueryToolsHandler) ListResources(c *gin.Context) {
	ctx := c.Request.Context()
	req := &knresources.ListResourcesReq{}
	// The body is optional; ignore binding errors for an empty body.
	_ = c.ShouldBindJSON(req)

	resp, err := h.resources.ListResources(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#ListResources] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// describeResourceReq describe_resource input parameter.
type describeResourceReq struct {
	ResourceID string `json:"resource_id" form:"resource_id"`
}

// DescribeResource takes the physical schema (column + connector type) of a single resource and writes it to run_sql.
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

// knDrillReq get_object_types / get_relation_types share input parameters.
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

// GetObjectTypes retrieves the complete definitions of object types in batches by ID (cooperated with get_kn_detail summary drill-down).
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
	// OT-first step 2: scoped metrics with unbound logical properties are only visible here.
	h.metrics.AttachRelatedMetrics(ctx, knID, matched)
	bkntrace.EmitSchemaDefinitionEvents(ctx, h.logger, "object", knID, req.IDs, len(matched))
	rest.ReplyOK(c, http.StatusOK, &interfaces.ObjectTypesResp{KnID: knID, ObjectTypes: matched, Missing: missing})
}

// GetRelationTypes retrieves complete definitions of relation types (including mapping_rules) in batches by id.
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
	bkntrace.EmitSchemaDefinitionEvents(ctx, h.logger, "relation", knID, req.IDs, len(matched))
	rest.ReplyOK(c, http.StatusOK, &interfaces.RelationTypesResp{KnID: knID, RelationTypes: matched, Missing: missing})
}

// QueryMetric takes the number according to the metric's own semantics (step 3 of the OT-first path).
//
// Separate from get_logic_properties_values: instance level, which one has bound logical properties; class level, or unbound.
// Logical attributes go this way. Neither should be replaced by run_sql - the semantics is in MetricDefinition.
func (h *knQueryToolsHandler) QueryMetric(c *gin.Context) {
	ctx := c.Request.Context()
	req := &interfaces.QueryMetricReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}
	if req.KnID == "" {
		req.KnID = c.GetHeader("X-Kn-ID")
	}

	resp, err := h.metrics.QueryMetric(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#QueryMetric] kn=%s metric=%s failed: %v",
			req.KnID, req.MetricID, err)
		if httpErr, ok := err.(*errors.HTTPError); ok {
			rest.ReplyError(c, httpErr)
			return
		}
		// Errors in input parameters (missing kn_id / metric_id, contradictory time windows) are all the fault of the caller.
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}
