// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"

	"errors"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// InspectActionExecutionByEx only evaluates admission; it cannot enqueue a task.
func (r *restHandler) InspectActionExecutionByEx(c *gin.Context) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)})
	ctx = interfaces.WithCallerRuntimeCredential(ctx, actionCallerRuntimeCredential(c, visitor))
	var request interfaces.ActionExecutionRequest
	if err := common.BindPreciseJSON(c.Request.Body, &request); err != nil {
		rest.ReplyError(c, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).WithErrorDetails("Action inspection input must be a JSON object"))
		return
	}
	request.KNID, request.ActionTypeID, request.Branch = c.Param("kn_id"), c.Param("at_id"), c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	inspector, ok := r.ass.(interfaces.ActionExecutionInspector)
	if !ok {
		rest.ReplyError(c, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_ActionExecution_GetActionTypeFailed))
		return
	}
	result, err := inspector.InspectActionExecution(ctx, &request)
	if err != nil {
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			rest.ReplyError(c, httpErr)
			return
		}
		rest.ReplyError(c, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_GetActionTypeFailed))
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}
