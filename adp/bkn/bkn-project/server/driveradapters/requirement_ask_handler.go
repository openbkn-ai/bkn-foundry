// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The supplement note and the Ask AI conversations of the kinetic modeling and business scenarios
// pages. They follow the project's visibility; only its owner writes them.

func (r *restHandler) GetRequirementSupplementByEx(c *gin.Context) {
	r.withExVisitor(c, r.GetRequirementSupplement)
}
func (r *restHandler) SetRequirementSupplementByEx(c *gin.Context) {
	r.withExVisitor(c, r.SetRequirementSupplement)
}
func (r *restHandler) GetRequirementKnowledgeByEx(c *gin.Context) {
	r.withExVisitor(c, r.GetRequirementKnowledge)
}
func (r *restHandler) ListRequirementConversationsByEx(c *gin.Context) {
	r.withExVisitor(c, r.ListRequirementConversations)
}
func (r *restHandler) GetRequirementConversationByEx(c *gin.Context) {
	r.withExVisitor(c, r.GetRequirementConversation)
}
func (r *restHandler) StartRequirementConversationByEx(c *gin.Context) {
	r.withExVisitor(c, r.StartRequirementConversation)
}
func (r *restHandler) AskRequirementConversationByEx(c *gin.Context) {
	r.withExVisitor(c, r.AskRequirementConversation)
}
func (r *restHandler) DeleteRequirementConversationByEx(c *gin.Context) {
	r.withExVisitor(c, r.DeleteRequirementConversation)
}
func (r *restHandler) DecideRequirementProposalsByEx(c *gin.Context) {
	r.withExVisitor(c, r.DecideRequirementProposals)
}

// serveRequirement runs a read or change of the project, with the body when T is not struct{},
// and replies what it returns with status.
func serveRequirement[T any, R any](c *gin.Context, status int, withBody bool,
	serve func(ctx context.Context, span trace.Span, body T) (R, error)) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))
	span.SetAttributes(attr.Key("project_id").String(c.Param("project_id")))

	var body T
	if withBody {
		if err := c.ShouldBindJSON(&body); err != nil {
			replyError(c, span, invalidRequirementBody(c))
			return
		}
	}
	reply, err := serve(ctx, span, body)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, status)
	rest.ReplyOK(c, status, reply)
}

// nameSupplement names who wrote the supplement note last.
func (r *restHandler) nameSupplement(ctx context.Context, supplement *interfaces.RequirementSupplement) {
	if supplement == nil || supplement.By == nil || r.ana == nil {
		return
	}
	account := interfaces.AccountInfo{ID: supplement.By.ID, Type: supplement.By.Type}
	supplement.By.Name = r.ana.Names(ctx, []interfaces.AccountInfo{account})[account]
}

// GetRequirementSupplement serves GET /projects/{project_id}/requirements/supplement.
func (r *restHandler) GetRequirementSupplement(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusOK, false, func(ctx context.Context, _ trace.Span, _ struct{}) (*interfaces.RequirementSupplement, error) {
		supplement, err := r.rqs.GetSupplement(ctx, visitor.TokenID, c.Param("project_id"))
		r.nameSupplement(ctx, supplement)
		return supplement, err
	})
}

// GetRequirementKnowledge serves GET /projects/{project_id}/requirements/knowledge.
func (r *restHandler) GetRequirementKnowledge(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusOK, false, func(ctx context.Context, _ trace.Span, _ struct{}) (*interfaces.RequirementKnowledge, error) {
		return r.rqs.GetKnowledge(ctx, visitor.TokenID, c.Param("project_id"))
	})
}

// SetRequirementSupplement serves PUT /projects/{project_id}/requirements/supplement.
func (r *restHandler) SetRequirementSupplement(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusOK, true, func(ctx context.Context, _ trace.Span,
		edit interfaces.RequirementSupplementEdit) (*interfaces.RequirementSupplement, error) {
		supplement, err := r.rqs.SetSupplement(ctx, visitor.TokenID, c.Param("project_id"), edit, visitorAccount(visitor))
		r.nameSupplement(ctx, supplement)
		return supplement, err
	})
}

// ListRequirementConversations serves GET /projects/{project_id}/requirements/conversations?page=.
func (r *restHandler) ListRequirementConversations(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusOK, false, func(ctx context.Context, _ trace.Span, _ struct{}) (gin.H, error) {
		list, err := r.rqs.ListConversations(ctx, visitor.TokenID, c.Param("project_id"), c.Query("page"))
		return gin.H{"conversations": list}, err
	})
}

// GetRequirementConversation serves GET /projects/{project_id}/requirements/conversations/{conversation_id}.
func (r *restHandler) GetRequirementConversation(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusOK, false, func(ctx context.Context, _ trace.Span, _ struct{}) (*interfaces.RequirementConversation, error) {
		return r.rqs.GetConversation(ctx, visitor.TokenID, c.Param("project_id"), c.Param("conversation_id"))
	})
}

// StartRequirementConversation serves POST /projects/{project_id}/requirements/conversations: a
// new conversation of a page with its first question, answered in the background; it replies 202.
func (r *restHandler) StartRequirementConversation(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusAccepted, true, func(ctx context.Context, _ trace.Span,
		req interfaces.RequirementAskRequest) (*interfaces.RequirementConversation, error) {
		return r.rqs.Ask(ctx, visitor.TokenID, c.Param("project_id"), "", req, visitorAccount(visitor))
	})
}

// AskRequirementConversation serves POST /projects/{project_id}/requirements/conversations/{conversation_id}/turns:
// another question of the conversation, answered in the background; it replies 202.
func (r *restHandler) AskRequirementConversation(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusAccepted, true, func(ctx context.Context, _ trace.Span,
		req interfaces.RequirementAskRequest) (*interfaces.RequirementConversation, error) {
		return r.rqs.Ask(ctx, visitor.TokenID, c.Param("project_id"), c.Param("conversation_id"), req, visitorAccount(visitor))
	})
}

// DeleteRequirementConversation serves DELETE /projects/{project_id}/requirements/conversations/{conversation_id}.
func (r *restHandler) DeleteRequirementConversation(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))
	span.SetAttributes(attr.Key("project_id").String(c.Param("project_id")))

	if err := r.rqs.DeleteConversation(ctx, visitor.TokenID, c.Param("project_id"), c.Param("conversation_id"),
		visitorAccount(visitor)); err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	c.Status(http.StatusNoContent)
}

// DecideRequirementProposals serves PUT
// /projects/{project_id}/requirements/conversations/{conversation_id}/turns/{turn_id}/proposals.
func (r *restHandler) DecideRequirementProposals(c *gin.Context, visitor hydra.Visitor) {
	serveRequirement(c, http.StatusOK, true, func(ctx context.Context, _ trace.Span,
		decision interfaces.RequirementProposalsDecision) (*interfaces.RequirementProposalsResult, error) {
		return r.rqs.DecideProposals(ctx, visitor.TokenID, c.Param("project_id"), c.Param("conversation_id"),
			c.Param("turn_id"), decision, visitorAccount(visitor))
	})
}
