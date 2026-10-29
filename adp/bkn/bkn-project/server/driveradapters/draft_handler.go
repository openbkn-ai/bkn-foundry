// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ontology drafts of a project. They follow the project's visibility; drafting reads
// vega-backend as the caller.

const (
	draftActionGenerate = "draft:generate"
	draftActionRestore  = "draft:restore"
	draftActionConfirm  = "draft:confirm"
	checkActionRun      = "check:run"
)

func (r *restHandler) GetDraftByEx(c *gin.Context)       { r.withExVisitor(c, r.GetDraft) }
func (r *restHandler) GetCheckByEx(c *gin.Context)       { r.withExVisitor(c, r.GetCheck) }
func (r *restHandler) GetIndexByEx(c *gin.Context)       { r.withExVisitor(c, r.GetIndex) }
func (r *restHandler) GetStatusByEx(c *gin.Context)      { r.withExVisitor(c, r.GetStatus) }
func (r *restHandler) GetPendingByEx(c *gin.Context)     { r.withExVisitor(c, r.GetPending) }
func (r *restHandler) EditDraftItemByEx(c *gin.Context)  { r.withExVisitor(c, r.EditDraftItem) }
func (r *restHandler) ChooseIdentityByEx(c *gin.Context) { r.withExVisitor(c, r.ChooseIdentity) }

// generateDraftRequest is the optional body of draft:generate.
type generateDraftRequest struct {
	// DiscardDecisions agrees to discard the decisions people made on the current draft.
	DiscardDecisions bool `json:"discard_decisions"`
}

// OntologyActionByEx serves POST /projects/{project_id}/ontology/draft:generate,
// draft:restore, draft:confirm and check:run.
func (r *restHandler) OntologyActionByEx(c *gin.Context) {
	switch c.Param("action") {
	case draftActionGenerate:
		r.withExVisitor(c, r.GenerateDraft)
	case draftActionRestore:
		r.withExVisitor(c, r.RestoreExcluded)
	case draftActionConfirm:
		r.withExVisitor(c, r.ConfirmDraft)
	case checkActionRun:
		r.withExVisitor(c, r.RunCheck)
	default:
		replyNotFound(c)
	}
}

func (r *restHandler) GenerateDraft(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req generateDraftRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
			return
		}
	}

	draft, err := r.drs.GenerateDraft(ctx, visitor.TokenID, id, visitorAccount(visitor), req.DiscardDecisions)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, draft)
}

func (r *restHandler) ConfirmDraft(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var confirm interfaces.DraftConfirm
	if err := c.ShouldBindJSON(&confirm); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	draft, err := r.drs.ConfirmDraft(ctx, visitor.TokenID, id, confirm, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}

func (r *restHandler) GetDraft(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	draft, err := r.drs.GetDraft(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}

// GetStatus replies where the project stands for the caller: its stage, what blocks it and who
// that waits for, what is out of date, and the requests the caller can send next.
func (r *restHandler) GetStatus(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	status, err := r.drs.GetStatus(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, status)
}

// GetPending replies what waits for a person before the draft can be confirmed, each item with the
// requests that settle it.
func (r *restHandler) GetPending(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	pending, err := r.drs.GetPending(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, pending)
}

// GetCheck replies the check of the project's last confirmation.
func (r *restHandler) GetCheck(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	check, err := r.drs.GetCheck(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, check)
}

// GetIndex replies the readiness of the local indexes of the draft's adopted object types.
func (r *restHandler) GetIndex(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	index, err := r.drs.GetIndex(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, index)
}

// RunCheck checks the last confirmation again in the background and replies the check, running.
func (r *restHandler) RunCheck(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	check, err := r.drs.RunCheck(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, check)
}

func (r *restHandler) EditDraftItem(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, kind, itemID := c.Param("project_id"), c.Param("kind"), c.Param("item_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("kind").String(kind),
		attr.Key("item_id").String(itemID))

	var edit interfaces.DraftItemEdit
	if err := c.ShouldBindJSON(&edit); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	draft, err := r.drs.EditDraftItem(ctx, visitor.TokenID, id, kind, itemID, edit, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}

func (r *restHandler) ChooseIdentity(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, itemID := c.Param("project_id"), c.Param("item_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("item_id").String(itemID))

	var identity interfaces.DraftIdentity
	if err := c.ShouldBindJSON(&identity); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	draft, err := r.drs.ChooseIdentity(ctx, visitor.TokenID, id, itemID, identity, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}

func (r *restHandler) RestoreExcluded(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var restore interfaces.DraftRestore
	if err := c.ShouldBindJSON(&restore); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	draft, err := r.drs.RestoreExcluded(ctx, visitor.TokenID, id, restore, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}
