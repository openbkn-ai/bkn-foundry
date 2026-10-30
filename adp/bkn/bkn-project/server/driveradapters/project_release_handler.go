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

// A project's releases: reviewing and publishing one, bringing the live version into the branch,
// and switching consumers to a release. Syncing and committing drift are custom methods,
// branch:sync and drift:commit, that the project's action route hands here.

const (
	projectActionSync   = "branch:sync"
	driftActionCommit   = "drift:commit"
	regressionActionRun = "regression:run"
)

type projectSwitchRequest struct {
	ToNetworkID string `json:"to_network_id"`
}

func (r *restHandler) ReviewReleaseByEx(c *gin.Context)  { r.withExVisitor(c, r.ReviewRelease) }
func (r *restHandler) PublishProjectByEx(c *gin.Context) { r.withExVisitor(c, r.PublishProject) }
func (r *restHandler) PlanProjectSwitchByEx(c *gin.Context) {
	r.withExVisitor(c, r.PlanProjectSwitch)
}
func (r *restHandler) SwitchProjectByEx(c *gin.Context) { r.withExVisitor(c, r.SwitchProject) }
func (r *restHandler) GetRegressionByEx(c *gin.Context) { r.withExVisitor(c, r.GetRegression) }

// ReleaseActionByEx serves the custom methods on a release of the project: regression:run.
func (r *restHandler) ReleaseActionByEx(c *gin.Context) {
	switch c.Param("action") {
	case regressionActionRun:
		r.withExVisitor(c, r.RunRegression)
	default:
		replyNotFound(c)
	}
}

// ReviewRelease serves GET /projects/{id}/release-review?version=X.Y.Z.
func (r *restHandler) ReviewRelease(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	review, err := r.prs.ReviewRelease(ctx, visitor.TokenID, id, c.Query("version"), visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, review)
}

// PublishProject serves POST /projects/{id}/releases with {"version", "notes", "signature", "accepts"}.
func (r *restHandler) PublishProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.ProjectPublish
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidRequestBody))
		return
	}
	result, err := r.prs.PublishRelease(ctx, visitor.TokenID, id, req, visitorAccount(visitor), gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// SyncProjectBranch serves POST /projects/{id}/branch:sync.
func (r *restHandler) SyncProjectBranch(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	result, err := r.prs.SyncBranch(ctx, visitor.TokenID, id, visitorAccount(visitor), gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// CommitProjectDrift serves POST /projects/{id}/drift:commit.
func (r *restHandler) CommitProjectDrift(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	result, err := r.prs.CommitDrift(ctx, visitor.TokenID, id, visitorAccount(visitor), gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// PlanProjectSwitch serves GET /projects/{id}/switch-plan?to_network_id=.
func (r *restHandler) PlanProjectSwitch(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	plan, err := r.prs.PlanSwitch(ctx, visitor.TokenID, id, c.Query("to_network_id"))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, plan)
}

// SwitchProject serves POST /projects/{id}/switches with {"to_network_id"}.
func (r *restHandler) SwitchProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req projectSwitchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidRequestBody))
		return
	}
	sw, err := r.prs.SwitchConsumers(ctx, visitor.TokenID, id, req.ToNetworkID, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, sw)
}

// GetRegression serves GET /projects/{id}/releases/{network_id}/regression.
func (r *restHandler) GetRegression(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, networkID := c.Param("project_id"), c.Param("network_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("network_id").String(networkID))

	regression, err := r.prs.GetRegression(ctx, visitor.TokenID, id, networkID)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, regression)
}

// RunRegression serves POST /projects/{id}/releases/{network_id}/regression:run and replies 202:
// the regression runs in the background.
func (r *restHandler) RunRegression(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, networkID := c.Param("project_id"), c.Param("network_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("network_id").String(networkID))

	regression, err := r.prs.RunRegression(ctx, visitor.TokenID, id, networkID, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, regression)
}

func (r *restHandler) PlanRollbackByEx(c *gin.Context) { r.withExVisitor(c, r.PlanRollback) }

func (r *restHandler) RollbackByEx(c *gin.Context) { r.withExVisitor(c, r.Rollback) }

// PlanRollback serves GET /projects/{id}/rollback-plan.
func (r *restHandler) PlanRollback(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	plan, err := r.prs.PlanRollback(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, plan)
}

// Rollback serves POST /projects/{id}/rollbacks with {"signature"} of the plan read.
func (r *restHandler) Rollback(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.ProjectRollback
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidRequestBody))
		return
	}
	sw, err := r.prs.Rollback(ctx, visitor.TokenID, id, req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, sw)
}
