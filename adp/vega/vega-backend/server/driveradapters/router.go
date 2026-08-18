// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package driveradapters provides HTTP handlers (primary adapters).
package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/middleware"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/common"
	"vega-backend/common/operationaudit"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics/auth"
	"vega-backend/logics/build_task"
	"vega-backend/logics/catalog"
	"vega-backend/logics/catalog_health_check_schedule"
	"vega-backend/logics/connector_type"
	"vega-backend/logics/dataset"
	"vega-backend/logics/discover_schedule"
	"vega-backend/logics/discover_task"
	"vega-backend/logics/local_index"
	"vega-backend/logics/resource"
	"vega-backend/logics/resource_data"
	"vega-backend/logics/semantic_understanding_task"
	"vega-backend/version"
)

// RestHandler interface
type RestHandler interface {
	RegisterPublic(engine *gin.Engine)
}

type restHandler struct {
	appSetting      *common.AppSetting
	auditRecorder   operationAuditRecorder
	auditQueryStore operationAuditQueryStore
	as              interfaces.AuthService
	bts             interfaces.BuildTaskService
	cs              interfaces.CatalogService
	cts             interfaces.ConnectorTypeService
	ds              interfaces.DatasetService
	dss             interfaces.DiscoverScheduleService
	dts             interfaces.DiscoverTaskService
	hcss            interfaces.CatalogHealthCheckScheduleService
	lim             interfaces.LocalIndexManager
	rds             interfaces.ResourceDataService
	rs              interfaces.ResourceService
	suts            interfaces.SemanticUnderstandingTaskService
}

// NewRestHandler creates a new RestHandler.
func NewRestHandler(appSetting *common.AppSetting) RestHandler {
	auditStore := operationaudit.NewStore(appSetting)
	as := auth.NewAuthService(appSetting)
	cs := catalog.NewCatalogService(appSetting)
	cts := connector_type.NewConnectorTypeService(appSetting)
	ds := dataset.NewDatasetService(appSetting)
	dts := discover_task.NewDiscoverTaskService(appSetting)
	dss := discover_schedule.NewDiscoverScheduleService(appSetting, dts)
	hcss := catalog_health_check_schedule.NewCatalogHealthCheckScheduleService(appSetting)
	lim := local_index.NewLocalIndexManager(appSetting)
	rds := resource_data.NewResourceDataService(appSetting)
	rs := resource.NewResourceService(appSetting)
	bts := build_task.NewBuildTaskService(appSetting, rs)
	suts := semantic_understanding_task.NewSemanticUnderstandingTaskService(appSetting)

	return &restHandler{
		appSetting:      appSetting,
		auditRecorder:   auditStore,
		auditQueryStore: auditStore,
		as:              as,
		bts:             bts,
		cs:              cs,
		cts:             cts,
		ds:              ds,
		dss:             dss,
		dts:             dts,
		hcss:            hcss,
		lim:             lim,
		rds:             rds,
		rs:              rs,
		suts:            suts,
	}
}

