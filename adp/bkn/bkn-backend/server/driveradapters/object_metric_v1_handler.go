// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/visitor"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/auth"
)

// RegisterObjectMetricRoutes mounts the public and internal object-metric API
// contracts around an implementation supplied during paid-image assembly.
// Community binaries never call this function, so they expose no object-metric
// routes even though the stable HTTP adapter and DTO contracts remain in core.
func RegisterObjectMetricRoutes(
	engine *gin.Engine,
	appSetting *common.AppSetting,
	service interfaces.ObjectMetricServiceV1,
	entitlementMiddleware gin.HandlerFunc,
) {
	handler := &restHandler{
		appSetting: appSetting,
		as:         auth.NewAuthService(appSetting),
		oms:        service,
	}
	public := engine.Group("/api/bkn-backend/v2")
	internal := engine.Group("/api/bkn-backend/in/v1")
	public.Use(rest.PrivateNoCacheMiddleware())
	internal.Use(rest.PrivateNoCacheMiddleware())
	if entitlementMiddleware != nil {
		public.Use(entitlementMiddleware)
		internal.Use(entitlementMiddleware)
	}

	public.GET("/object-metrics/capabilities", handler.GetObjectMetricCapabilitiesByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics", handler.verifyJsonContentType(), handler.CreateObjectMetricDraftByEx)
	public.GET("/knowledge-networks/:kn_id/object-metrics", handler.ListObjectMetricsByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/validate", handler.verifyJsonContentType(), handler.ValidateObjectMetricByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/preview", handler.verifyJsonContentType(), handler.PreviewObjectMetricByEx)
	public.GET("/knowledge-networks/:kn_id/object-metrics/:metric_id", handler.GetObjectMetricByEx)
	public.PUT("/knowledge-networks/:kn_id/object-metrics/:metric_id", handler.verifyJsonContentType(), handler.UpdateObjectMetricDraftByEx)
	public.DELETE("/knowledge-networks/:kn_id/object-metrics/:metric_id", handler.DeleteObjectMetricDraftByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/versions", handler.CreateObjectMetricVersionByEx)
	public.GET("/knowledge-networks/:kn_id/object-metrics/:metric_id/versions", handler.ListObjectMetricVersionsByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/validate", handler.ValidateObjectMetricVersionByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/publish", handler.PublishObjectMetricByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/deprecate", handler.DeprecateObjectMetricByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/logic-properties", handler.verifyJsonContentType(), handler.BindObjectMetricLogicPropertyByEx)
	internal.GET("/knowledge-networks/:kn_id/object-metrics/:metric_id/execution-context", handler.GetObjectMetricExecutionContextByIn)
}

func (r *restHandler) objectMetricExternal(c *gin.Context, next func(*gin.Context, hydra.Visitor)) {
	vis, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	next(c, vis)
}

func objectMetricContext(c *gin.Context, vis hydra.Visitor) context.Context {
	return context.WithValue(c.Request.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)})
}

func replyObjectMetricError(c *gin.Context, err error) {
	var httpErr *rest.HTTPError
	if errors.As(err, &httpErr) {
		rest.ReplyError(c, httpErr)
		return
	}
	rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusInternalServerError, berrors.BknBackend_ObjectMetric_InternalError).WithErrorDetails(err.Error()))
}

func parseObjectMetricVersion(c *gin.Context) (int, bool) {
	raw := c.Query("version")
	if raw == "" {
		return 0, true
	}
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("version must be a positive integer"))
		return 0, false
	}
	return version, true
}

func (r *restHandler) CreateObjectMetricDraftByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.createObjectMetricDraft)
}

func (r *restHandler) createObjectMetricDraft(c *gin.Context, vis hydra.Visitor) {
	var definition interfaces.ObjectMetricDefinitionV1
	if err := c.ShouldBindJSON(&definition); err != nil {
		replyObjectMetricError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails(err.Error()))
		return
	}
	record, err := r.oms.CreateDraft(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), &definition)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusCreated, record)
}

func (r *restHandler) UpdateObjectMetricDraftByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.updateObjectMetricDraft)
}

func (r *restHandler) updateObjectMetricDraft(c *gin.Context, vis hydra.Visitor) {
	var definition interfaces.ObjectMetricDefinitionV1
	if err := c.ShouldBindJSON(&definition); err != nil {
		replyObjectMetricError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails(err.Error()))
		return
	}
	record, err := r.oms.UpdateDraft(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), &definition)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, record)
}

func (r *restHandler) GetObjectMetricByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.getObjectMetric)
}

func (r *restHandler) getObjectMetric(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	record, err := r.oms.Get(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, record)
}

func (r *restHandler) ListObjectMetricsByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.listObjectMetrics)
}

