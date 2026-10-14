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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_logs"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_scheduler"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/object_type"
	queryauthorization "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/query_authorization"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/version"
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
	oms        interfaces.ObjectMetricQueryServiceV1
	ots        interfaces.ObjectTypeService
	qas        interfaces.QueryAuthorizationService
	oma        interfaces.OntologyManagerAccess
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
		qas:        queryauthorization.NewQueryAuthorizationService(appSetting),
		oma:        logics.OMA,
	}
	return r
}

func (r *restHandler) RegisterPublic(c *gin.Engine) {
	c.Use(middleware.TracingMiddleware())
	c.Use(r.AccessLog())
	c.Use(r.TraceContextMiddleware())
	c.Use(r.LanguageMiddleware())

	c.GET("/api/ontology-query/v1/health", r.HealthCheck)

	apiV1 := c.Group("/api/ontology-query/v1")
	apiV1.Use(rest.PrivateNoCacheMiddleware())
	{
		// Query object data for the specified object type.
		apiV1.GET("/knowledge-networks/:kn_id/object-types/:ot_id/schema", r.GetObjectTypeSchemaByEx)
		apiV1.GET("/knowledge-networks/:kn_id/object-types/:ot_id/sample-data", r.GetObjectTypeSampleDataByEx)
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
		apiV1.GET("/knowledge-networks/:kn_id/action-logs/:log_id/results", r.QueryActionLogResultsByEx)
		apiV1.POST("/knowledge-networks/:kn_id/action-logs/:log_id/cancel", r.CancelActionLogByEx)

		apiV1.POST("/knowledge-networks/:kn_id/metrics/dry-run", r.verifyJsonContentType(), r.PostMetricDryRunByEx)
		apiV1.POST("/knowledge-networks/:kn_id/metrics/:metric_id/data", r.verifyJsonContentType(), r.PostMetricDataByEx)
	}

	apiInV1 := c.Group("/api/ontology-query/in/v1")
	apiInV1.Use(rest.PrivateNoCacheMiddleware())
	{
		// Published-model capability for bkn-safe's row-filter policy manager.
		// It is internal-only and returns no data values or backend expressions.
		apiInV1.POST("/row-filter-capabilities", r.verifyJsonContentType(), r.GetRowFilterCapabilities)
		// Knowledge networks.
		apiInV1.GET("/knowledge-networks/:kn_id/object-types/:ot_id/schema", r.GetObjectTypeSchemaByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/object-types/:ot_id/sample-data", r.GetObjectTypeSampleDataByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/object-types/:ot_id", r.verifyJsonContentType(), r.GetObjectsInObjectTypeByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/object-types/:ot_id/properties", r.verifyJsonContentType(), r.GetObjectsPropertiesByIn)
		// Get an object subgraph by start point, direction, and path length.
		apiInV1.POST("/knowledge-networks/:kn_id/subgraph", r.verifyJsonContentType(), r.GetObjectsSubgraphByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/subgraph/objects", r.verifyJsonContentType(), r.GetObjectsSubgraphByObjectsByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/action-types/:at_id", r.verifyJsonContentType(), r.GetActionsInActionTypeByIn)

		// Action execution APIs (internal).
		apiInV1.POST("/knowledge-networks/:kn_id/action-types/:at_id/execute/check", r.verifyJsonContentType(), r.CheckActionExecutionByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/action-types/:at_id/execute", r.verifyJsonContentType(), r.ExecuteActionByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-executions/:execution_id", r.GetActionExecutionByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-logs", r.QueryActionLogsByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-logs/:log_id", r.GetActionLogByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-logs/:log_id/results", r.QueryActionLogResultsByIn)
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
		ctx, span := otel.Tracer("github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/http").Start(
			c.Request.Context(), "HTTP request", trace.WithSpanKind(trace.SpanKindServer),
		)
		c.Request = c.Request.WithContext(ctx)
		defer func() {
			panicValue := recover()
			if panicValue != nil && !c.Writer.Written() {
				c.Status(http.StatusInternalServerError)
			}
			endTime := time.Now()
			durTime := endTime.Sub(beginTime).Seconds()

			logger.Debugf("access log: url: %s, method: %s, begin_time: %s, end_time: %s, subTime: %f",
				c.Request.URL.Path,
				c.Request.Method,
				beginTime.Format(libCommon.RFC3339Milli),
				endTime.Format(libCommon.RFC3339Milli),
				durTime,
			)
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			span.SetName(c.Request.Method + " " + route)
			span.SetAttributes(operationSpanAttributes(c.Request.Method, route, c.Writer.Status())...)
			if c.Writer.Status() >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(c.Writer.Status()))
			}
			span.End()
			if panicValue != nil {
				panic(panicValue)
			}
		}()
		c.Next()
	}
}

func operationSpanAttributes(method, route string, status int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
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
