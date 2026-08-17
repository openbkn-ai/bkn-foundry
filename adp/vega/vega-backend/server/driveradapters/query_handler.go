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
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/common/visitor"
	"vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics/query"
)

// RawQueryByEx handles POST /api/vega-backend/v1/resources/query (External)
func (r *restHandler) RawQueryByEx(c *gin.Context) {
	// External network interface: Verify token
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.rawQuery(c, visitor)
}

// RawQueryByIn handles POST /api/vega-backend/in/v1/resources/query (Internal)
func (r *restHandler) RawQueryByIn(c *gin.Context) {
	// Internal network interface: user_id is taken from the header
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

	// query_timeout_sec takes effect only on the first request; When a new page is created using the cursor session
	// The fixed value cannot be rewritten by the client.
	if req.QueryTimeoutSec != 0 && (req.QueryTimeoutSec < 1 || req.QueryTimeoutSec > 3600) {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, errors.VegaBackend_Query_InvalidParameter_QueryTimeout).
			WithErrorDetails(fmt.Sprintf("query_timeout_sec must be between 1 and 3600, got: %d", req.QueryTimeoutSec))
		otellog.LogError(ctx, "Query timeout is invalid", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if !req.IsContinuation() && req.QueryTimeoutSec == 0 {
		req.QueryTimeoutSec = 60
	}

	qs := query.NewRawQueryService(r.appSetting)
	resp, err := qs.Execute(ctx, &req)
	if err != nil {
		var httpErr *rest.HTTPError
		var ok bool
		if httpErr, ok = err.(*rest.HTTPError); !ok {
			// If it is not an HTTPError, it is converted to an internal server error
			httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, errors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails(err.Error())
		}
		otellog.LogError(ctx, "Execute raw query failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	emitRawQueryEvidence(c, ctx, &req, resp)
	rest.ReplyOK(c, http.StatusOK, resp)
}