func (r *restHandler) listObjectMetrics(c *gin.Context, vis hydra.Visitor) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		offset = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 500 {
		limit = 20
	}
	result, err := r.oms.List(objectMetricContext(c, vis), interfaces.ObjectMetricListQueryV1{
		KNID: c.Param("kn_id"), Branch: c.DefaultQuery("branch", interfaces.MAIN_BRANCH),
		OwnerObjectTypeID: c.Query("owner_object_type_id"), MetricType: c.Query("metric_type"),
		Status: c.Query("status"), Keyword: c.Query("keyword"), Offset: offset, Limit: limit,
	})
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) DeleteObjectMetricDraftByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.deleteObjectMetricDraft)
}

func (r *restHandler) deleteObjectMetricDraft(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	ctx := objectMetricContext(c, vis)
	if version == 0 {
		record, err := r.oms.Get(ctx, c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), 0)
		if err != nil {
			replyObjectMetricError(c, err)
			return
		}
		version = record.Definition.Version
	}
	if err := r.oms.DeleteDraft(ctx, c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version); err != nil {
		replyObjectMetricError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (r *restHandler) CreateObjectMetricVersionByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.createObjectMetricVersion)
}

func (r *restHandler) ListObjectMetricVersionsByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.listObjectMetricVersions)
}

func (r *restHandler) listObjectMetricVersions(c *gin.Context, vis hydra.Visitor) {
	entries, err := r.oms.ListVersions(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"))
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, entries)
}

func (r *restHandler) createObjectMetricVersion(c *gin.Context, vis hydra.Visitor) {
	record, err := r.oms.CreateNextDraft(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"))
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusCreated, record)
}

func (r *restHandler) ValidateObjectMetricByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.validateObjectMetric)
}

func (r *restHandler) ValidateObjectMetricVersionByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.validateObjectMetricVersion)
}

func (r *restHandler) validateObjectMetricVersion(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	if version == 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("version is required"))
		return
	}
	record, issues, err := r.oms.ValidateVersion(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	if len(issues) > 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusUnprocessableEntity, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails(issues))
		return
	}
	rest.ReplyOK(c, http.StatusOK, record)
}

func (r *restHandler) validateObjectMetric(c *gin.Context, vis hydra.Visitor) {
	var definition interfaces.ObjectMetricDefinitionV1
	if err := c.ShouldBindJSON(&definition); err != nil {
		replyObjectMetricError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails(err.Error()))
		return
	}
	result, err := r.oms.Validate(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), &definition)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) PreviewObjectMetricByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.previewObjectMetric)
}

// GetObjectMetricExecutionContextByIn returns the immutable metric dependency
// closure to ontology-query. The caller identity is propagated through the
// internal account headers and the service still performs KN authorization.
func (r *restHandler) GetObjectMetricExecutionContextByIn(c *gin.Context) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	if version == 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("an immutable metric version is required for execution"))
		return
	}
	vis := visitor.GenerateVisitor(c)
	result, err := r.oms.GetExecutionContext(
		objectMetricContext(c, vis),
		c.Param("kn_id"),
		c.DefaultQuery("branch", interfaces.MAIN_BRANCH),
		c.Param("metric_id"),
		version,
	)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) previewObjectMetric(c *gin.Context, vis hydra.Visitor) {
	var definition interfaces.ObjectMetricDefinitionV1
	if err := c.ShouldBindJSON(&definition); err != nil {
		replyObjectMetricError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails(err.Error()))
		return
	}
	result, err := r.oms.Preview(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), &definition)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) PublishObjectMetricByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.publishObjectMetric)
}

func (r *restHandler) publishObjectMetric(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	if version == 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("version is required"))
		return
	}
	record, issues, err := r.oms.Publish(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	if len(issues) > 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusUnprocessableEntity, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails(issues))
		return
	}
	rest.ReplyOK(c, http.StatusOK, record)
}

func (r *restHandler) GetObjectMetricCapabilitiesByEx(c *gin.Context) {
	r.objectMetricExternal(c, func(c *gin.Context, _ hydra.Visitor) {
		rest.ReplyOK(c, http.StatusOK, r.oms.Capabilities())
	})
}

func (r *restHandler) DeprecateObjectMetricByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.deprecateObjectMetric)
}

func (r *restHandler) deprecateObjectMetric(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	if version == 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("version is required"))
		return
	}
	record, err := r.oms.Deprecate(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, record)
}

func (r *restHandler) BindObjectMetricLogicPropertyByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.bindObjectMetricLogicProperty)
}

func (r *restHandler) bindObjectMetricLogicProperty(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	if version == 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("version is required"))
		return
	}
	var binding interfaces.MetricPropertyBindingV1
	if err := c.ShouldBindJSON(&binding); err != nil {
		replyObjectMetricError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails(err.Error()))
		return
	}
	property, err := r.oms.BindLogicProperty(objectMetricContext(c, vis), c.Param("kn_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version, &binding)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusCreated, property)
}
