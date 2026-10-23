// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/visitor"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// Value types (SPECIFICATION.md §16) of a knowledge network. Each route has an external variant,
// which authenticates the caller, and an internal one, which takes the caller from the headers.

func (r *restHandler) CreateValueTypesByEx(c *gin.Context) { r.withExVisitor(c, r.CreateValueTypes) }
func (r *restHandler) ListValueTypesByEx(c *gin.Context)   { r.withExVisitor(c, r.ListValueTypes) }
func (r *restHandler) GetValueTypeByEx(c *gin.Context)     { r.withExVisitor(c, r.GetValueType) }
func (r *restHandler) UpdateValueTypeByEx(c *gin.Context)  { r.withExVisitor(c, r.UpdateValueType) }
func (r *restHandler) DeleteValueTypeByEx(c *gin.Context)  { r.withExVisitor(c, r.DeleteValueType) }

func (r *restHandler) CreateValueTypesByIn(c *gin.Context) {
	r.CreateValueTypes(c, visitor.GenerateVisitor(c))
}
func (r *restHandler) ListValueTypesByIn(c *gin.Context) {
	r.ListValueTypes(c, visitor.GenerateVisitor(c))
}
func (r *restHandler) GetValueTypeByIn(c *gin.Context) { r.GetValueType(c, visitor.GenerateVisitor(c)) }
func (r *restHandler) UpdateValueTypeByIn(c *gin.Context) {
	r.UpdateValueType(c, visitor.GenerateVisitor(c))
}
func (r *restHandler) DeleteValueTypeByIn(c *gin.Context) {
	r.DeleteValueType(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) withExVisitor(c *gin.Context, handle func(*gin.Context, hydra.Visitor)) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	handle(c, visitor)
}

// valueTypeRequest starts a value type request: the caller goes into the context and the
// knowledge network must exist. It replies the error itself and reports false when the request
// cannot go on.
func (r *restHandler) valueTypeRequest(c *gin.Context, visitor hydra.Visitor) (context.Context, trace.Span, string, string, bool) {
	ctx, span := oteltrace.StartServerSpan(c)
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)})
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("branch").String(branch))

	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
	if err == nil && !exist {
		err = rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)
	}
	if err != nil {
		replyError(c, span, err)
		return ctx, span, "", "", false
	}
	return ctx, span, knID, branch, true
}

func replyError(c *gin.Context, span trace.Span, err error) {
	httpErr := err.(*rest.HTTPError)
	oteltrace.AddHttpAttrs4HttpError(span, httpErr)
	rest.ReplyError(c, httpErr)
}

func (r *restHandler) CreateValueTypes(c *gin.Context, visitor hydra.Visitor) {
	ctx, span, knID, branch, ok := r.valueTypeRequest(c, visitor)
	defer span.End()
	if !ok {
		return
	}
	mode := c.DefaultQuery(interfaces.QueryParam_ImportMode, interfaces.ImportMode_Normal)
	if httpErr := validateImportMode(ctx, mode); httpErr != nil {
		replyError(c, span, httpErr)
		return
	}
	var body struct {
		Entries []*interfaces.ValueType `json:"entries"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.RequestBindingFailed", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		replyError(c, span, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_InvalidParameter).
			WithErrorDetails(localizedText))
		return
	}
	if len(body.Entries) == 0 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.EntriesRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		replyError(c, span, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody).
			WithErrorDetails(localizedText))
		return
	}
	for _, vt := range body.Entries {
		if vt != nil {
			vt.KNID, vt.Branch = knID, branch
		}
	}
	if err := ValidateValueTypes(ctx, body.Entries); err != nil {
		replyError(c, span, err)
		return
	}
	ids, err := r.vts.CreateValueTypes(ctx, nil, body.Entries, mode)
	if err != nil {
		replyError(c, span, err)
		return
	}
	result := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		result = append(result, map[string]any{"id": id})
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, result)
}

func (r *restHandler) ListValueTypes(c *gin.Context, visitor hydra.Visitor) {
	ctx, span, knID, branch, ok := r.valueTypeRequest(c, visitor)
	defer span.End()
	if !ok {
		return
	}
	pageParam, err := validatePaginationQueryParameters(ctx,
		c.DefaultQuery("offset", interfaces.DEFAULT_OFFEST),
		c.DefaultQuery("limit", interfaces.DEFAULT_LIMIT),
		c.DefaultQuery("sort", "update_time"),
		c.DefaultQuery("direction", interfaces.DESC_DIRECTION),
		interfaces.ValueTypeSort)
	if err != nil {
		replyError(c, span, err)
		return
	}
	list, total, err := r.vts.ListValueTypes(ctx, interfaces.ValueTypesQueryParams{
		PaginationQueryParameters: pageParam,
		NamePattern:               c.Query("name_pattern"),
		Tag:                       strings.TrimSpace(c.Query("tag")),
		Branch:                    branch,
		KNID:                      knID,
	})
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"entries": list, "total_count": total})
}

func (r *restHandler) GetValueType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span, knID, branch, ok := r.valueTypeRequest(c, visitor)
	defer span.End()
	if !ok {
		return
	}
	vt, err := r.vts.GetValueTypeByID(ctx, knID, branch, c.Param("vt_id"))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, vt)
}

func (r *restHandler) UpdateValueType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span, knID, branch, ok := r.valueTypeRequest(c, visitor)
	defer span.End()
	if !ok {
		return
	}
	var vt interfaces.ValueType
	if err := c.ShouldBindJSON(&vt); err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.RequestBindingFailed", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		replyError(c, span, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_InvalidParameter).
			WithErrorDetails(localizedText))
		return
	}
	vt.VTID, vt.KNID, vt.Branch = c.Param("vt_id"), knID, branch
	if err := ValidateValueType(ctx, &vt); err != nil {
		replyError(c, span, err)
		return
	}
	if err := r.vts.UpdateValueType(ctx, nil, &vt); err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

func (r *restHandler) DeleteValueType(c *gin.Context, visitor hydra.Visitor) {
	ctx, span, knID, branch, ok := r.valueTypeRequest(c, visitor)
	defer span.End()
	if !ok {
		return
	}
	vtID := c.Param("vt_id")
	vt, err := r.vts.GetValueTypeByID(ctx, knID, branch, vtID)
	if err != nil {
		replyError(c, span, err)
		return
	}
	if err = r.vts.DeleteValueType(ctx, nil, knID, branch, vtID); err != nil {
		replyError(c, span, err)
		return
	}
	c.Set("operation_audit_target_names", []string{vt.VTName})
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}
