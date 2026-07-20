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

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"vega-backend/common/visitor"
	"vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics/query"
)

// RawQueryByEx handles POST /api/vega-backend/v1/resources/query (External)
func (r *restHandler) RawQueryByEx(c *gin.Context) {
	// 外网接口：校验token
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.rawQuery(c, visitor)
}

// RawQueryByIn handles POST /api/vega-backend/in/v1/resources/query (Internal)
func (r *restHandler) RawQueryByIn(c *gin.Context) {
	// 内网接口：user_id从header中取
	visitor := visitor.GenerateVisitor(c)
	r.rawQuery(c, visitor)
}

// sqlQuery is the shared implementation for SQL query
func (r *restHandler) rawQuery(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	var req interfaces.RawQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, errors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Bind raw query request failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// 校验query_timeout_sec参数，默认值为60，最大值为3600，最小值为1
	if req.QueryTimeoutSec == 0 {
		req.QueryTimeoutSec = 60 // 设置默认值
	} else if req.QueryTimeoutSec < 1 || req.QueryTimeoutSec > 3600 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, errors.VegaBackend_Query_InvalidParameter_QueryTimeout).
			WithErrorDetails(fmt.Sprintf("query_timeout_sec must be between 1 and 3600, got: %d", req.QueryTimeoutSec))
		otellog.LogError(ctx, "Query timeout is invalid", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	qs := query.NewRawQueryService(r.appSetting)
	resp, err := qs.Execute(ctx, &req)
	if err != nil {
		var httpErr *rest.HTTPError
		var ok bool
		if httpErr, ok = err.(*rest.HTTPError); !ok {
			// 如果不是HTTPError，则转换为内部服务器错误
			httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, errors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails(err.Error())
		}
		otellog.LogError(ctx, "Execute raw query failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, resp)
}
