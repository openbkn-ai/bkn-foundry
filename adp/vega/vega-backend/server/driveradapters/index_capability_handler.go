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
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// GetIndexCapabilitiesByEx handles GET /api/vega-backend/v1/index-capabilities.
func (r *restHandler) GetIndexCapabilitiesByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	capabilities, err := r.lim.GetIndexCapabilities(ctx)
	if err != nil {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			verrors.VegaBackend_IndexCapability_InternalError_Unavailable).WithErrorDetails(err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, capabilities)
}
