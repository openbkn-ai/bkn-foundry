// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ontology drafts of a project. They follow the project's visibility; drafting reads
// vega-backend as the caller.

const (
	draftActionGenerate      = "draft:generate"
	draftActionRestore       = "draft:restore"
	draftActionConfirm       = "draft:confirm"
	draftActionAccept        = "draft:accept_suggestions"
	checkActionRun           = "check:run"
	indexActionBuild         = "index:build"
	catalogsActionUnderstand = "catalogs:understand"
	catalogsActionAdd        = "catalogs:add"
	sourceChangesActionDraft = "source-changes:draft"
)

func (r *restHandler) GetDraftByEx(c *gin.Context)       { r.withExVisitor(c, r.GetDraft) }
func (r *restHandler) GetCheckByEx(c *gin.Context)       { r.withExVisitor(c, r.GetCheck) }
func (r *restHandler) GetIndexByEx(c *gin.Context)       { r.withExVisitor(c, r.GetIndex) }
func (r *restHandler) GetStatusByEx(c *gin.Context)      { r.withExVisitor(c, r.GetStatus) }
func (r *restHandler) GetPendingByEx(c *gin.Context)     { r.withExVisitor(c, r.GetPending) }
func (r *restHandler) EditDraftItemByEx(c *gin.Context)  { r.withExVisitor(c, r.EditDraftItem) }
func (r *restHandler) ChooseIdentityByEx(c *gin.Context) { r.withExVisitor(c, r.ChooseIdentity) }
func (r *restHandler) SaveObjectTypeByEx(c *gin.Context) { r.withExVisitor(c, r.SaveObjectType) }
func (r *restHandler) GetCandidatesByEx(c *gin.Context)  { r.withExVisitor(c, r.GetCandidates) }
func (r *restHandler) CreateRelationTypeByEx(c *gin.Context) {
	r.withExVisitor(c, r.CreateRelationType)
}
func (r *restHandler) SaveRelationTypeByEx(c *gin.Context) { r.withExVisitor(c, r.SaveRelationType) }
func (r *restHandler) DeleteRelationTypeByEx(c *gin.Context) {
	r.withExVisitor(c, r.DeleteRelationType)
}
func (r *restHandler) AskObjectTypeByEx(c *gin.Context)   { r.withExVisitor(c, r.AskObjectType) }
func (r *restHandler) AskRelationTypeByEx(c *gin.Context) { r.withExVisitor(c, r.AskRelationType) }

// RelationTypesActionByEx serves POST …/relation_types:ask.
func (r *restHandler) RelationTypesActionByEx(c *gin.Context) {
	// The router keeps the colon in the parameter.
	if c.Param("action") != ":ask" {
		replyNotFound(c)
		return
	}
	r.withExVisitor(c, r.AskNewRelationType)
}
func (r *restHandler) RelationSuggestionsByEx(c *gin.Context) {
	r.withExVisitor(c, r.RelationSuggestions)
}

// RelationSuggestionsActionByEx serves POST …/relation-suggestions:ask.
func (r *restHandler) RelationSuggestionsActionByEx(c *gin.Context) {
	// The router keeps the colon in the parameter.
	if c.Param("action") != ":ask" {
		replyNotFound(c)
		return
	}
	r.withExVisitor(c, r.AskRelationSuggestions)
}

// relationSuggestionsAsk is the body of relation-suggestions:ask.
type relationSuggestionsAsk struct {
	ObjectType string `json:"object_type"`
}

// AskRelationSuggestions serves POST …/ontology/draft/relation-suggestions:ask: an object type's
// candidates with Ask AI's reasons and names, and the relations it proposes. It changes nothing.
func (r *restHandler) AskRelationSuggestions(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	var req relationSuggestionsAsk
	if err := c.ShouldBindJSON(&req); err != nil || req.ObjectType == "" {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("object_type").String(req.ObjectType))
	suggestions, err := r.drs.AskRelationSuggestions(ctx, visitor.TokenID, id, req.ObjectType, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, suggestions)
}

// generateDraftRequest is the optional body of draft:generate.
type generateDraftRequest struct {
	// DiscardDecisions agrees to discard the decisions people made on the current draft.
	DiscardDecisions bool `json:"discard_decisions"`
}

// OntologyActionByEx serves POST /projects/{project_id}/ontology/draft:generate,
// draft:restore, draft:confirm, check:run and index:build.
func (r *restHandler) OntologyActionByEx(c *gin.Context) {
	switch c.Param("action") {
	case draftActionGenerate:
		r.withExVisitor(c, r.GenerateDraft)
	case draftActionRestore:
		r.withExVisitor(c, r.RestoreExcluded)
	case draftActionConfirm:
		r.withExVisitor(c, r.ConfirmDraft)
	case draftActionAccept:
		r.withExVisitor(c, r.AcceptSuggestions)
	case checkActionRun:
		r.withExVisitor(c, r.RunCheck)
	case indexActionBuild:
		r.withExVisitor(c, r.BuildIndex)
	default:
		replyNotFound(c)
	}
}

