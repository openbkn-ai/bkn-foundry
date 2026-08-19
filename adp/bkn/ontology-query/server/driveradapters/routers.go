// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

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

	"ontology-query/common"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/logics/action_logs"
	"ontology-query/logics/action_scheduler"
	"ontology-query/logics/action_type"
	"ontology-query/logics/auth"
	"ontology-query/logics/knowledge_network"
	"ontology-query/logics/metric"
	"ontology-query/logics/object_type"
	"ontology-query/version"
)

type RestHandler interface {
	RegisterPublic(engine *gin.Engine)
}

type restHandler struct {
	appSetting *common.AppSetting
	as         interfaces.AuthService
	als        interfaces.ActionLogsService
	ass        interfaces.ActionSchedulerService
	ats        interfaces.ActionTypeService
	kns        interfaces.KnowledgeNetworkService
	ms         interfaces.MetricQueryService
	ots        interfaces.ObjectTypeService
}

func NewRestHandler(appSetting *common.AppSetting) RestHandler {
	r := &restHandler{
		appSetting: appSetting,
		als:        action_logs.NewActionLogsService(appSetting),
		as:         auth.NewAuthService(appSetting),
		ass:        action_scheduler.NewActionSchedulerService(appSetting),
		ats:        action_type.NewActionTypeService(appSetting),
		kns:        knowledge_network.NewKnowledgeNetworkService(appSetting),
		ms:         metric.NewMetricQueryService(appSetting),
		ots:        object_type.NewObjectTypeService(appSetting),
	}
	return r
}

func (r *restHandler) RegisterPublic(c *gin.Engine) {
	c.Use(r.AccessLog())
	c.Use(middleware.TracingMiddleware())
	c.Use(r.TraceContextMiddleware())
	c.Use(r.LanguageMiddleware())

	c.GET("/health", r.HealthCheck)

	apiV1 := c.Group("/api/ontology-query/v1")
	apiV1.Use(rest.PrivateNoCacheMiddleware())
	{
		apiV1.GET("/trace/outbox", r.ListTraceOutbox)
		apiV1.GET("/trace/outbox/:outbox_id", r.GetTraceOutbox)
		apiV1.POST("/trace/outbox/:outbox_id/retry", r.verifyJsonContentType(), r.RetryTraceOutbox)
		apiV1.POST("/trace/outbox/:outbox_id/abandon", r.verifyJsonContentType(), r.AbandonTraceOutbox)

		// Query object data for the specified object type.
		apiV1.POST("/knowledge-networks/:kn_id/object-types/:ot_id", r.verifyJsonContentType(), r.GetObjectsInObjectTypeByEx)
		apiV1.POST("/knowledge-networks/:kn_id/object-types/:ot_id/properties", r.verifyJsonContentType(), r.GetObjectsPropertiesByEx)
		// Get an object subgraph by start point, direction, and path length.
		apiV1.POST("/knowledge-networks/:kn_id/subgraph", r.verifyJsonContentType(), r.GetObjectsSubgraphByEx)
		apiV1.POST("/knowledge-networks/:kn_id/subgraph/objects", r.verifyJsonContentType(), r.GetObjectsSubgraphByObjectsByEx)
		apiV1.POST("/knowledge-networks/:kn_id/action-types/:at_id", r.verifyJsonContentType(), r.GetActionsInActionTypeByEx)

		// Action execution APIs.
		apiV1.POST("/knowledge-networks/:kn_id/action-types/:at_id/execute", r.verifyJsonContentType(), r.ExecuteActionByEx)
		apiV1.GET("/knowledge-networks/:kn_id/action-executions/:execution_id", r.GetActionExecutionByEx)
		apiV1.GET("/knowledge-networks/:kn_id/action-logs", r.QueryActionLogsByEx)
		apiV1.GET("/knowledge-networks/:kn_id/action-logs/:log_id", r.GetActionLogByEx)
		apiV1.POST("/knowledge-networks/:kn_id/action-logs/:log_id/cancel", r.CancelActionLogByEx)

		apiV1.POST("/knowledge-networks/:kn_id/metrics/dry-run", r.verifyJsonContentType(), r.PostMetricDryRunByEx)
		apiV1.POST("/knowledge-networks/:kn_id/metrics/:metric_id/data", r.verifyJsonContentType(), r.PostMetricDataByEx)
	}

	apiInV1 := c.Group("/api/ontology-query/in/v1")
	apiInV1.Use(rest.PrivateNoCacheMiddleware())
	{
		// Knowledge networks.
		apiInV1.POST("/knowledge-networks/:kn_id/object-types/:ot_id", r.verifyJsonContentType(), r.GetObjectsInObjectTypeByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/object-types/:ot_id/properties", r.verifyJsonContentType(), r.GetObjectsPropertiesByIn)
		// Get an object subgraph by start point, direction, and path length.
		apiInV1.POST("/knowledge-networks/:kn_id/subgraph", r.verifyJsonContentType(), r.GetObjectsSubgraphByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/subgraph/objects", r.verifyJsonContentType(), r.GetObjectsSubgraphByObjectsByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/action-types/:at_id", r.verifyJsonContentType(), r.GetActionsInActionTypeByIn)

		// Action execution APIs (internal).
		apiInV1.POST("/knowledge-networks/:kn_id/action-types/:at_id/execute", r.verifyJsonContentType(), r.ExecuteActionByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-executions/:execution_id", r.GetActionExecutionByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-logs", r.QueryActionLogsByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-logs/:log_id", r.GetActionLogByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/action-logs/:log_id/cancel", r.CancelActionLogByIn)

		apiInV1.POST("/knowledge-networks/:kn_id/metrics/dry-run", r.verifyJsonContentType(), r.PostMetricDryRunByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/metrics/:metric_id/data", r.verifyJsonContentType(), r.PostMetricDataByIn)
	}

	logger.Info("RestHandler RegisterPublic")
}

// HealthCheck reports service health.
func (r *restHandler) HealthCheck(c *gin.Context) {
	// Return service information.
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
		// Reject requests whose Content-Type is not application/json.
		if c.ContentType() != interfaces.CONTENT_TYPE_JSON {
			httpErr := rest.NewHTTPError(c, http.StatusNotAcceptable, oerrors.OntologyQuery_InvalidRequestHeader_ContentType).
				WithErrorDetails(fmt.Sprintf("Content-Type header [%s] is not supported, expected is [application/json].", c.ContentType()))
			rest.ReplyError(c, httpErr)

			c.Abort()
			return
		}

		// Continue with the next handler.
		c.Next()
	}
}

// LanguageMiddleware resolves Accept-Language once and stores it in request context.
// Registration order must be after TracingMiddleware so the language context is layered on top of the trace context.
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

// Gin middleware access log.
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

// Verify OAuth credentials.
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
