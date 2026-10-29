// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/requirement"
)

// Requirement documents of a project. They follow the project's visibility; only its owner
// changes them.

// uploadField is the multipart field the files of an upload come in.
const uploadField = "files"

// maxUploadBytes bounds an upload's body: a project's limit of documents at their limit of size,
// with room for the multipart framing.
const maxUploadBytes = int64(requirement.MaxDocuments*requirement.MaxDocumentBytes) + 1<<20

// uploadMemoryBytes is how much of an upload is parsed in memory before going to temporary files.
const uploadMemoryBytes = 32 << 20

func (r *restHandler) UploadRequirementDocumentsByEx(c *gin.Context) {
	r.withExVisitor(c, r.UploadRequirementDocuments)
}
func (r *restHandler) ListRequirementDocumentsByEx(c *gin.Context) {
	r.withExVisitor(c, r.ListRequirementDocuments)
}
func (r *restHandler) GetRequirementDocumentByEx(c *gin.Context) {
	r.withExVisitor(c, r.GetRequirementDocument)
}
func (r *restHandler) SetRequirementDocumentTypeByEx(c *gin.Context) {
	r.withExVisitor(c, r.SetRequirementDocumentType)
}
func (r *restHandler) DeleteRequirementDocumentByEx(c *gin.Context) {
	r.withExVisitor(c, r.DeleteRequirementDocument)
}

// UploadRequirementDocuments serves POST /projects/{project_id}/requirements/documents, a
// multipart form with the files under files, and replies each file's outcome.
func (r *restHandler) UploadRequirementDocuments(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	if err := c.Request.ParseMultipartForm(uploadMemoryBytes); err != nil {
		code := perrors.BknProject_Requirement_InvalidRequestBody
		if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
			code = perrors.BknProject_Requirement_DocumentTooLarge
		}
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, code))
		return
	}
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	headers := c.Request.MultipartForm.File[uploadField]
	if len(headers) == 0 {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidRequestBody))
		return
	}
	if len(headers) > requirement.MaxDocuments {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_TooManyDocuments))
		return
	}
	files := make([]interfaces.RequirementUpload, 0, len(headers))
	for _, header := range headers {
		f, err := header.Open()
		if err != nil {
			replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidRequestBody))
			return
		}
		// A byte past the limit is enough for the service to refuse the file as too large.
		content, err := io.ReadAll(io.LimitReader(f, requirement.MaxDocumentBytes+1))
		_ = f.Close()
		if err != nil {
			replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidRequestBody))
			return
		}
		files = append(files, interfaces.RequirementUpload{Name: header.Filename, Content: content})
	}
	results, err := r.rqs.UploadDocuments(ctx, visitor.TokenID, id, files, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, gin.H{"results": results})
}

// ListRequirementDocuments serves GET /projects/{project_id}/requirements/documents, without the
// documents' content.
func (r *restHandler) ListRequirementDocuments(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	docs, err := r.rqs.ListDocuments(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, gin.H{"entries": docs})
}

// GetRequirementDocument serves GET /projects/{project_id}/requirements/documents/{document_id},
// with the content and its blocks.
func (r *restHandler) GetRequirementDocument(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, documentID := c.Param("project_id"), c.Param("document_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("document_id").String(documentID))

	doc, err := r.rqs.GetDocument(ctx, visitor.TokenID, id, documentID)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, doc)
}

// setDocumentTypeRequest is the body of PATCH .../documents/{document_id}.
type setDocumentTypeRequest struct {
	DocType string `json:"doc_type"`
}

// SetRequirementDocumentType serves PATCH /projects/{project_id}/requirements/documents/{document_id}
// with {"doc_type"}.
func (r *restHandler) SetRequirementDocumentType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, documentID := c.Param("project_id"), c.Param("document_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("document_id").String(documentID))

	var req setDocumentTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidRequestBody))
		return
	}
	doc, err := r.rqs.SetDocumentType(ctx, visitor.TokenID, id, documentID, req.DocType, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, doc)
}

// DeleteRequirementDocument serves DELETE /projects/{project_id}/requirements/documents/{document_id}.
func (r *restHandler) DeleteRequirementDocument(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, documentID := c.Param("project_id"), c.Param("document_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("document_id").String(documentID))

	if err := r.rqs.DeleteDocument(ctx, visitor.TokenID, id, documentID, visitorAccount(visitor)); err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	c.Status(http.StatusNoContent)
}