// ProjectDataActionByEx serves POST /projects/{project_id}/catalogs:understand, branch:sync,
// drift:commit, requirements:draft and requirements:review.
func (r *restHandler) ProjectDataActionByEx(c *gin.Context) {
	switch c.Param("action") {
	case catalogsActionUnderstand:
		r.withExVisitor(c, r.Understand)
	case catalogsActionAdd:
		r.withExVisitor(c, r.AddCatalogs)
	case projectActionSync:
		r.withExVisitor(c, r.SyncProjectBranch)
	case driftActionCommit:
		r.withExVisitor(c, r.CommitProjectDrift)
	case requirementsActionDraft:
		r.withExVisitor(c, r.GenerateRequirement)
	case requirementsActionReview:
		r.withExVisitor(c, r.ReviewRequirementItems)
	case sourceChangesActionDraft:
		r.withExVisitor(c, r.DraftSourceChanges)
	default:
		replyNotFound(c)
	}
}

func (r *restHandler) GetUnderstandingByEx(c *gin.Context) { r.withExVisitor(c, r.GetUnderstanding) }

// Understand submits the semantic understanding of the project's tables and replies 202.
func (r *restHandler) Understand(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.ProjectUnderstand
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
			return
		}
	}
	submitted, err := r.drs.Understand(ctx, visitor.TokenID, id, req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, submitted)
}

// GetUnderstanding replies the semantic understanding of the project's tables.
func (r *restHandler) GetUnderstanding(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	understanding, err := r.drs.GetUnderstanding(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, understanding)
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

// BuildIndex builds the last confirmation's local indexes again and replies the readiness.
func (r *restHandler) BuildIndex(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	index, err := r.drs.BuildIndex(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, index)
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

// AskObjectType serves POST /projects/{project_id}/ontology/draft/object_types/{item_id}/ask: Ask AI
// about one object type. It reads the draft and never changes it.
func (r *restHandler) AskObjectType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, itemID := c.Param("project_id"), c.Param("item_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("item_id").String(itemID))

	var req interfaces.DraftAskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	replyAsk(c, span, func(stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
		return r.drs.AskObjectType(ctx, visitor.TokenID, id, itemID, req, visitorAccount(visitor), stream)
	})
}

// AskRelationType serves POST /projects/{project_id}/ontology/draft/relation_types/{item_id}/ask:
// Ask AI about one relation type. It reads the draft and never changes it.
func (r *restHandler) AskRelationType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, itemID := c.Param("project_id"), c.Param("item_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("item_id").String(itemID))

	var req interfaces.DraftRelationAskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	replyAsk(c, span, func(stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
		return r.drs.AskRelationType(ctx, visitor.TokenID, id, itemID, req, visitorAccount(visitor), stream)
	})
}

// AskNewRelationType serves POST …/ontology/draft/relation_types:ask: AskRelationType for a
// relation type not made yet, from the editor's form alone. It changes nothing.
func (r *restHandler) AskNewRelationType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.DraftRelationAskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	replyAsk(c, span, func(stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
		return r.drs.AskNewRelationType(ctx, visitor.TokenID, id, req, visitorAccount(visitor), stream)
	})
}

// RelationSuggestions serves GET /projects/{project_id}/ontology/draft/relation-suggestions: the
// isolated object types, or with ?object_type= the relations that object type could take. It reads
// the draft and never changes it.
func (r *restHandler) RelationSuggestions(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, objectType := c.Param("project_id"), c.Query("object_type")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("object_type").String(objectType))

	var reply any
	var err error
	if objectType == "" {
		reply, err = r.drs.IsolatedObjectTypes(ctx, visitor.TokenID, id)
	} else {
		reply, err = r.drs.RelationSuggestions(ctx, visitor.TokenID, id, objectType, visitorAccount(visitor))
	}
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, reply)
}

// Events of a streamed answer to Ask AI: token ({content}) for each piece of the answer's text,
// then result (the answer as replied without streaming) or error ({error_code, description,
// answer}: the text written so far).
const (
	askEventToken  = "token"
	askEventResult = "result"
	askEventError  = "error"
)

