// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package driveradapters provides HTTP handlers (primary adapters).
package driveradapters

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// ListAuthorizationResourcesByIn 处理 bkn-safe 权限配置使用的内部资源目录。
// 它不做认证或调用方业务资源授权过滤，由 /in/v1 的网络边界控制访问。
func (r *restHandler) ListAuthorizationResourcesByIn(c *gin.Context) {
	logger.Debug("ListAuthorizationResourcesByIn Start")

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	resourceType := strings.TrimSpace(c.Query("resource_type"))
	query, err := parseInternalAuthorizationResourceQuery(ctx, c)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_Resource_InternalError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	var (
		entries []*interfaces.AuthResourceEntry
		total   int64
	)
	switch resourceType {
	case interfaces.AUTH_RESOURCE_TYPE_CATALOG:
		entries, total, err = r.cs.ListAuthResourceEntries(ctx, query)
	case interfaces.AUTH_RESOURCE_TYPE_RESOURCE:
		entries, total, err = r.rs.ListAuthResourceEntries(ctx, query)
	case interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE:
		entries, total, err = r.cts.ListAuthResourceEntries(ctx, query)
	default:
		err = rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("resource_type is invalid; valid values: catalog, resource, connector_type")
	}
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_Resource_InternalError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if entries == nil {
		entries = []*interfaces.AuthResourceEntry{}
	}

	logger.Debug("ListAuthorizationResourcesByIn Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
	})
}

func parseInternalAuthorizationResourceQuery(ctx context.Context, c *gin.Context) (interfaces.AuthResourceQueryParams, error) {
	offset := common.GetQueryOrDefault(c, "offset", interfaces.DEFAULT_OFFSET)
	limit := common.GetQueryOrDefault(c, "limit", interfaces.DEFAULT_LIMIT)
	sort := common.GetQueryOrDefault(c, "sort", interfaces.AuthResourceSortName)
	direction := common.GetQueryOrDefault(c, "direction", interfaces.ASC_DIRECTION)

	pageParam, err := validatePaginationQueryParams(ctx, offset, limit, sort, direction, interfaces.AuthResourceSort)
	if err != nil {
		return interfaces.AuthResourceQueryParams{}, err
	}
	return interfaces.AuthResourceQueryParams{
		PaginationQueryParams: pageParam,
		Name:                  strings.TrimSpace(c.Query("name")),
	}, nil
}
