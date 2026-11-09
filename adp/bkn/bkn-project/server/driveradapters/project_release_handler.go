// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A project's releases: reviewing one, preparing it in the network's draft, publishing it,
// bringing the running version into the branch, and rolling back. Syncing and committing drift are
// custom methods, branch:sync and drift:commit, that the project's action route hands here.

const (
	projectActionSync    = "branch:sync"
	driftActionCommit    = "drift:commit"
	regressionActionRun  = "regression:run"
	releaseActionPrepare = "prepare"
)

func (r *restHandler) ReviewReleaseByEx(c *gin.Context)  { r.withExVisitor(c, r.ReviewRelease) }
func (r *restHandler) PublishProjectByEx(c *gin.Context) { r.withExVisitor(c, r.PublishProject) }
func (r *restHandler) GetRegressionByEx(c *gin.Context)  { r.withExVisitor(c, r.GetRegression) }

// ReleaseActionByEx serves the methods on a release of the project: prepare and regression:run.
func (r *restHandler) ReleaseActionByEx(c *gin.Context) {
	switch c.Param("action") {
	case releaseActionPrepare:
		r.withExVisitor(c, r.PrepareProject)
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

// PrepareProject serves POST /projects/{id}/releases/{version}/prepare with {"signature", "accepts",
// "overwrite_draft_changes"}.
func (r *restHandler) PrepareProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.ProjectPrepare
	if err := bindOptionalJSON(c, &req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidRequestBody))
		return
	}
	req.Version = c.Param("version")
	release, err := r.prs.PrepareRelease(ctx, visitor.TokenID, id, req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, release)
}

// PublishProject serves POST /projects/{id}/releases with {"version", "notes", "allow_outdated_base"}.
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

// GetRegression serves GET /projects/{id}/releases/{version}/regression.
func (r *restHandler) GetRegression(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, version := c.Param("project_id"), c.Param("version")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("version").String(version))

	regression, err := r.prs.GetRegression(ctx, visitor.TokenID, id, version)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, regression)
}

// RunRegression serves POST /projects/{id}/releases/{version}/regression:run and replies 202:
// the regression runs in the background.
func (r *restHandler) RunRegression(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, version := c.Param("project_id"), c.Param("version")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("version").String(version))

	regression, err := r.prs.RunRegression(ctx, visitor.TokenID, id, version, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, regression)
}

func (r *restHandler) PlanRollbackByEx(c *gin.Context) { r.withExVisitor(c, r.PlanRollback) }

func (r *restHandler) RollbackByEx(c *gin.Context) { r.withExVisitor(c, r.Rollback) }

// PlanRollback serves GET /projects/{id}/rollback-plan[?to_version=N]; no version is the previous one.
func (r *restHandler) PlanRollback(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	toVersion := 0
	if v := c.Query("to_version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidRequestBody))
			return
		}
		toVersion = n
	}
	plan, err := r.prs.PlanRollback(ctx, visitor.TokenID, id, toVersion)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, plan)
}

// Rollback serves POST /projects/{id}/rollbacks with {"signature", "to_version"} of the plan read.
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
	result, err := r.prs.Rollback(ctx, visitor.TokenID, id, req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) ListVersionsByEx(c *gin.Context) { r.withExVisitor(c, r.ListProjectVersions) }
func (r *restHandler) CopyVersionByEx(c *gin.Context)  { r.withExVisitor(c, r.CopyProjectVersion) }

// ListProjectVersions serves GET /projects/{id}/versions.
func (r *restHandler) ListProjectVersions(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	versions, err := r.prs.ListVersions(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, versions)
}

// CopyProjectVersion serves POST /projects/{id}/versions/{kn_version}/copy with
// {"overwrite_draft_changes"}.
func (r *restHandler) CopyProjectVersion(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	knVersion, err := strconv.Atoi(c.Param("kn_version"))
	var req interfaces.ProjectCopyVersion
	if err != nil || knVersion < 1 || bindOptionalJSON(c, &req) != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidRequestBody))
		return
	}
	result, err := r.prs.CopyVersion(ctx, visitor.TokenID, id, knVersion, req, visitorAccount(visitor), gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// bindOptionalJSON reads a JSON body whose fields are all optional: no body is the zero value.
func bindOptionalJSON(c *gin.Context, out any) error {
	if c.Request.ContentLength == 0 {
		return nil
	}
	return c.ShouldBindJSON(out)
}
