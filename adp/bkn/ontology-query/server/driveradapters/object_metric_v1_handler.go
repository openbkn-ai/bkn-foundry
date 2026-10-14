// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/visitor"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/auth"
	queryauthorization "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/query_authorization"
)

// RegisterObjectMetricRoutes mounts the public and internal object-metric
// query contracts around an implementation supplied by the paid image.
func RegisterObjectMetricRoutes(
	engine *gin.Engine,
	appSetting *common.AppSetting,
	service interfaces.ObjectMetricQueryServiceV1,
	entitlementMiddleware gin.HandlerFunc,
) {
	handler := &restHandler{
		appSetting: appSetting,
		as:         auth.NewAuthService(appSetting),
		oms:        service,
		qas:        queryauthorization.NewQueryAuthorizationService(appSetting),
	}
	public := engine.Group("/api/ontology-query/v2")
	internal := engine.Group("/api/ontology-query/in/v2")
	public.Use(rest.PrivateNoCacheMiddleware())
	internal.Use(rest.PrivateNoCacheMiddleware())
	if entitlementMiddleware != nil {
		public.Use(entitlementMiddleware)
		internal.Use(entitlementMiddleware)
	}
	public.POST("/knowledge-networks/:kn_id/object-metrics/trial", handler.verifyJsonContentType(), handler.PostObjectMetricTrialV1ByEx)
	public.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/data", handler.verifyJsonContentType(), handler.PostObjectMetricDataV1ByEx)
	internal.POST("/knowledge-networks/:kn_id/object-metrics/trial", handler.verifyJsonContentType(), handler.PostObjectMetricTrialV1ByIn)
	internal.POST("/knowledge-networks/:kn_id/object-metrics/:metric_id/data", handler.verifyJsonContentType(), handler.PostObjectMetricDataV1ByIn)
}

func (r *restHandler) PostObjectMetricDataV1ByEx(c *gin.Context) {
	ctx := rest.GetLanguageCtx(c)
	visitorInfo, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.postObjectMetricDataV1(c, visitorInfo)
}

func (r *restHandler) PostObjectMetricDataV1ByIn(c *gin.Context) {
	r.postObjectMetricDataV1(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) PostObjectMetricTrialV1ByEx(c *gin.Context) {
	ctx := rest.GetLanguageCtx(c)
	visitorInfo, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.postObjectMetricTrialV1(c, visitorInfo)
}

func (r *restHandler) PostObjectMetricTrialV1ByIn(c *gin.Context) {
	r.postObjectMetricTrialV1(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) postObjectMetricTrialV1(c *gin.Context, visitorInfo hydra.Visitor) {
	ctx := rest.GetLanguageCtx(c)
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: visitorInfo.ID, Type: string(visitorInfo.Type)})
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	var body interfaces.ObjectMetricTrialRequestV1
	if err := c.ShouldBindJSON(&body); err != nil {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_Metric_InvalidParameter).
			WithErrorDetails("bind json: "+err.Error()))
		return
	}
	if !r.authorizeQuery(c, ctx, func() error {
		return r.qas.AuthorizeObjectMetricTrial(ctx, knID, branch, &body.Definition)
	}) {
		return
	}
	result, err := r.oms.TrialObjectMetricDataV1(ctx, knID, branch, &body)
	if err != nil {
		if httpErr, ok := err.(*rest.HTTPError); ok {
			rest.ReplyError(c, httpErr)
			return
		}
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_Metric_InternalError_QueryFailed).
			WithErrorDetails(err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) postObjectMetricDataV1(c *gin.Context, visitorInfo hydra.Visitor) {
	ctx := rest.GetLanguageCtx(c)
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: visitorInfo.ID, Type: string(visitorInfo.Type)})
	knID := c.Param("kn_id")
	metricID := c.Param("metric_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	version, err := strconv.Atoi(c.Query("version"))
	if err != nil || version < 1 {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_Metric_InvalidParameter).
			WithErrorDetails("version must identify an immutable published metric version"))
		return
	}
	var body interfaces.ObjectMetricQueryRequestV1
	if err := c.ShouldBindJSON(&body); err != nil {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_Metric_InvalidParameter).
			WithErrorDetails("bind json: "+err.Error()))
		return
	}
	if !r.authorizeQuery(c, ctx, func() error {
		return r.qas.AuthorizeObjectMetricQuery(ctx, knID, branch, metricID)
	}) {
		return
	}
	result, err := r.oms.QueryObjectMetricDataV1(ctx, knID, branch, metricID, version, &body)
	if err != nil {
		if httpErr, ok := err.(*rest.HTTPError); ok {
			rest.ReplyError(c, httpErr)
			return
		}
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_Metric_InternalError_QueryFailed).
			WithErrorDetails(err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}