// RegisterPublic registers public API routes.
func (r *restHandler) RegisterPublic(c *gin.Engine) {
	c.Use(r.AccessLog())
	c.Use(middleware.TracingMiddleware())
	c.Use(r.TraceContextMiddleware())
	c.Use(r.LanguageMiddleware())
	c.Use(r.OperationAudit())

	c.GET("/health", r.HealthCheck)

	// External API (External
	apiV1 := c.Group("/api/vega-backend/v1")
	apiV1.Use(rest.PrivateNoCacheMiddleware())
	{
		apiV1.GET("/operation-audits", r.ListOperationAudits)
		apiV1.GET("/operation-audits/:event_id", r.GetOperationAudit)

		// Catalog APIs - External
		catalogs := apiV1.Group("/catalogs")
		{
			catalogs.GET("", r.ListCatalogsByEx)
			catalogs.POST("", r.verifyJsonContentType(), r.CreateCatalogByEx)
			catalogs.PUT("/:id", r.verifyJsonContentType(), r.UpdateCatalogByEx)
			catalogs.GET("/:id", r.GetCatalogsByEx)
			catalogs.DELETE("/:id", r.DeleteCatalogByEx)
			catalogs.POST("/:id/enable", r.EnableCatalogByEx)
			catalogs.POST("/:id/disable", r.DisableCatalogByEx)

			catalogs.GET("/:id/health-status", r.GetCatalogHealthStatusByEx)
			catalogs.GET("/:id/health-check-schedule", r.GetCatalogHealthCheckScheduleByEx)
			catalogs.PUT("/:id/health-check-schedule", r.verifyJsonContentType(), r.UpdateCatalogHealthCheckScheduleByEx)
			catalogs.POST("/:id/test-connection", r.TestConnectionByEx)
			catalogs.POST("/:id/discover", r.DiscoverCatalogResourcesByEx)

			catalogs.POST("/test-connection", r.verifyJsonContentType(), r.TestConnectionConfigByEx)
		}

		// Resource APIs - External
		resources := apiV1.Group("/resources")
		{
			resources.GET("", r.ListResourcesByEx)
			resources.POST("", r.verifyJsonContentType(), r.CreateResourceByEx)
			resources.POST("/:id/data", r.verifyJsonContentType(), r.PostResourceDataByEx)
			resources.PUT("/:id/data", r.verifyJsonContentType(), r.PutResourceDataByEx)
			resources.GET("/:id/data/:doc_id", r.GetResourceDataDocByEx)
			resources.PUT("/:id/data/:doc_id", r.verifyJsonContentType(), r.PutResourceDataDocByEx)
			resources.DELETE("/:id/data/:doc_ids", r.DeleteResourceDataByEx)
			resources.GET("/:id", r.GetResourcesByEx) // The ID is the resource ID, and multiple resource ids are separated by commas
			resources.PUT("/:id", r.verifyJsonContentType(), r.UpdateResourceByEx)
			resources.DELETE("/:id", r.DeleteResourcesByEx) // The ID is the resource ID, and multiple resource ids are separated by commas
			resources.POST("/query", r.verifyJsonContentType(), r.RawQueryByEx)
		}

		// DiscoverTask APIs - External
		discoverTasks := apiV1.Group("/discover-tasks")
		{
			discoverTasks.GET("", r.ListDiscoverTasksByEx)
			discoverTasks.GET("/:id", r.GetDiscoverTaskByEx)
			discoverTasks.DELETE("/:ids", r.DeleteDiscoverTasksByEx)
		}

		// DiscoverSchedule APIs - External
		discoverSchedules := apiV1.Group("/discover-schedules")
		{
			discoverSchedules.POST("", r.verifyJsonContentType(), r.CreateDiscoverScheduleByEx)
			discoverSchedules.GET("", r.ListDiscoverSchedulesByEx)
			discoverSchedules.GET("/:id", r.GetDiscoverScheduleByEx)
			discoverSchedules.PUT("/:id", r.verifyJsonContentType(), r.UpdateDiscoverScheduleByEx)
			discoverSchedules.DELETE("/:id", r.DeleteDiscoverScheduleByEx)
			discoverSchedules.POST("/:id/enable", r.EnableDiscoverScheduleByEx)
			discoverSchedules.POST("/:id/disable", r.DisableDiscoverScheduleByEx)
		}

		// BuildTask APIs - External
		buildTasks := apiV1.Group("/build-tasks")
		{
			buildTasks.POST("", r.verifyJsonContentType(), r.CreateBuildTaskByEx)
			buildTasks.GET("", r.ListBuildTasksByEx)
			buildTasks.GET("/:id", r.GetBuildTaskByEx)
			buildTasks.DELETE("/:ids", r.DeleteBuildTasksByEx)
			buildTasks.POST("/:id/start", r.StartBuildTaskByEx)
			buildTasks.POST("/:id/stop", r.StopBuildTaskByEx)
		}

		// SemanticUnderstandingTask APIs - External
		semanticUnderstandingTasks := apiV1.Group("/semantic-understanding-tasks")
		{
			semanticUnderstandingTasks.POST("", r.verifyJsonContentType(), r.CreateSemanticUnderstandingTaskByEx)
			semanticUnderstandingTasks.GET("", r.ListSemanticUnderstandingTasksByEx)
			semanticUnderstandingTasks.GET("/:id", r.GetSemanticUnderstandingTaskByEx)
			semanticUnderstandingTasks.DELETE("/:ids", r.DeleteSemanticUnderstandingTasksByEx)
		}

		// ConnectorType APIs - External
		connectorTypes := apiV1.Group("/connector-types")
		{
			connectorTypes.GET("", r.ListConnectorTypes)
			connectorTypes.POST("", r.verifyJsonContentType(), r.RegisterConnectorType)
			connectorTypes.GET("/:type", r.GetConnectorType)
			connectorTypes.PUT("/:type", r.verifyJsonContentType(), r.UpdateConnectorType)
			connectorTypes.DELETE("/:type", r.DeleteConnectorType)
			connectorTypes.POST("/:type/enable", r.EnableConnectorType)
			connectorTypes.POST("/:type/disable", r.DisableConnectorType)
		}

		apiV1.GET("/index-capabilities", r.GetIndexCapabilitiesByEx)

		apiV1.GET("/auth-resources", r.ListAuthResources)
	}

	// Internal API
	apiInV1 := c.Group("/api/vega-backend/in/v1")
	apiInV1.Use(rest.PrivateNoCacheMiddleware())
	{
		// Catalog APIs - Internal
		catalogs := apiInV1.Group("/catalogs")
		{
			catalogs.GET("", r.ListCatalogsByIn)
			catalogs.POST("", r.verifyJsonContentType(), r.CreateCatalogByIn)
			catalogs.PUT("/:id", r.verifyJsonContentType(), r.UpdateCatalogByIn)
			catalogs.GET("/:id", r.GetCatalogsByIn)
			catalogs.DELETE("/:id", r.DeleteCatalogByIn)
			catalogs.POST("/:id/enable", r.EnableCatalogByIn)
			catalogs.POST("/:id/disable", r.DisableCatalogByIn)

			catalogs.GET("/:id/health-status", r.GetCatalogHealthStatusByIn)
			catalogs.GET("/:id/health-check-schedule", r.GetCatalogHealthCheckScheduleByIn)
			catalogs.PUT("/:id/health-check-schedule", r.verifyJsonContentType(), r.UpdateCatalogHealthCheckScheduleByIn)
			catalogs.POST("/:id/test-connection", r.TestConnectionByIn)
			catalogs.POST("/:id/discover", r.DiscoverCatalogResourcesByIn)

			catalogs.POST("/test-connection", r.verifyJsonContentType(), r.TestConnectionConfigByIn)
		}

		// Resource APIs - Internal
		resources := apiInV1.Group("/resources")
		{
			resources.GET("", r.ListResourcesByIn)
			resources.POST("", r.verifyJsonContentType(), r.CreateResourceByIn)
			resources.POST("/:id/data", r.verifyJsonContentType(), r.PostResourceDataByIn)
			resources.PUT("/:id/data", r.verifyJsonContentType(), r.PutResourceDataByIn)
			resources.GET("/:id/data/:doc_id", r.GetResourceDataDocByIn)
			resources.PUT("/:id/data/:doc_id", r.verifyJsonContentType(), r.PutResourceDataDocByIn)
			resources.DELETE("/:id/data/:doc_ids", r.DeleteResourceDataByIn)
			resources.GET("/:id", r.GetResourcesByIn) // The ID is the resource ID, and multiple resource ids are separated by commas
			resources.PUT("/:id", r.verifyJsonContentType(), r.UpdateResourceByIn)
			resources.DELETE("/:id", r.DeleteResourcesByIn) // The ID is the resource ID, and multiple resource ids are separated by commas
			resources.POST("/query", r.verifyJsonContentType(), r.RawQueryByIn)
		}

		// DiscoverTask APIs - Internal
		discoverTasks := apiInV1.Group("/discover-tasks")
		{
			discoverTasks.GET("", r.ListDiscoverTasksByIn)
			discoverTasks.GET("/:id", r.GetDiscoverTaskByIn)
			discoverTasks.DELETE("/:ids", r.DeleteDiscoverTasksByIn)
		}

		// DiscoverSchedule APIs - Internal
		discoverSchedules := apiInV1.Group("/discover-schedules")
		{
			discoverSchedules.POST("", r.verifyJsonContentType(), r.CreateDiscoverScheduleByIn)
			discoverSchedules.GET("", r.ListDiscoverSchedulesByIn)
			discoverSchedules.GET("/:id", r.GetDiscoverScheduleByIn)
			discoverSchedules.PUT("/:id", r.verifyJsonContentType(), r.UpdateDiscoverScheduleByIn)
			discoverSchedules.DELETE("/:id", r.DeleteDiscoverScheduleByIn)
			discoverSchedules.POST("/:id/enable", r.EnableDiscoverScheduleByIn)
			discoverSchedules.POST("/:id/disable", r.DisableDiscoverScheduleByIn)
		}

		// BuildTask APIs - Internal
		buildTasks := apiInV1.Group("/build-tasks")
		{
			buildTasks.POST("", r.verifyJsonContentType(), r.CreateBuildTaskByIn)
			buildTasks.GET("", r.ListBuildTasksByIn)
			buildTasks.GET("/:id", r.GetBuildTaskByIn)
			buildTasks.DELETE("/:ids", r.DeleteBuildTasksByIn)
			buildTasks.POST("/:id/start", r.StartBuildTaskByIn)
			buildTasks.POST("/:id/stop", r.StopBuildTaskByIn)
		}

		// SemanticUnderstandingTask APIs - Internal
		semanticUnderstandingTasks := apiInV1.Group("/semantic-understanding-tasks")
		{
			semanticUnderstandingTasks.POST("", r.verifyJsonContentType(), r.CreateSemanticUnderstandingTaskByIn)
			semanticUnderstandingTasks.GET("", r.ListSemanticUnderstandingTasksByIn)
			semanticUnderstandingTasks.GET("/:id", r.GetSemanticUnderstandingTaskByIn)
			semanticUnderstandingTasks.DELETE("/:ids", r.DeleteSemanticUnderstandingTasksByIn)
		}
	}

	logger.Info("RestHandler RegisterPublic")
}

