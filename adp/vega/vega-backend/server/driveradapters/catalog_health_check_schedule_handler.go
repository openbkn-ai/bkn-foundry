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
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
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

	if !r.requirePhysicalCatalog(ctx, c, catalogID) {
		return
	}

	schedule, err := r.hcss.GetByCatalogID(ctx, catalogID)
	if err != nil {
		replyCatalogHealthCheckScheduleError(ctx, c, err, http.StatusNotFound)
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

	schedule, err := r.hcss.Update(ctx, catalogID, &req)
	if err != nil {
		replyCatalogHealthCheckScheduleError(ctx, c, err, http.StatusInternalServerError)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, schedule)
}

func (r *restHandler) requirePhysicalCatalog(ctx context.Context, c *gin.Context, catalogID string) bool {
	catalog, err := r.cs.GetByID(ctx, catalogID, false)
	if err != nil {
		replyCatalogHealthCheckScheduleError(ctx, c, err, http.StatusInternalServerError)
		return false
	}

	if catalog.Type != interfaces.CatalogTypePhysical {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter).
			WithErrorDetails("health check schedules are only supported for physical catalogs")
		rest.ReplyError(c, httpErr)
		return false
	}

	return true
}

func replyCatalogHealthCheckScheduleError(ctx context.Context, c *gin.Context, err error, fallbackStatus int) {
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		httpErr = rest.NewHTTPError(ctx, fallbackStatus,
			verrors.VegaBackend_Catalog_InternalError).WithErrorDetails(err.Error())
	}

	rest.ReplyError(c, httpErr)
}
