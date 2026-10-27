// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
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

// Projects. A project is visible to callers who can see its network in
// bkn-backend; the project service checks that with the caller's token.

const (
	projectActionArchive   = ":archive"
	projectActionUnarchive = ":unarchive"
)

func (r *restHandler) CreateProjectByEx(c *gin.Context) { r.withExVisitor(c, r.CreateProject) }
func (r *restHandler) ListProjectsByEx(c *gin.Context)  { r.withExVisitor(c, r.ListProjects) }
func (r *restHandler) GetProjectByEx(c *gin.Context)    { r.withExVisitor(c, r.GetProject) }

// ProjectActionByEx serves POST /projects/{project_id}:archive and :unarchive.
func (r *restHandler) ProjectActionByEx(c *gin.Context) {
	param := c.Param("project_id")
	var id string
	var archived bool
	if trimmed, ok := strings.CutSuffix(param, projectActionArchive); ok {
		id, archived = trimmed, true
	} else if trimmed, ok := strings.CutSuffix(param, projectActionUnarchive); ok {
		id = trimmed
	}
	if id == "" {
		replyNotFound(c)
		return
	}
	r.withExVisitor(c, func(c *gin.Context, visitor hydra.Visitor) { r.SetProjectArchived(c, visitor, id, archived) })
}

func visitorAccount(visitor hydra.Visitor) interfaces.AccountInfo {
	return interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
}

// CreateProject serves POST /projects with {"name", "kn_id" | "new_kn", "catalog_ids"}.
func (r *restHandler) CreateProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	req := interfaces.CreateProjectRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidRequestBody))
		return
	}
	span.SetAttributes(attr.Key("kn_id").String(req.KNID))

	entry, err := r.ps.CreateProject(ctx, visitor.TokenID, req, visitorAccount(visitor), gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, entry)
}

// ListProjects serves GET /projects?archived=&name=&limit=&cursor=.
func (r *restHandler) ListProjects(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	limit, err := parseLimit(ctx, c.Query("limit"), interfaces.DEFAULT_PROJECT_LIMIT, interfaces.MAX_PROJECT_LIMIT)
	if err != nil {
		replyError(c, span, err)
		return
	}
	list, err := r.ps.ListProjects(ctx, visitor.TokenID, c.Query("archived"), c.Query("name"), limit, c.Query("cursor"))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, list)
}

func (r *restHandler) GetProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	entry, err := r.ps.GetProject(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, entry)
}

func (r *restHandler) SetProjectArchived(c *gin.Context, visitor hydra.Visitor, id string, archived bool) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("archived").Bool(archived))

	entry, err := r.ps.SetProjectArchived(ctx, visitor.TokenID, id, archived, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, entry)
}
