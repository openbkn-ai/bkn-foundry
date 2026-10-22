// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/object_type"
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
	public := engine.Group("/api/bkn-backend/v1")
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
	public.GET("/knowledge-networks/:kn_id/object-metrics/:metric_id/deletion-check", handler.CheckObjectMetricVersionDeletionByEx)
	public.DELETE("/knowledge-networks/:kn_id/object-metrics/:metric_id", handler.DeleteObjectMetricVersionByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/versions", handler.CreateObjectMetricVersionByEx)
	public.GET("/knowledge-networks/:kn_id/object-metrics/:metric_id/versions", handler.ListObjectMetricVersionsByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/validate", handler.ValidateObjectMetricVersionByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/publish", handler.PublishObjectMetricByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/deprecate", handler.DeprecateObjectMetricByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/logic-properties", handler.verifyJsonContentType(), handler.BindObjectMetricLogicPropertyByEx)
	public.PUT("/knowledge-networks/:kn_id/object-metrics/:metric_id/logic-properties/:property_name/metric-ref", handler.UpgradeObjectMetricLogicPropertyByEx)
	internal.GET("/knowledge-networks/:kn_id/object-metrics", handler.ListObjectMetricsByIn)
	internal.GET("/knowledge-networks/:kn_id/object-metrics/:metric_id/provenance", handler.GetObjectMetricProvenanceByIn)
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

// populateObjectMetricOwnerNames projects only the owner object-type name after
// metric authorization has succeeded. It deliberately bypasses object-type
// authorization and does not expose the object-type definition or properties.
func (r *restHandler) populateObjectMetricOwnerNames(
	ctx context.Context,
	knID string,
	branch string,
	records []*interfaces.ObjectMetricRecordV1,
) ([]*interfaces.ObjectMetricRecordV1, error) {
	ownerIDs := make([]string, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record == nil || record.Definition.OwnerObjectTypeID == "" {
			continue
		}
		ownerID := record.Definition.OwnerObjectTypeID
		if _, exists := seen[ownerID]; exists {
			continue
		}
		seen[ownerID] = struct{}{}
		ownerIDs = append(ownerIDs, ownerID)
	}
	if len(ownerIDs) == 0 {
		return records, nil
	}
	objectTypeService := r.ots
	if objectTypeService == nil {
		objectTypeService = object_type.NewObjectTypeService(r.appSetting)
	}
	owners, err := objectTypeService.GetObjectTypesMapByIDs(ctx, knID, branch, ownerIDs, false)
	if err != nil {
		return nil, err
	}
	projected := make([]*interfaces.ObjectMetricRecordV1, len(records))
	for index, record := range records {
		if record == nil {
			continue
		}
		copy := *record
		copy.OwnerObjectTypeName = ""
		if owner := owners[copy.Definition.OwnerObjectTypeID]; owner != nil {
			copy.OwnerObjectTypeName = owner.OTName
		}
		projected[index] = &copy
	}
	return projected, nil
}

func parseRequiredObjectMetricVersion(c *gin.Context) (int, bool) {
	if c.Query("version") == "" {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("version is required"))
		return 0, false
	}
	return parseObjectMetricVersion(c)
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
	if c.Query("version") != "" {
		version, ok := parseObjectMetricVersion(c)
		if !ok {
			return
		}
		if definition.Version != version {
			replyObjectMetricError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("definition.version must match query version"))
			return
		}
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

// GetObjectMetricProvenanceByIn resolves an exact immutable version under
// caller-scoped metric view authorization. Drafts are never returned.
func (r *restHandler) GetObjectMetricProvenanceByIn(c *gin.Context) {
	version, ok := parseObjectMetricVersion(c)
	if !ok || version == 0 {
		if ok {
			c.Status(http.StatusBadRequest)
		}
		return
	}
	vis := visitor.GenerateVisitor(c)
	metricID := c.Param("metric_id")
	versions, err := r.oms.ListVersions(objectMetricContext(c, vis), c.Param("kn_id"),
		c.DefaultQuery("branch", interfaces.MAIN_BRANCH), metricID)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	for _, record := range versions {
		if record != nil && record.Definition.ID == metricID && record.Definition.Version == version &&
			(record.Definition.Lifecycle.Status == "published" || record.Definition.Lifecycle.Status == "deprecated") {
			rest.ReplyOK(c, http.StatusOK, record)
			return
		}
	}
	c.Status(http.StatusNotFound)
}

func (r *restHandler) getObjectMetric(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	ctx := objectMetricContext(c, vis)
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	record, err := r.oms.Get(ctx, c.Param("kn_id"), branch, c.Param("metric_id"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	projected, err := r.populateObjectMetricOwnerNames(ctx, c.Param("kn_id"), branch, []*interfaces.ObjectMetricRecordV1{record})
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, projected[0])
}

func (r *restHandler) ListObjectMetricsByEx(c *gin.Context) {
	r.objectMetricExternal(c, func(c *gin.Context, vis hydra.Visitor) {
		r.listObjectMetrics(c, vis, true)
	})
}

// ListObjectMetricsByIn preserves the caller identity from internal headers;
// the service applies the same metric view permissions as the public list.
func (r *restHandler) ListObjectMetricsByIn(c *gin.Context) {
	r.listObjectMetrics(c, visitor.GenerateVisitor(c), false)
}

func (r *restHandler) listObjectMetrics(c *gin.Context, vis hydra.Visitor, includeOwnerNames bool) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		offset = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 500 {
		limit = 20
	}
	ctx := objectMetricContext(c, vis)
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	result, err := r.oms.List(ctx, interfaces.ObjectMetricListQueryV1{
		KNID: c.Param("kn_id"), Branch: branch,
		OwnerObjectTypeID: c.Query("owner_object_type_id"), MetricType: c.Query("metric_type"),
		Status: c.Query("status"), Keyword: c.Query("keyword"), Offset: offset, Limit: limit,
	})
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	if includeOwnerNames {
		projected, err := r.populateObjectMetricOwnerNames(ctx, c.Param("kn_id"), branch, result.Entries)
		if err != nil {
			replyObjectMetricError(c, err)
			return
		}
		response := *result
		response.Entries = projected
		rest.ReplyOK(c, http.StatusOK, &response)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) CheckObjectMetricVersionDeletionByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.checkObjectMetricVersionDeletion)
}

