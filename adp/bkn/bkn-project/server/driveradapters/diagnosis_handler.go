// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The network diagnosis of a project's draft: diagnosis:run starts it, and a suggestion of the
// latest one is applied or dismissed by {suggestion_id}:apply and {suggestion_id}:dismiss.

const (
	diagnosisActionRun = "diagnosis:run"
	suggestionApply    = "apply"
	suggestionDismiss  = "dismiss"
)

func (r *restHandler) GetDiagnosisByEx(c *gin.Context) { r.withExVisitor(c, r.GetDiagnosis) }

// SuggestionActionByEx serves POST …/diagnosis/suggestions/{suggestion_id}:apply and :dismiss; the
// router reads the id and its method as one parameter.
func (r *restHandler) SuggestionActionByEx(c *gin.Context) {
	at := strings.LastIndex(c.Param("suggestion"), ":")
	if at <= 0 {
		replyNotFound(c)
		return
	}
	switch c.Param("suggestion")[at+1:] {
	case suggestionApply:
		r.withExVisitor(c, r.ApplySuggestion)
	case suggestionDismiss:
		r.withExVisitor(c, r.DismissSuggestion)
	default:
		replyNotFound(c)
	}
}

func suggestionIDOf(c *gin.Context) string {
	param := c.Param("suggestion")
	return param[:strings.LastIndex(param, ":")]
}

// RunDiagnosis starts a diagnosis and replies 202 with its run.
func (r *restHandler) RunDiagnosis(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	run, err := r.dgs.Run(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, run)
}

// GetDiagnosis replies the project's latest diagnosis.
func (r *restHandler) GetDiagnosis(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	diagnosis, err := r.dgs.Get(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, diagnosis)
}

// ApplySuggestion applies a suggestion on the draft read and replies the draft's new update time.
func (r *restHandler) ApplySuggestion(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.DiagnosisApply
	// The body is optional: a concept disposition needs no draft read; the service checks the rest.
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	applied, err := r.dgs.Apply(ctx, visitor.TokenID, id, suggestionIDOf(c), req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, applied)
}

// DismissSuggestion dismisses a suggestion, with an optional reason, and replies it.
func (r *restHandler) DismissSuggestion(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.DiagnosisDismiss
	// The body is optional: no reason is a dismissal too.
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	suggestion, err := r.dgs.Dismiss(ctx, visitor.TokenID, id, suggestionIDOf(c), req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, suggestion)
}