// replyAsk replies an answer of Ask AI: as server-sent events when the client accepts them, from
// the moment bkn-agent begins answering, otherwise as one JSON body. A refusal before bkn-agent
// answers is an error reply either way.
func replyAsk(c *gin.Context, span trace.Span, ask func(stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error)) {
	if !strings.Contains(c.GetHeader("Accept"), "text/event-stream") {
		answer, err := ask(nil)
		if err != nil {
			replyError(c, span, err)
			return
		}
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, answer)
		return
	}
	started := false
	var text strings.Builder
	send := func(event string, data any) {
		c.SSEvent(event, data)
		c.Writer.Flush()
	}
	start := func() {
		started = true
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		c.Writer.Flush()
	}
	answer, err := ask(&interfaces.DraftAskStream{Start: start, Token: func(token string) {
		text.WriteString(token)
		send(askEventToken, gin.H{"content": token})
	}})
	switch {
	case err != nil && !started:
		replyError(c, span, err)
	case err != nil:
		httpErr := err.(*httperrors.HTTPError)
		_ = httpErr.Render()
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		send(askEventError, gin.H{"error_code": httpErr.ErrorCode, "description": httpErr.Description,
			"answer": strings.TrimSpace(text.String())})
	default:
		if !started {
			start()
		}
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		send(askEventResult, answer)
	}
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

func (r *restHandler) GetSourceChangesByEx(c *gin.Context) { r.withExVisitor(c, r.GetSourceChanges) }

// GetSourceChanges replies how the tables the project binds differ from what it maps.
func (r *restHandler) GetSourceChanges(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	changes, err := r.drs.GetSourceChanges(ctx, visitor.TokenID, id, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, changes)
}

// DraftSourceChanges applies the source changes read to the draft and replies the draft.
func (r *restHandler) DraftSourceChanges(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.SourceChangesDraft
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	draft, err := r.drs.DraftSourceChanges(ctx, visitor.TokenID, id, req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}

// SaveObjectType saves an object type of the draft as a whole and replies the draft.
func (r *restHandler) SaveObjectType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, itemID := c.Param("project_id"), c.Param("item_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("item_id").String(itemID))

	var save interfaces.DraftObjectTypeSave
	if err := c.ShouldBindJSON(&save); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	draft, err := r.drs.SaveObjectType(ctx, visitor.TokenID, id, itemID, save, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}

// AddCatalogs adds catalogs to the project and drafts their tables into the draft; it replies 202
// with the draft, drafting.
func (r *restHandler) AddCatalogs(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.DraftAddCatalogs
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	draft, err := r.drs.AddCatalogs(ctx, visitor.TokenID, id, req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, draft)
}

// CreateRelationType makes a relation type of the draft and replies 201 with the draft.
func (r *restHandler) CreateRelationType(c *gin.Context, visitor hydra.Visitor) {
	r.saveRelationType(c, visitor, "")
}

// SaveRelationType saves a relation type of the draft as a whole and replies the draft.
func (r *restHandler) SaveRelationType(c *gin.Context, visitor hydra.Visitor) {
	r.saveRelationType(c, visitor, c.Param("item_id"))
}

// saveRelationType makes a relation type when itemID is empty, else saves that one.
func (r *restHandler) saveRelationType(c *gin.Context, visitor hydra.Visitor, itemID string) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("item_id").String(itemID))

	var save interfaces.DraftRelationTypeSave
	if err := c.ShouldBindJSON(&save); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	var draft *interfaces.Draft
	var err error
	status := http.StatusOK
	if itemID == "" {
		draft, err = r.drs.CreateRelationType(ctx, visitor.TokenID, id, save, visitorAccount(visitor))
		status = http.StatusCreated
	} else {
		draft, err = r.drs.SaveRelationType(ctx, visitor.TokenID, id, itemID, save, visitorAccount(visitor))
	}
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, status)
	rest.ReplyOK(c, status, draft)
}

// DeleteRelationType deletes a relation type a person made, the draft's update time in the query.
func (r *restHandler) DeleteRelationType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id, itemID := c.Param("project_id"), c.Param("item_id")
	span.SetAttributes(attr.Key("project_id").String(id), attr.Key("item_id").String(itemID))

	updateTime, err := strconv.ParseInt(c.Query("update_time"), 10, 64)
	if err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_MissingUpdateTime))
		return
	}
	draft, err := r.drs.DeleteRelationType(ctx, visitor.TokenID, id, itemID, updateTime, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, draft)
}

// GetCandidates replies the draft's items as candidates, of the catalog_id the query names, if any.
func (r *restHandler) GetCandidates(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	candidates, err := r.drs.GetCandidates(ctx, visitor.TokenID, id, c.Query("catalog_id"))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, candidates)
}

// AcceptSuggestions accepts the suggestions of the candidates not reviewed yet.
func (r *restHandler) AcceptSuggestions(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("project_id")
	span.SetAttributes(attr.Key("project_id").String(id))

	var req interfaces.DraftAcceptSuggestions
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody))
		return
	}
	accepted, err := r.drs.AcceptSuggestions(ctx, visitor.TokenID, id, req, visitorAccount(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, accepted)
}