func (r *restHandler) checkObjectMetricVersionDeletion(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseRequiredObjectMetricVersion(c)
	if !ok {
		return
	}
	check, err := r.oms.CheckVersionDeletion(objectMetricContext(c, vis), c.Param("kn_id"),
		c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, check)
}

func (r *restHandler) DeleteObjectMetricVersionByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.deleteObjectMetricVersion)
}

func (r *restHandler) deleteObjectMetricVersion(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseRequiredObjectMetricVersion(c)
	if !ok {
		return
	}
	c.Set("operation_audit_target_ids", []string{fmt.Sprintf("%s@%d", c.Param("metric_id"), version)})
	record, err := r.oms.DeleteVersion(objectMetricContext(c, vis), c.Param("kn_id"),
		c.DefaultQuery("branch", interfaces.MAIN_BRANCH), c.Param("metric_id"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	c.Set("operation_audit_target_ids", []string{fmt.Sprintf("%s@%d", firstNonEmpty(record.Definition.ID, c.Param("metric_id")), version)})
	c.Set("operation_audit_target_names", []string{record.Definition.Name})
	c.Status(http.StatusNoContent)
}

func (r *restHandler) CreateObjectMetricVersionByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.createObjectMetricVersion)
}

func (r *restHandler) ListObjectMetricVersionsByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.listObjectMetricVersions)
}

func (r *restHandler) listObjectMetricVersions(c *gin.Context, vis hydra.Visitor) {
	ctx := objectMetricContext(c, vis)
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	entries, err := r.oms.ListVersions(ctx, c.Param("kn_id"), branch, c.Param("metric_id"))
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	projected, err := r.populateObjectMetricOwnerNames(ctx, c.Param("kn_id"), branch, entries)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, projected)
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

func (r *restHandler) UpgradeObjectMetricLogicPropertyByEx(c *gin.Context) {
	r.objectMetricExternal(c, r.upgradeObjectMetricLogicProperty)
}

func (r *restHandler) upgradeObjectMetricLogicProperty(c *gin.Context, vis hydra.Visitor) {
	version, ok := parseObjectMetricVersion(c)
	if !ok {
		return
	}
	if version == 0 {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusBadRequest, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("version is required"))
		return
	}
	upgrader, ok := r.oms.(interfaces.ObjectMetricLogicPropertyUpgradeV1)
	if !ok {
		rest.ReplyError(c, rest.NewHTTPError(c.Request.Context(), http.StatusNotImplemented, berrors.BknBackend_ObjectMetric_InvalidDefinition).WithErrorDetails("logical-property upgrade is unavailable"))
		return
	}
	ctx := objectMetricContext(c, vis)
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	metricID := c.Param("metric_id")
	record, err := r.oms.Get(ctx, knID, branch, metricID, version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	if record == nil {
		replyObjectMetricError(c, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ObjectMetric_NotFound))
		return
	}
	ownerID := record.Definition.OwnerObjectTypeID
	objectTypeService := r.ots
	if objectTypeService == nil {
		objectTypeService = object_type.NewObjectTypeService(r.appSetting)
	}
	objectType, err := objectTypeService.GetObjectTypeByID(ctx, nil, knID, branch, ownerID)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	if objectType == nil {
		replyObjectMetricError(c, errors.New("object type lookup returned no record"))
		return
	}
	property, err := upgrader.UpgradeLogicProperty(ctx, knID, branch, metricID, c.Param("property_name"), version)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	c.Set("operation_audit_target_ids", []string{ownerID})
	c.Set("operation_audit_target_names", []string{objectType.OTName})
	rest.ReplyOK(c, http.StatusOK, property)
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
	ctx := objectMetricContext(c, vis)
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	objectTypeService := r.ots
	if objectTypeService == nil {
		// The extension handler is assembled separately from the core REST handler,
		// but both share the process-wide object-type service singleton.
		objectTypeService = object_type.NewObjectTypeService(r.appSetting)
	}
	objectType, err := objectTypeService.GetObjectTypeByID(ctx, nil, c.Param("kn_id"), branch, binding.ObjectTypeID)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	if objectType == nil {
		replyObjectMetricError(c, errors.New("object type lookup returned no record"))
		return
	}
	property, err := r.oms.BindLogicProperty(ctx, c.Param("kn_id"), branch, c.Param("metric_id"), version, &binding)
	if err != nil {
		replyObjectMetricError(c, err)
		return
	}
	c.Set("operation_audit_target_ids", []string{binding.ObjectTypeID})
	c.Set("operation_audit_target_names", []string{objectType.OTName})
	rest.ReplyOK(c, http.StatusCreated, property)
}
