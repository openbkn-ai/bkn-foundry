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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The kinetic needs of a project's specification, with their implementations in its live network.

func (r *restHandler) ListLogicNeedsByEx(c *gin.Context) { r.withExVisitor(c, r.ListLogicNeeds) }
func (r *restHandler) LogicNeedCandidatesByEx(c *gin.Context) {
	r.withExVisitor(c, r.LogicNeedCandidates)
}
func (r *restHandler) LinkLogicNeedByEx(c *gin.Context)   { r.withExVisitor(c, r.LinkLogicNeed) }
func (r *restHandler) UnlinkLogicNeedByEx(c *gin.Context) { r.withExVisitor(c, r.UnlinkLogicNeed) }

// ListLogicNeeds serves GET /projects/{project_id}/requirements/logic-needs.
func (r *restHandler) ListLogicNeeds(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))
	view, err := r.rqs.ListLogicNeeds(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, view)
}

// LogicNeedCandidates serves GET /projects/{project_id}/requirements/logic-needs/{need_id}/candidates.
func (r *restHandler) LogicNeedCandidates(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, needID := c.Param("project_id"), c.Param("need_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("need_id").String(needID))
	candidates, err := r.rqs.LogicNeedCandidates(ctx, visitor.TokenID, id, needID)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, candidates)
}

// LinkLogicNeed serves PUT /projects/{project_id}/requirements/logic-needs/{need_id}/link.
func (r *restHandler) LinkLogicNeed(c *gin.Context, visitor hydra.Visitor) {
	replySpec(c, http.StatusOK, func(ctx context.Context, link interfaces.RequirementLogicLinkEdit) (*interfaces.Requirement, error) {
		return r.rqs.LinkLogicNeed(ctx, visitor.TokenID, c.Param("project_id"), c.Param("need_id"), link,
			visitorAccount(visitor))
	})
}

// UnlinkLogicNeed serves DELETE /projects/{project_id}/requirements/logic-needs/{need_id}/link?update_time=.
func (r *restHandler) UnlinkLogicNeed(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))
	updateTime, err := queryUpdateTime(c)
	if err != nil {
		replyError(c, span, err)
		return
	}
	spec, err := r.rqs.UnlinkLogicNeed(ctx, visitor.TokenID, id, c.Param("need_id"), updateTime, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, spec)
}