// HealthCheck Health checkup
func (r *restHandler) HealthCheck(c *gin.Context) {
	// Return service information
	rest.ReplyOK(c, http.StatusOK, gin.H{
		"ServerName":    version.ServerName,
		"ServerVersion": version.ServerVersion,
		"Language":      version.LanguageGo,
		"GoVersion":     version.GoVersion,
		"GoArch":        version.GoArch,
	})
}

// verifyJsonContentType middleware
func (r *restHandler) verifyJsonContentType() gin.HandlerFunc {
	return func(c *gin.Context) {
		//Intercept the request and determine whether the ContentType is XXX
		if c.ContentType() != interfaces.CONTENT_TYPE_JSON {
			httpErr := rest.NewHTTPError(c, http.StatusNotAcceptable, verrors.VegaBackend_InvalidRequestHeader_ContentType).
				WithErrorDetails(fmt.Sprintf("Content-Type header [%s] is not supported, expected is [application/json].", c.ContentType()))
			rest.ReplyError(c, httpErr)

			c.Abort()
			return
		}

		//Perform subsequent operations
		c.Next()
	}
}

// LanguageMiddleware resolves Accept-Language once and stores it in request context.
// Register this after TracingMiddleware so that the language context wraps the trace context.
func (r *restHandler) LanguageMiddleware() gin.HandlerFunc {
	return rest.LanguageMiddleware()
}

// TraceContextMiddleware parses OpenBKN phase-one trace context into request context.
func (r *restHandler) TraceContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := common.SetTraceContextToCtx(c.Request.Context(), common.TraceContextFromHeaders(c.GetHeader))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// Gin middleware for access logging.
func (r *restHandler) AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		beginTime := time.Now()
		c.Next()
		endTime := time.Now()
		durTime := endTime.Sub(beginTime).Seconds()

		logger.Debugf("access log: url: %s, method: %s, begin_time: %s, end_time: %s, subTime: %f",
			c.Request.URL.Path,
			c.Request.Method,
			beginTime.Format(libCommon.RFC3339Milli),
			endTime.Format(libCommon.RFC3339Milli),
			durTime,
		)
	}
}

// Verify oauth
func (r *restHandler) verifyOAuth(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	visitor, err := r.as.VerifyToken(ctx, c)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusUnauthorized, rest.PublicError_Unauthorized).
			WithErrorDetails(err.Error())
		rest.ReplyError(c, httpErr)
		return visitor, err
	}
	c.Set(operationAuditVisitorKey, visitor)

	return visitor, nil
}
