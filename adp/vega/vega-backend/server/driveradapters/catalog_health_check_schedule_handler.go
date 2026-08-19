// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"vega-backend/common/visitor"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

func (r *restHandler) GetCatalogHealthCheckScheduleByEx(c *gin.Context) {
	v, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err == nil {
		r.getCatalogHealthCheckSchedule(c, v)
	}
}

func (r *restHandler) GetCatalogHealthCheckScheduleByIn(c *gin.Context) {
	r.getCatalogHealthCheckSchedule(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) getCatalogHealthCheckSchedule(c *gin.Context, v hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{
			ID:   v.ID,
			Type: string(v.Type),
		})
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	catalogID := c.Param("id")
	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	catalog, err := r.cs.GetByID(ctx, catalogID, false)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err,
			verrors.VegaBackend_CatalogHealthCheckSchedule_InternalError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if catalog.Type != interfaces.CatalogTypePhysical {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_CatalogHealthCheckSchedule_InvalidParameter).
			WithErrorDetails("health check schedules are only supported for physical catalogs")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	schedule, err := r.hcss.GetByCatalogID(ctx, catalogID)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err,
			verrors.VegaBackend_CatalogHealthCheckSchedule_InternalError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, schedule)
}

func (r *restHandler) UpdateCatalogHealthCheckScheduleByEx(c *gin.Context) {
	v, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err == nil {
		r.updateCatalogHealthCheckSchedule(c, v)
	}
}

func (r *restHandler) UpdateCatalogHealthCheckScheduleByIn(c *gin.Context) {
	r.updateCatalogHealthCheckSchedule(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) updateCatalogHealthCheckSchedule(c *gin.Context, v hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{
			ID:   v.ID,
			Type: string(v.Type),
		})
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	catalogID := c.Param("id")
	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	var req interfaces.CatalogHealthCheckScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if err := validateExpectedUpdateTime(ctx, req.ExpectedUpdateTime); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	schedule, err := r.hcss.Update(ctx, catalogID, &req)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err,
			verrors.VegaBackend_CatalogHealthCheckSchedule_InternalError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, schedule)
}
