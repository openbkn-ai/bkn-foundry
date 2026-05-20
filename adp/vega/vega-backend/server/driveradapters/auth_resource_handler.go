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
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/kweaver-ai/kweaver-go-lib/otel/otellog"
	"github.com/kweaver-ai/kweaver-go-lib/otel/oteltrace"
	"github.com/kweaver-ai/kweaver-go-lib/rest"
	"go.opentelemetry.io/otel/trace"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// ListAuthResources handles GET /api/vega-backend/v1/auth-resources.
func (r *restHandler) ListAuthResources(c *gin.Context) {
	logger.Debug("ListAuthResources Start")

	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	resourceType := strings.TrimSpace(c.Query("resource_type"))
	switch resourceType {
	case interfaces.AUTH_RESOURCE_TYPE_CATALOG:
		r.listCatalogAuthResources(ctx, span, c)
	case interfaces.AUTH_RESOURCE_TYPE_RESOURCE:
		r.listResourceAuthResources(ctx, span, c)
	case interfaces.AuthResourceTypeConnectorType:
		r.listConnectorTypeAuthResources(ctx, span, c)
	default:
		err := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("resource_type is invalid; valid values: %s", strings.Join(interfaces.AuthResourceTypes(), ", ")))
		rest.ReplyError(c, err)
		return
	}
}

func (r *restHandler) listCatalogAuthResources(ctx context.Context, span trace.Span, c *gin.Context) {
	query, ok := parseAuthResourceQuery(ctx, span, c)
	if !ok {
		return
	}

	entries, total, err := r.cs.ListAuthResources(ctx, query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler ListAuthResources Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{
		"entries":     entries,
		"total_count": total,
	})
}

func (r *restHandler) listResourceAuthResources(ctx context.Context, span trace.Span, c *gin.Context) {
	query, ok := parseAuthResourceQuery(ctx, span, c)
	if !ok {
		return
	}

	entries, total, err := r.rs.ListAuthResources(ctx, query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler ListAuthResources Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{
		"entries":     entries,
		"total_count": total,
	})
}

func (r *restHandler) listConnectorTypeAuthResources(ctx context.Context, span trace.Span, c *gin.Context) {
	query, ok := parseAuthResourceQuery(ctx, span, c)
	if !ok {
		return
	}

	entries, total, err := r.cts.ListAuthResources(ctx, query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler ListConnectorTypeAuthResources Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{
		"entries":     entries,
		"total_count": total,
	})
}

func parseAuthResourceQuery(ctx context.Context, span trace.Span, c *gin.Context) (interfaces.AuthResourceQueryParams, bool) {
	offset := common.GetQueryOrDefault(c, "offset", interfaces.DEFAULT_OFFSET)
	limit := common.GetQueryOrDefault(c, "limit", "50")
	sort := common.GetQueryOrDefault(c, "sort", "name")
	direction := common.GetQueryOrDefault(c, "direction", interfaces.DESC_DIRECTION)

	pageParam, err := validatePaginationQueryParams(ctx, offset, limit, sort, direction, interfaces.AuthResourceSort)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return interfaces.AuthResourceQueryParams{}, false
	}

	return interfaces.AuthResourceQueryParams{
		PaginationQueryParams: pageParam,
		ID:                    c.Query("id"),
		Keyword:               strings.TrimSpace(c.Query("keyword")),
	}, true
}
