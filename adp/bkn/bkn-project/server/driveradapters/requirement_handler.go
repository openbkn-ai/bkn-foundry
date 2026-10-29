// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"io"
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

// The requirement specification of a project. It follows the project's visibility; only its
// owner drafts and changes it.

const requirementsActionDraft = "requirements:draft"

func (r *restHandler) GetRequirementByEx(c *gin.Context) { r.withExVisitor(c, r.GetRequirement) }
func (r *restHandler) AddRequirementItemByEx(c *gin.Context) {
	r.withExVisitor(c, r.AddRequirementItem)
}
func (r *restHandler) EditRequirementItemByEx(c *gin.Context) {
	r.withExVisitor(c, r.EditRequirementItem)
}
func (r *restHandler) DeleteRequirementItemByEx(c *gin.Context) {
	r.withExVisitor(c, r.DeleteRequirementItem)
}
func (r *restHandler) MapRequirementConceptByEx(c *gin.Context) {
	r.withExVisitor(c, r.MapRequirementConcept)
}
func (r *restHandler) ResolveRequirementConflictByEx(c *gin.Context) {
	r.withExVisitor(c, r.ResolveRequirementConflict)
}
func (r *restHandler) DecideRequirementChoiceByEx(c *gin.Context) {
	r.withExVisitor(c, r.DecideRequirementChoice)
}

// generateRequirementRequest is the optional body of requirements:draft.
type generateRequirementRequest struct {
	// DiscardDecisions agrees to discard the conflict resolutions and choices made.
	DiscardDecisions bool `json:"discard_decisions"`
}

// GetRequirement serves GET /projects/{project_id}/requirements.
func (r *restHandler) GetRequirement(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	view, err := r.rqs.GetRequirement(ctx, visitor.TokenID, id)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, view)
}

// GenerateRequirement serves POST /projects/{project_id}/requirements:draft and replies 202 with
// the specification drafting.
func (r *restHandler) GenerateRequirement(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req generateRequirementRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			replyError(c, span, invalidRequirementBody(c))
			return
		}
	}
	spec, err := r.rqs.GenerateRequirement(ctx, visitor.TokenID, id, visitorAccount(visitor), req.DiscardDecisions)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, spec)
}

func invalidRequirementBody(c *gin.Context) error {
	return httperrors.NewHTTPError(c.Request.Context(), http.StatusBadRequest, perrors.BknProject_Requirement_InvalidRequestBody)
}

// replySpec runs a change of the specification read from the body and replies the specification
// with status.
func replySpec[T any](c *gin.Context, status int,
	change func(ctx context.Context, body T) (*interfaces.Requirement, error)) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))
	span.SetAttributes(attr.Key("project_id").String(c.Param("project_id")))

	var body T
	if err := c.ShouldBindJSON(&body); err != nil {
		replyError(c, span, invalidRequirementBody(c))
		return
	}
	spec, err := change(ctx, body)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, status)
	rest.ReplyOK(c, status, spec)
}

// AddRequirementItem serves POST /projects/{project_id}/requirements/items/{kind} and replies 201.
func (r *restHandler) AddRequirementItem(c *gin.Context, visitor hydra.Visitor) {
	replySpec(c, http.StatusCreated, func(ctx context.Context, edit interfaces.RequirementItemEdit) (*interfaces.Requirement, error) {
		return r.rqs.AddItem(ctx, visitor.TokenID, c.Param("project_id"), c.Param("kind"), edit, visitorAccount(visitor))
	})
}

// EditRequirementItem serves PATCH /projects/{project_id}/requirements/items/{kind}/{item_id}.
func (r *restHandler) EditRequirementItem(c *gin.Context, visitor hydra.Visitor) {
	replySpec(c, http.StatusOK, func(ctx context.Context, edit interfaces.RequirementItemEdit) (*interfaces.Requirement, error) {
		return r.rqs.EditItem(ctx, visitor.TokenID, c.Param("project_id"), c.Param("kind"), c.Param("item_id"), edit,
			visitorAccount(visitor))
	})
}

// DeleteRequirementItem serves DELETE /projects/{project_id}/requirements/items/{kind}/{item_id}
// with the update time read in the query, and replies the specification after it.
func (r *restHandler) DeleteRequirementItem(c *gin.Context, visitor hydra.Visitor) {
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
	spec, err := r.rqs.DeleteItem(ctx, visitor.TokenID, id, c.Param("kind"), c.Param("item_id"), updateTime,
		visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, spec)
}

