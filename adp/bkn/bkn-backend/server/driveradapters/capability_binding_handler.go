// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
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

// ListCapabilitiesByIn answers with bare references rather than the public face's backfilled
// bindings. Context Loader calls this on every recall to learn what is in scope, and it goes to
// the execution factory for names itself — backfilling here would add a service hop per recall to
// produce data the caller is about to fetch.
func (r *restHandler) ListCapabilitiesByIn(c *gin.Context) {
	r.ResolveCapabilities(c, visitor.GenerateVisitor(c))
}

// resolveCapabilityKN parses the shared path parameters and confirms the knowledge network branch
// exists. It returns false when a reply has already been written.
func (r *restHandler) resolveCapabilityKN(c *gin.Context, ctx context.Context, span interface {
	SetAttributes(...attr.KeyValue)
}) (knID, branch string, ok bool) {
	knID = c.Param("kn_id")
	branch = c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("branch").String(branch))

	knName, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		rest.ReplyError(c, httpErr)
		return "", "", false
	}
	if !exist {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound))
		return "", "", false
	}
	if r.auditRecorder != nil {
		c.Set("operation_audit_kn_name", knName)
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

	result, err := r.publishKNCapabilityMutation(ctx, knID, branch, nil,
		func(mutationCtx context.Context, tx *sql.Tx) (*interfaces.KNCapabilityMutationResult, error) {
			bindings, mutationErr := r.cbs.AttachCapabilities(mutationCtx, tx, knID, branch, req.Capabilities)
			return &interfaces.KNCapabilityMutationResult{Bindings: bindings}, mutationErr
		})
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	bindings := result.Bindings
	if r.auditRecorder != nil {
		// Only new rows are committed changes; an idempotent mount is not an audit event.
		ids := make([]string, 0, len(bindings))
		seen := make(map[string]bool, len(bindings))
		for _, binding := range bindings {
			if binding != nil && binding.CreatedByAttach && !seen[binding.ID] {
				ids = append(ids, binding.ID)
				seen[binding.ID] = true
			}
		}
		c.Set("operation_audit_skip", true)
		if len(ids) > 0 {
			names := r.operationAuditCapabilityNames(c, ctx, knID, branch, ids)
			if len(names) == len(ids) {
				c.Set("operation_audit_target_names", names)
				c.Set("operation_audit_target_ids", ids)
				c.Set("operation_audit_skip", false)
			} else {
				logger.Errorf("operation audit fact rejected: incomplete attached capability snapshot")
			}
		}
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, &interfaces.CapabilityBindingsList{
		Entries:    bindings,
		TotalCount: len(bindings),
		Boxes:      make([]*interfaces.CapabilityBoxSummary, 0),
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

	// Capture binding metadata before deletion; a failed audit lookup must not block the mutation.
	var auditNames []string
	if r.auditRecorder != nil {
		auditNames = r.operationAuditCapabilityNames(c, ctx, knID, branch, bindingIDs)
	}

	result, err := r.publishKNCapabilityMutation(ctx, knID, branch, bindingIDs,
		func(mutationCtx context.Context, tx *sql.Tx) (*interfaces.KNCapabilityMutationResult, error) {
			rows, mutationErr := r.cbs.DetachCapabilities(mutationCtx, tx, knID, branch, bindingIDs)
			return &interfaces.KNCapabilityMutationResult{RowsAffected: rows}, mutationErr
		})
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if result != nil && result.RowsAffected > 0 && result.RowsAffected == int64(len(auditNames)) && len(auditNames) == len(bindingIDs) {
		c.Set("operation_audit_target_names", auditNames)
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

	readAccessMode, err := r.kns.ResolveKNReadAccess(ctx, knID, branch)
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
		ReadAccessMode: readAccessMode,
		// Either spelling narrows by tool box; box_id is what every other surface calls it.
		OwnerID:      firstNonEmpty(c.Query("owner_id"), c.Query("box_id")),
		WithDetail:   strings.EqualFold(strings.TrimSpace(c.Query("with_detail")), "true"),
		MetadataType: strings.TrimSpace(c.Query("metadata_type")),
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

// ResolveCapabilities serves the internal reference list.
func (r *restHandler) ResolveCapabilities(c *gin.Context, vis hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID, branch, ok := r.resolveCapabilityKN(c, ctx, span)
	if !ok {
		return
	}

	list, err := r.cbs.ResolveCapabilities(ctx, knID, branch, c.Query("type"))
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, list)
}

// operationAuditCapabilityNames resolves only the affected binding IDs using existing metadata.
func (r *restHandler) operationAuditCapabilityNames(c *gin.Context, ctx context.Context, knID, branch string, ids []string) []string {
	list, err := r.cbs.ListCapabilities(ctx, interfaces.CapabilityBindingsQueryParams{
		KNID: knID, Branch: branch, WithDetail: true,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
	})
	if err != nil || list == nil {
		return nil
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	knName := c.GetString("operation_audit_kn_name")
	var names []string
	for _, binding := range list.Entries {
		if binding == nil || !selected[binding.ID] || knName == "" {
			continue
		}
		name := binding.Name
		if binding.CapabilityType == interfaces.CAPABILITY_TYPE_MCP_TOOL {
			// MCP CapabilityID is the protocol tool_name, rather than an opaque ID.
			if name == "" {
				name = binding.CapabilityID
			}
		} else if name == binding.CapabilityID {
			continue
		}
		if name != "" {
			names = append(names, knName+" / "+name)
			delete(selected, binding.ID)
		}
	}
	return names
}
