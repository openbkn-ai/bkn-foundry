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
	appSetting *common.AppSetting
	as         interfaces.AuthService
	bts        interfaces.BuildTaskService
	cs         interfaces.CatalogService
	cts        interfaces.ConnectorTypeService
	ds         interfaces.DatasetService
	dss        interfaces.DiscoverScheduleService
	dts        interfaces.DiscoverTaskService
	hcss       interfaces.CatalogHealthCheckScheduleService
	lim        interfaces.LocalIndexManager
	rds        interfaces.ResourceDataService
	rs         interfaces.ResourceService
	suts       interfaces.SemanticUnderstandingTaskService
}

// NewRestHandler creates a new RestHandler.
func NewRestHandler(appSetting *common.AppSetting) RestHandler {
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
		appSetting: appSetting,
		as:         as,
		bts:        bts,
		cs:         cs,
		cts:        cts,
		ds:         ds,
		dss:        dss,
		dts:        dts,
		hcss:       hcss,
		lim:        lim,
		rds:        rds,
		rs:         rs,
		suts:       suts,
	}
}

// RegisterPublic registers public API routes.
func (r *restHandler) RegisterPublic(c *gin.Engine) {
	c.Use(r.AccessLog())
	c.Use(middleware.TracingMiddleware())
	c.Use(r.TraceContextMiddleware())
	c.Use(r.LanguageMiddleware())

	c.GET("/health", r.HealthCheck)

	// 外部 API (External)
	apiV1 := c.Group("/api/vega-backend/v1")
	{
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
			resources.GET("/:id", r.GetResourcesByEx) // id为资源ID，多个资源ID逗号分隔
			resources.PUT("/:id", r.verifyJsonContentType(), r.UpdateResourceByEx)
			resources.DELETE("/:id", r.DeleteResourcesByEx) // id为资源ID，多个资源ID逗号分隔
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

	// 内部 API (Internal)
	apiInV1 := c.Group("/api/vega-backend/in/v1")
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
			resources.GET("/:id", r.GetResourcesByIn) // id为资源ID，多个资源ID逗号分隔
			resources.PUT("/:id", r.verifyJsonContentType(), r.UpdateResourceByIn)
			resources.DELETE("/:id", r.DeleteResourcesByIn) // id为资源ID，多个资源ID逗号分隔
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

// HealthCheck 健康检查
func (r *restHandler) HealthCheck(c *gin.Context) {
	// 返回服务信息
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
		//拦截请求，判断ContentType是否为XXX
		if c.ContentType() != interfaces.CONTENT_TYPE_JSON {
			httpErr := rest.NewHTTPError(c, http.StatusNotAcceptable, verrors.VegaBackend_InvalidRequestHeader_ContentType).
				WithErrorDetails(fmt.Sprintf("Content-Type header [%s] is not supported, expected is [application/json].", c.ContentType()))
			rest.ReplyError(c, httpErr)

			c.Abort()
			return
		}

		//执行后续操作
		c.Next()
	}
}

// gin中间件 把 X-Language 头解析结果挂到 request ctx。
// 注册顺序必须在 TracingMiddleware 之后，这样 language ctx 叠加在 trace ctx 上。
func (r *restHandler) LanguageMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(rest.GetLanguageCtx(c))
		c.Next()
	}
}

// TraceContextMiddleware parses OpenBKN phase-one trace context into request context.
func (r *restHandler) TraceContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := common.SetTraceContextToCtx(c.Request.Context(), common.TraceContextFromHeaders(c.GetHeader))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// gin中间件 访问日志
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

// 校验oauth
func (r *restHandler) verifyOAuth(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	visitor, err := r.as.VerifyToken(ctx, c)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusUnauthorized, rest.PublicError_Unauthorized).
			WithErrorDetails(err.Error())
		rest.ReplyError(c, httpErr)
		return visitor, err
	}

	return visitor, nil
}
