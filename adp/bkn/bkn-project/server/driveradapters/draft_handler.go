// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
)

// Ontology drafts of a project. They follow the project's visibility; drafting reads
// vega-backend as the caller.

const draftActionGenerate = "draft:generate"

func (r *restHandler) GetDraftByEx(c *gin.Context) { r.withExVisitor(c, r.GetDraft) }

// OntologyActionByEx serves POST /projects/{project_id}/ontology/draft:generate.
func (r *restHandler) OntologyActionByEx(c *gin.Context) {
	if c.Param("action") != draftActionGenerate {
		replyNotFound(c)
		return
	}
	r.withExVisitor(c, r.GenerateDraft)
}

func (r *restHandler) GenerateDraft(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	draft, err := r.drs.GenerateDraft(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, draft)
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
