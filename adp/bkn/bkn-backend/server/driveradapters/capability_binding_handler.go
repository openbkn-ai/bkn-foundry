// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/audit"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"bkn-backend/common"
	"bkn-backend/common/visitor"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func (r *restHandler) AttachCapabilitiesByEx(c *gin.Context) {
	vis, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.AttachCapabilities(c, vis)
}

func (r *restHandler) AttachCapabilitiesByIn(c *gin.Context) {
	r.AttachCapabilities(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) DetachCapabilitiesByEx(c *gin.Context) {
	vis, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.DetachCapabilities(c, vis)
}

func (r *restHandler) DetachCapabilitiesByIn(c *gin.Context) {
	r.DetachCapabilities(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) ListCapabilitiesByEx(c *gin.Context) {
	vis, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ListCapabilities(c, vis)
}

func (r *restHandler) ListCapabilitiesByIn(c *gin.Context) {
	r.ListCapabilities(c, visitor.GenerateVisitor(c))
}

// resolveCapabilityKN parses the shared path parameters and confirms the knowledge network branch
// exists. It returns false when a reply has already been written.
func (r *restHandler) resolveCapabilityKN(c *gin.Context, ctx context.Context, span interface {
	SetAttributes(...attr.KeyValue)
}) (knID, branch string, ok bool) {
	knID = c.Param("kn_id")
	branch = c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("branch").String(branch))

	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		rest.ReplyError(c, httpErr)
		return "", "", false
	}
	if !exist {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound))
		return "", "", false
	}
	return knID, branch, true
}

func (r *restHandler) AttachCapabilities(c *gin.Context, vis hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID, branch, ok := r.resolveCapabilityKN(c, ctx, span)
	if !ok {
		return
	}

	req := &interfaces.AttachCapabilitiesReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	bindings, err := r.cbs.AttachCapabilities(ctx, nil, knID, branch, req.Capabilities)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	for _, binding := range bindings {
		audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(vis),
			interfaces.GenerateCapabilityBindingAuditObject(binding.ID, binding.CapabilityID), "")
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, &interfaces.CapabilityBindingsList{
		Entries:    bindings,
		TotalCount: len(bindings),
	})
}

func (r *restHandler) DetachCapabilities(c *gin.Context, vis hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID, branch, ok := r.resolveCapabilityKN(c, ctx, span)
	if !ok {
		return
	}

	bindingIDs := common.StringToStringSlice(c.Param("binding_ids"))
	if len(bindingIDs) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidParameter).WithErrorDetails("binding_ids must not be empty")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if _, err := r.cbs.DetachCapabilities(ctx, nil, knID, branch, bindingIDs); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	for _, id := range bindingIDs {
		audit.NewInfoLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(vis),
			interfaces.GenerateCapabilityBindingAuditObject(id, ""), "")
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

func (r *restHandler) ListCapabilities(c *gin.Context, vis hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID, branch, ok := r.resolveCapabilityKN(c, ctx, span)
	if !ok {
		return
	}

	offset := c.DefaultQuery("offset", interfaces.DEFAULT_OFFEST)
	limit := c.DefaultQuery("limit", interfaces.DEFAULT_LIMIT)
	sort := c.DefaultQuery("sort", "create_time")
	direction := c.DefaultQuery("direction", interfaces.DESC_DIRECTION)

	pageParam, err := validatePaginationQueryParameters(ctx, offset, limit, sort, direction,
		interfaces.CapabilityBindingSort)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	list, err := r.cbs.ListCapabilities(ctx, interfaces.CapabilityBindingsQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Offset:    pageParam.Offset,
			Limit:     pageParam.Limit,
			Sort:      pageParam.Sort,
			Direction: pageParam.Direction,
		},
		KNID:           knID,
		Branch:         branch,
		CapabilityType: c.Query("type"),
		OwnerID:        c.Query("owner_id"),
	})
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, list)
}
