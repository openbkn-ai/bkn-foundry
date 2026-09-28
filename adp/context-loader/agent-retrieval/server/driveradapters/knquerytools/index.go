// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knquerytools provides HTTP handlers for the query tools that are also
// exposed as MCP tools: run_sql, run_cypher, list_knowledge_networks, get_kn_detail,
// list_resources, describe_resource.
// These internal REST endpoints back the operator-integration toolbox entries.
package knquerytools

import (
	stderrors "errors"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kncypher"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knrunsql"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/objectpermission"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permission"
)

// KnQueryToolsHandler handle run_sql / list_knowledge_networks / get_kn_detail /.
// Internal REST entry for get_object_types / get_relation_types / list_resources / describe_resource.
type KnQueryToolsHandler interface {
	RunSQL(c *gin.Context)
	RunCypher(c *gin.Context)
	ListKnowledgeNetworks(c *gin.Context)
	GetKnDetail(c *gin.Context)
	GetObjectTypes(c *gin.Context)
	GetRelationTypes(c *gin.Context)
	QueryMetric(c *gin.Context)
	ListResources(c *gin.Context)
	DescribeResource(c *gin.Context)
}

type knQueryToolsHandler struct {
	logger       interfaces.Logger
	runSQL       knrunsql.KnRunSQLService
	cypher       kncypher.KnCypherService
	resources    knresources.KnResourcesService
	bknBackend   interfaces.BknBackendAccess
	metrics      knmetrics.KnMetricsService
	schemaAccess interfaces.ObjectSchemaAccess
	knAuthz      interfaces.KnowledgeNetworkAuthorizer
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
			logger:       conf.GetLogger(),
			runSQL:       knrunsql.NewKnRunSQLService(),
			cypher:       kncypher.NewKnCypherService(),
			resources:    knresources.NewKnResourcesService(),
			bknBackend:   drivenadapters.NewBknBackendAccess(),
			metrics:      knmetrics.NewKnMetricsService(),
			schemaAccess: drivenadapters.NewObjectSchemaAccess(),
			knAuthz:      permission.NewKnowledgeNetworkAuthorizer(conf),
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
		if _, ok := errors.HTTPStatus(err); ok {
			rest.ReplyError(c, err)
			return
		}
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// RunCypher compiles one read-only Cypher query against the knowledge network's
// model in bkn-backend and returns the rows it produced.
func (h *knQueryToolsHandler) RunCypher(c *gin.Context) {
	ctx := c.Request.Context()
	req := &kncypher.RunCypherReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}

	resp, err := h.cypher.RunCypher(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#RunCypher] run cypher failed: %v", err)
		// A refusal from the compiler already carries its own status and names
		// the construct it refused; that message is what tells the caller how
		// to rewrite the query, so it is passed through untouched. Only the
		// errors raised in this service need a status of their own.
		httpErr := &errors.HTTPError{}
		if !stderrors.As(err, &httpErr) {
			err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		}
		rest.ReplyError(c, err)
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
	// A network too large to answer with its concept model narrows to its navigation
	// shell, and does so here -- before the two steps below, which each cost a
	// downstream call per object type of a list this answer will not carry (#1877).
	if resp.NeedsNavigationShell() {
		resp.ReduceToNavigationShell()
		resp.Notice = errors.LocalizedDetail(ctx, resp.NavigationShellNoticeKey(),
			resp.ObjectTypeCount, resp.RelationTypeCount,
			interfaces.MaxSummaryObjectTypes, interfaces.MaxSummaryRelationTypes)
	} else {
		resp.ObjectTypes, err = objectpermission.FilterObjectTypes(ctx, h.schemaAccess, req.KnID, resp.ObjectTypes)
		if err != nil {
			h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetKnDetail] object property authorization failed: %v", err)
			rest.ReplyError(c, err)
			return
		}
		// Only the count is attached but not the details: it is enough for the Agent to judge which object type is worthy of drill-down metrics.
		if err := h.metrics.AttachRelatedMetricCounts(ctx, req.KnID, resp.ObjectTypes); err != nil {
			h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetKnDetail] metric authorization failed: %v", err)
			rest.ReplyError(c, err)
			return
		}
	}
	// The mounted Skills and tools, counted like the metrics above — but gated first. Those
	// counts inherit their scope from object types that already survived FilterObjectTypes; the
	// bindings have none, and are read with this service's identity, so every other reader of
	// them authorizes the caller first. A refusal omits the field rather than failing the call:
	// absent means unknown, which is what it must also mean for a list this call could not read.
	if err := h.knAuthz.AuthorizeRead(ctx, req.KnID); err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetKnDetail] capability counts withheld: %v", err)
	} else if refs, err := h.bknBackend.ListKNCapabilities(ctx, req.KnID, "", ""); err == nil {
		resp.AttachMountedCapabilities(refs)
	} else {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetKnDetail] capability bindings unreadable: %v", err)
	}
	detailLevel := req.DetailLevel
	if detailLevel == "" {
		detailLevel = interfaces.DetailLevelSummary
	}
	resp.Slim(detailLevel)
	// The same trim the MCP tool applies, for the same reason: an all-full map is the
	// caller's own permissions read back to them one property at a time. Answering the
	// REST caller differently made two faces of one service disagree about one network
	// by megabytes (#1891).
	if detailLevel != interfaces.DetailLevelFull {
		objectpermission.OmitUnrestrictedPermissions(resp.ObjectTypes)
	}
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
	// Offset and Limit are the window get_object_types walks with when no ids are
	// given; they are ignored when ids are, and get_relation_types ignores them
	// outright -- it has never had a caller without ids.
	Offset int `json:"offset" form:"offset"`
	Limit  int `json:"limit" form:"limit"`
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
	// No ids means the caller has none to give -- which is the state get_kn_detail
	// leaves them in on a network past its caps, where the concept arrays are
	// withheld and nothing else here enumerates them (#1889). Read a page instead
	// of refusing. Named ids stay the primary path and ignore the paging inputs.
	var (
		matched    []*interfaces.ObjectType
		err        error
		totalCount int64
		nextOffset *int
		offset     int
		notice     string
	)
	if len(req.IDs) == 0 {
		var (
			limit   int
			clamped bool
		)
		offset, limit, clamped = interfaces.ResolveObjectTypePage(req.Offset, req.Limit)
		page, listErr := h.bknBackend.ListObjectTypes(ctx, knID, offset, limit)
		if listErr != nil {
			h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetObjectTypes] list failed: %v", listErr)
			rest.ReplyError(c, listErr)
			return
		}
		matched, totalCount = page.Entries, page.TotalCount
		nextOffset = interfaces.NextObjectTypeOffset(offset, len(matched), totalCount)
		if clamped {
			notice = errors.LocalizedDetail(ctx, "ObjectTypePageLimitClamped",
				req.Limit, interfaces.MaxObjectTypePageSize, limit)
		}
	} else if matched, err = h.bknBackend.GetObjectTypeDetail(ctx, knID, req.IDs, true); err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetObjectTypes] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	matched, err = objectpermission.FilterObjectTypes(ctx, h.schemaAccess, knID, matched)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetObjectTypes] object property authorization failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	objectpermission.TrimObjectTypesToIndexBackedOps(matched)
	// OT-first step 2: scoped metrics with unbound logical properties are only visible here.
	if err := h.metrics.AttachRelatedMetrics(ctx, knID, matched); err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetObjectTypes] metric authorization failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	bkntrace.EmitSchemaDefinitionEvents(ctx, h.logger, "object", knID, req.IDs, len(matched))
	rest.ReplyOK(c, http.StatusOK, &interfaces.ObjectTypesResp{KnID: knID, ObjectTypes: matched,
		TotalCount: totalCount, NextOffset: nextOffset, Notice: notice})
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

	matched, err := h.bknBackend.GetRelationTypeDetail(ctx, knID, req.IDs, true)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnQueryToolsHandler#GetRelationTypes] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	bkntrace.EmitSchemaDefinitionEvents(ctx, h.logger, "relation", knID, req.IDs, len(matched))
	rest.ReplyOK(c, http.StatusOK, &interfaces.RelationTypesResp{KnID: knID, RelationTypes: matched})
}

// QueryMetric takes the number according to the metric's own semantics (step 3 of the OT-first path).
//
// Separate from get_logic_properties_values: instance level, which one has bound logical properties; class level, or unbound.
// Logical attributes go this way. Neither should be replaced by run_sql - the semantics is in MetricDefinition.
func (h *knQueryToolsHandler) QueryMetric(c *gin.Context) {
	ctx := c.Request.Context()
	req := &interfaces.QueryMetricReq{}
	if err := common.BindPreciseJSON(c.Request.Body, req); err != nil {
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
