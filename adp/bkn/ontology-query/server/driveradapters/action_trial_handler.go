// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/visitor"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// TrialActionByIn tries an action type definition as the account in the headers (internal).
func (r *restHandler) TrialActionByIn(c *gin.Context) {
	logger.Debug("Handler TrialActionByIn Start")
	r.TrialAction(c, visitor.GenerateVisitor(c))
}

// TrialActionByEx tries an action type definition as the bearer of the token (external).
func (r *restHandler) TrialActionByEx(c *gin.Context) {
	logger.Debug("Handler TrialActionByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.TrialAction(c, visitor)
}

// TrialAction selects what an action type definition, saved or not, would act on now, without
// recording or executing anything. Trying needs what saving needs: the network's modify
// permission. Only record-event actions can be tried in this version.
func (r *restHandler) TrialAction(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)})
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("branch").String(branch))
	reply := func(err error) {
		var httpErr *httperrors.HTTPError
		if !errors.As(err, &httpErr) {
			httpErr = httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError).
				WithErrorDetails(err.Error())
		}
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
	}

	if r.actionReadPermissions == nil {
		reply(httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("action permission service is not configured"))
		return
	}
	if err := r.actionReadPermissions.RequirePermissions(ctx, []interfaces.PermissionRequirement{{
		ResourceType: interfaces.PermissionResourceTypeKnowledgeNetwork,
		ResourceID:   knID,
		Operation:    interfaces.PermissionOperationModify,
	}}); err != nil {
		var httpErr *httperrors.HTTPError
		if !errors.As(err, &httpErr) {
			err = httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
				WithErrorDetails(err.Error())
		}
		reply(err)
		return
	}

	action := interfaces.ActionType{}
	if err := common.BindJSON(c.Request.Body, &action); err != nil {
		reply(httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Parameter Failed: %s", err.Error())))
		return
	}
	// What the platform keeps on a saved action type has no part in a trial.
	action.ExecutionSubject, action.RuleVersion = nil, ""

	trials, ok := r.ass.(interfaces.ActionTrialService)
	if !ok {
		reply(httperrors.NewHTTPError(ctx, http.StatusNotImplemented, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails("action trials are not available"))
		return
	}
	result, err := trials.TrialAction(ctx, knID, branch, &action)
	if err != nil {
		reply(err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}
