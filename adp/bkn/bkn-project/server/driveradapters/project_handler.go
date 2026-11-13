// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/driveradapters/httpx"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
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
func (r *restHandler) SetProjectCatalogsByEx(c *gin.Context) {
	r.withExVisitor(c, r.SetProjectCatalogs)
}

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

func visitorAccount(visitor hydra.Visitor) interfaces.AccountInfo { return httpx.Account(visitor) }

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
	r.describeProjects(ctx, visitor, entry)
	rest.ReplyOK(c, http.StatusCreated, entry)
}

// ListProjects serves GET /projects?archived=&name=&limit=&offset=&cursor=.
func (r *restHandler) ListProjects(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	limit, err := parseLimit(ctx, c.Query("limit"), interfaces.DEFAULT_PROJECT_LIMIT, interfaces.MAX_PROJECT_LIMIT)
	if err != nil {
		replyError(c, span, err)
		return
	}
	offset, err := parseOffset(ctx, c.Query("offset"))
	if err != nil {
		replyError(c, span, err)
		return
	}
	list, err := r.ps.ListProjects(ctx, visitor.TokenID, c.Query("archived"), c.Query("name"), limit, offset,
		c.Query("cursor"))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	r.describeProjects(ctx, visitor, list.Entries...)
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
	r.ps.ResolveCatalogs(ctx, visitor.TokenID, entry, visitorAccount(visitor))
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	r.describeProjects(ctx, visitor, entry)
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
	if hooks := projecthooks.Current().Projects; archived && hooks != nil {
		hooks.Archived(ctx, visitor.TokenID, id, visitorAccount(visitor))
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	r.describeProjects(ctx, visitor, entry)
	rest.ReplyOK(c, http.StatusOK, entry)
}

// SetProjectCatalogs serves PUT /projects/{project_id}/catalogs with {"catalog_ids"}.
func (r *restHandler) SetProjectCatalogs(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	req := interfaces.SetProjectCatalogsRequest{}
	// The catalogs are replaced as a whole, so leaving them out is not taken for none.
	if err := c.ShouldBindJSON(&req); err != nil || req.CatalogIDs == nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidRequestBody))
		return
	}

	entry, err := r.ps.SetProjectCatalogs(ctx, visitor.TokenID, id, req.CatalogIDs, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	r.describeProjects(ctx, visitor, entry)
	rest.ReplyOK(c, http.StatusOK, entry)
}

// parseOffset reads how many entries a page by number skips; an absent offset is 0.
func parseOffset(ctx context.Context, value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(value)
	if err != nil || offset < 0 {
		return 0, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Offset)
	}
	return offset, nil
}

// describeProjects names the owners and tells whether the caller may manage each project.
func (r *restHandler) describeProjects(ctx context.Context, visitor hydra.Visitor, entries ...*interfaces.ProjectEntry) {
	r.nameOwners(ctx, entries...)
	r.ps.MarkManageable(ctx, visitor.TokenID, visitorAccount(visitor), entries...)
}

func (r *restHandler) RenameProjectByEx(c *gin.Context)  { r.withExVisitor(c, r.RenameProject) }
func (r *restHandler) DeleteProjectByEx(c *gin.Context)  { r.withExVisitor(c, r.DeleteProject) }
func (r *restHandler) ExportProjectByEx(c *gin.Context)  { r.withExVisitor(c, r.ExportProject) }
func (r *restHandler) PreviewPackageByEx(c *gin.Context) { r.withExVisitor(c, r.PreviewPackage) }
func (r *restHandler) ImportPackageByEx(c *gin.Context)  { r.withExVisitor(c, r.ImportPackage) }

// RenameProject serves PATCH /projects/{project_id} with {"name"}.
func (r *restHandler) RenameProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	req := interfaces.RenameProjectRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidRequestBody))
		return
	}
	entry, err := r.ps.RenameProject(ctx, visitor.TokenID, id, req.Name, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	r.describeProjects(ctx, visitor, entry)
	rest.ReplyOK(c, http.StatusOK, entry)
}

// DeleteProject serves DELETE /projects/{project_id}.
func (r *restHandler) DeleteProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	if err := r.ps.DeleteProject(ctx, visitor.TokenID, id, visitorAccount(visitor)); err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	c.Status(http.StatusNoContent)
}

// ExportProject serves GET /projects/{project_id}/export: the project package as a file.
func (r *restHandler) ExportProject(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	pkg, name, err := r.ps.ExportProject(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	c.Data(http.StatusOK, "application/zip", pkg)
}

// PreviewPackage serves POST /project-packages/preview with the package in the multipart field file.
func (r *restHandler) PreviewPackage(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	pkg, err := readPackageUpload(ctx, c)
	if err != nil {
		replyError(c, span, err)
		return
	}
	preview, err := r.ps.PreviewPackage(ctx, pkg)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, preview)
}

// ImportPackage serves POST /project-packages/import with the package in the multipart field file
// and where to import it, ImportProjectOptions as JSON, in the field options.
func (r *restHandler) ImportPackage(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	pkg, err := readPackageUpload(ctx, c)
	if err != nil {
		replyError(c, span, err)
		return
	}
	options := interfaces.ImportProjectOptions{}
	if err := json.Unmarshal([]byte(c.Request.FormValue("options")), &options); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidRequestBody))
		return
	}
	entry, err := r.ps.ImportPackage(ctx, visitor.TokenID, pkg, options, visitorAccount(visitor), gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	r.describeProjects(ctx, visitor, entry)
	rest.ReplyOK(c, http.StatusCreated, entry)
}

// packageUploadSlack is what the multipart framing and the options field may add to a package.
const packageUploadSlack = 1 << 20

// readPackageUpload reads the package of the multipart field file, refusing one too large.
func readPackageUpload(ctx context.Context, c *gin.Context) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, interfaces.PROJECT_PACKAGE_MAX_BYTES+packageUploadSlack)
	tooLarge := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusRequestEntityTooLarge, perrors.BknProject_Project_PackageTooLarge).
			WithErrorDetails(map[string]any{"max_bytes": interfaces.PROJECT_PACKAGE_MAX_BYTES})
	}
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		if maxErr := (*http.MaxBytesError)(nil); errors.As(err, &maxErr) {
			return nil, tooLarge()
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)
	}
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)
	}
	defer func() { _ = file.Close() }()
	if header.Size > interfaces.PROJECT_PACKAGE_MAX_BYTES {
		return nil, tooLarge()
	}
	pkg, err := io.ReadAll(io.LimitReader(file, interfaces.PROJECT_PACKAGE_MAX_BYTES+1))
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)
	}
	if len(pkg) > interfaces.PROJECT_PACKAGE_MAX_BYTES {
		return nil, tooLarge()
	}
	return pkg, nil
}