// MapRequirementConcept serves PUT /projects/{project_id}/requirements/concepts/{concept_id}/maps-to.
func (r *restHandler) MapRequirementConcept(c *gin.Context, visitor hydra.Visitor) {
	replySpec(c, http.StatusOK, func(ctx context.Context, mapping interfaces.RequirementMapping) (*interfaces.Requirement, error) {
		return r.rqs.MapConcept(ctx, visitor.TokenID, c.Param("project_id"), c.Param("concept_id"), mapping,
			visitorAccount(visitor))
	})
}

// ResolveRequirementConflict serves PUT /projects/{project_id}/requirements/conflicts/{conflict_id}.
func (r *restHandler) ResolveRequirementConflict(c *gin.Context, visitor hydra.Visitor) {
	replySpec(c, http.StatusOK, func(ctx context.Context, resolve interfaces.RequirementResolve) (*interfaces.Requirement, error) {
		return r.rqs.ResolveConflict(ctx, visitor.TokenID, c.Param("project_id"), c.Param("conflict_id"), resolve,
			visitorAccount(visitor))
	})
}

// DecideRequirementChoice serves PUT /projects/{project_id}/requirements/choices/{choice_id}.
func (r *restHandler) DecideRequirementChoice(c *gin.Context, visitor hydra.Visitor) {
	replySpec(c, http.StatusOK, func(ctx context.Context, decide interfaces.RequirementDecide) (*interfaces.Requirement, error) {
		return r.rqs.DecideChoice(ctx, visitor.TokenID, c.Param("project_id"), c.Param("choice_id"), decide,
			visitorAccount(visitor))
	})
}

// queryUpdateTime reads the update time a deletion carries in its query: it has no body, so one
// that does not parse is a missing one.
func queryUpdateTime(c *gin.Context) (int64, error) {
	raw := c.Query("update_time")
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, httperrors.NewHTTPError(c.Request.Context(), http.StatusBadRequest,
			perrors.BknProject_Requirement_MissingUpdateTime).WithErrorDetails("update_time is not a number")
	}
	return parsed, nil
}

// The test questions and development guidance of a scenario question.

const guidanceActionGenerate = "dev:generate"

func (r *restHandler) QuestionActionByEx(c *gin.Context) {
	switch c.Param("action") {
	case guidanceActionGenerate:
		r.withExVisitor(c, r.GenerateGuidance)
	default:
		replyNotFound(c)
	}
}
func (r *restHandler) GetGuidanceByEx(c *gin.Context) { r.withExVisitor(c, r.GetGuidance) }
func (r *restHandler) EditTestByEx(c *gin.Context)    { r.withExVisitor(c, r.EditTest) }
func (r *restHandler) DeleteTestByEx(c *gin.Context)  { r.withExVisitor(c, r.DeleteTest) }

// GenerateGuidance serves POST /projects/{project_id}/requirements/questions/{question_id}/dev:generate
// and replies 202 with the guidance generating.
func (r *restHandler) GenerateGuidance(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, questionID := c.Param("project_id"), c.Param("question_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("question_id").String(questionID))

	g, err := r.rqs.GenerateGuidance(ctx, visitor.TokenID, id, questionID, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, g)
}

// GetGuidance serves GET /projects/{project_id}/requirements/questions/{question_id}/dev.
func (r *restHandler) GetGuidance(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, questionID := c.Param("project_id"), c.Param("question_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("question_id").String(questionID))

	view, err := r.rqs.GetGuidance(ctx, visitor.TokenID, id, questionID)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, view)
}

// EditTest serves PUT /projects/{project_id}/requirements/questions/{question_id}/tests/{test_id}.
func (r *restHandler) EditTest(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, questionID, testID := c.Param("project_id"), c.Param("question_id"), c.Param("test_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("question_id").String(questionID))

	var edit interfaces.RequirementTestEdit
	if err := c.ShouldBindJSON(&edit); err != nil {
		replyError(c, span, invalidRequirementBody(c))
		return
	}
	g, err := r.rqs.EditTest(ctx, visitor.TokenID, id, questionID, testID, edit, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, g)
}

// DeleteTest serves DELETE /projects/{project_id}/requirements/questions/{question_id}/tests/{test_id}
// with the update time read in the query, and replies the guidance after it.
func (r *restHandler) DeleteTest(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, questionID, testID := c.Param("project_id"), c.Param("question_id"), c.Param("test_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("question_id").String(questionID))

	updateTime, err := queryUpdateTime(c)
	if err != nil {
		replyError(c, span, err)
		return
	}
	g, err := r.rqs.DeleteTest(ctx, visitor.TokenID, id, questionID, testID, updateTime, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, g)
}
