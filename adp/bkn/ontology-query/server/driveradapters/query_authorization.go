// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
)

func (r *restHandler) authorizeQuery(c *gin.Context, ctx context.Context,
	check func() error) bool {
	if r == nil || r.qas == nil {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("query authorization service is not configured"))
		return false
	}
	if err := check(); err != nil {
		if httpErr, ok := err.(*rest.HTTPError); ok {
			rest.ReplyError(c, httpErr)
			return false
		}
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails(fmt.Sprintf("query authorization failed: %v", err)))
		return false
	}
	return true
}
