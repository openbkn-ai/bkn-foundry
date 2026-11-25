// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/visitor"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func (r *restHandler) RetryActionIndexSyncByEx(c *gin.Context) {
	ctx := c.Request.Context()
	caller, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.retryActionIndexSync(c, caller)
}

func (r *restHandler) RetryActionIndexSyncByIn(c *gin.Context) {
	r.retryActionIndexSync(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) retryActionIndexSync(c *gin.Context, caller hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: caller.ID, Type: string(caller.Type)})
	knID := c.Param("kn_id")
	if !r.authorizeActionLogRead(ctx, c, span, knID) {
		return
	}
	count, err := r.ass.RetryActionIndexSync(ctx, knID, c.Param("execution_id"))
	if err != nil {
		httpErr, ok := err.(*httperrors.HTTPError)
		if !ok {
			httpErr = httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError)
		}
		rest.ReplyError(c, httpErr)
		return
	}
	rest.ReplyOK(c, http.StatusAccepted, map[string]any{"retried_results": count})
}
