// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/audit"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"bkn-backend/common/visitor"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

const projectionGrantHeader = "X-BKN-Projection-Grant"

// GetKNByProjectionGrant exports one current network for a sealed historical
// projection build. It has no caller, tenant, or business-domain fallback.
func (r *restHandler) GetKNByProjectionGrant(c *gin.Context) {
	if r.projectionGrantVerifier == nil {
		c.Status(http.StatusNotFound)
		return
	}
	knID := c.Param("kn_id")
	if _, err := r.projectionGrantVerifier.Authorize(c.GetHeader(projectionGrantHeader), knID); err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	kn, err := r.kns.GetKNByID(c.Request.Context(), knID, interfaces.MAIN_BRANCH, interfaces.Mode_Export)
	if err != nil {
		if httpErr, ok := err.(*rest.HTTPError); ok {
			rest.ReplyError(c, httpErr)
			return
		}
		c.Status(http.StatusInternalServerError)
		return
	}
	rest.ReplyOK(c, http.StatusOK, kn)
}

// Create knowledge networks (internal).
func (r *restHandler) CreateKNByIn(c *gin.Context) {
	logger.Debug("Handler CreateKNByIn Start")
	// Internal endpoints read user_id from the header.
	visitor := visitor.GenerateVisitor(c)
	r.CreateKN(c, visitor)
}

// Create knowledge networks (external).
func (r *restHandler) CreateKNByEx(c *gin.Context) {
	logger.Debug("Handler CreateKNByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.CreateKN(c, visitor)
}

// Create a knowledge network.
func (r *restHandler) CreateKN(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler CreateKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Import mode.
	mode := c.DefaultQuery(interfaces.QueryParam_ImportMode, interfaces.ImportMode_Normal)
	httpErr := validateImportMode(ctx, mode)
	if httpErr != nil {
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Whether to validate dependencies, default true. Parse priority: strict_mode > validate_dependency (legacy) > true
	strictModeStr := c.Query(interfaces.QueryParam_StrictMode)
	if strictModeStr == "" {
		strictModeStr = c.Query("validate_dependency")
	}
	if strictModeStr == "" {
		strictModeStr = "true"
	}
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Bind one knowledge network request object.
	kn := interfaces.KN{}
	err = c.ShouldBindJSON(&kn)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("创建业务知识网络请求参数: [%s,%v]", c.Request.RequestURI, kn))

	// Validate that the imported model is a knowledge network.
	if kn.ModuleType != "" && kn.ModuleType != interfaces.MODULE_TYPE_KN {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
			WithErrorDetails(commonValidationDetail(ctx, "KnowledgeNetworkModuleTypeInvalid", nil))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate required knowledge network creation fields, lengths, and enum values.
	err = ValidateKN(ctx, &kn)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate knowledge network[%s] failed: %s. %v", kn.KNName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("kn_name").String(kn.KNName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate each populated object type, relation type, action type, and concept group in the knowledge network.
	if len(kn.ObjectTypes) > 0 {
		err = ValidateObjectTypes(ctx, kn.KNID, kn.ObjectTypes, strictMode)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.RelationTypes) > 0 {
		err = ValidateRelationTypes(ctx, kn.KNID, kn.RelationTypes, strictMode)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.ActionTypes) > 0 {
		err = ValidateActionTypes(ctx, kn.KNID, kn.ActionTypes, strictMode)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.ConceptGroups) > 0 {
		for _, conceptGroup := range kn.ConceptGroups {
			err = ValidateConceptGroup(ctx, conceptGroup)
			if err != nil {
				httpErr := err.(*rest.HTTPError)
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
		}
	}

	// Create the knowledge network.
	knID, err := r.kns.CreateKN(ctx, &kn, mode, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record an audit log after successful creation.
	audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(visitor),
		interfaces.GenerateKNAuditObject(knID, kn.KNName), "")

	logger.Debug("Handler CreateKN Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusCreated, map[string]any{"id": knID})
}

// ValidateKNByIn validates knowledge network dependencies without persistence (internal).
func (r *restHandler) ValidateKNByIn(c *gin.Context) {
	logger.Debug("Handler ValidateKNByIn Start")
	v := visitor.GenerateVisitor(c)
	r.ValidateKN(c, v)
}

// ValidateKNByEx validates knowledge network dependencies without persistence (external).
func (r *restHandler) ValidateKNByEx(c *gin.Context) {
	logger.Debug("Handler ValidateKNByEx Start")
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ValidateKN(c, visitor)
}

// ValidateKN validates knowledge network dependencies without persistence.
func (r *restHandler) ValidateKN(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler ValidateKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	mode := c.DefaultQuery(interfaces.QueryParam_ImportMode, interfaces.ImportMode_Normal)
	if httpErr := validateImportMode(ctx, mode); httpErr != nil {
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)

	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	kn := interfaces.KN{}
	if err = c.ShouldBindJSON(&kn); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	kn.KNID = knID
	kn.Branch = branch

	if err = ValidateKN(ctx, &kn); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	if len(kn.ObjectTypes) > 0 {
		if err = ValidateObjectTypes(ctx, kn.KNID, kn.ObjectTypes, strictMode); err != nil {
			oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
			rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
			return
		}
	}
	if len(kn.RelationTypes) > 0 {
		if err = ValidateRelationTypes(ctx, kn.KNID, kn.RelationTypes, strictMode); err != nil {
			oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
			rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
			return
		}
	}
	if len(kn.ActionTypes) > 0 {
		if err = ValidateActionTypes(ctx, kn.KNID, kn.ActionTypes, strictMode); err != nil {
			oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
			rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
			return
		}
	}
	if len(kn.ConceptGroups) > 0 {
		for _, cg := range kn.ConceptGroups {
			if err = ValidateConceptGroup(ctx, cg); err != nil {
				oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
				rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
				return
			}
		}
	}
	if err = r.kns.ValidateKN(ctx, &kn, strictMode, mode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]bool{"valid": true})
}

// Update knowledge networks (internal).
func (r *restHandler) UpdateKNByIn(c *gin.Context) {
	logger.Debug("Handler UpdateKNByIn Start")
	// Internal endpoints read user_id from the header.
	visitor := visitor.GenerateVisitor(c)
	r.UpdateKN(c, visitor)
}

// Update knowledge networks (external).
func (r *restHandler) UpdateKNByEx(c *gin.Context) {
	logger.Debug("Handler UpdateKNByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.UpdateKN(c, visitor)
}

// Update a knowledge network.
func (r *restHandler) UpdateKN(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler UpdateKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	// Whether to validate dependencies, default true. Parse priority: strict_mode > validate_dependency (legacy) > true
	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Bind request parameters.
	kn := interfaces.KN{}
	err = c.ShouldBindJSON(&kn)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	kn.KNID = knID
	kn.Branch = branch

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("修改业务知识网络请求参数: [%s, %v]", c.Request.RequestURI, kn))

	// Load the existing resource by ID.
	oldKNName, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate required knowledge network fields, lengths, and enum values.
	err = ValidateKN(ctx, &kn)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate knowledge network[%s] failed: %s. %v", kn.KNName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("kn_name").String(kn.KNName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// When the name or group changes, ensure the new name is available.
	ifNameModify := false
	if oldKNName != kn.KNName {
		ifNameModify = true
		_, exist, err = r.kns.CheckKNExistByName(ctx, kn.KNName, branch)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		if exist {
			httpErr := rest.NewHTTPError(ctx, http.StatusForbidden,
				berrors.BknBackend_KnowledgeNetwork_KNNameExisted)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	kn.IfNameModify = ifNameModify

	// Update the resource by ID.
	err = r.kns.UpdateKN(ctx, nil, &kn, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, audit.UPDATE, audit.TransforOperator(visitor),
		interfaces.GenerateKNAuditObject(knID, kn.KNName), "")

	logger.Debug("Handler UpdateKN Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// Delete knowledge networks in batch.
func (r *restHandler) DeleteKN(c *gin.Context) {
	logger.Debug("Handler DeleteKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("删除业务知识网络请求参数: [%s]", c.Request.RequestURI))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	kn, err := r.kns.GetKNByID(ctx, knID, branch, "")
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if kn == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Delete knowledge networks in batch.
	err = r.kns.DeleteKN(ctx, kn)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record the audit log.
	audit.NewWarnLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(visitor),
		interfaces.GenerateKNAuditObject(knID, kn.KNName), audit.SUCCESS, "")

	logger.Debug("Handler DeleteKN Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// List knowledge networks with pagination (internal).
func (r *restHandler) ListKNsByIn(c *gin.Context) {
	logger.Debug("Handler ListKNsByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.ListKNs(c, visitor)
}

// List knowledge networks with pagination (external).
func (r *restHandler) ListKNsByEx(c *gin.Context) {
	logger.Debug("Handler ListKNsByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ListKNs(c, visitor)
}

// List knowledge networks with pagination.
func (r *restHandler) ListKNs(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("ListKNs Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("分页获取业务知识网络列表请求参数: [%s]", c.Request.RequestURI))

	// Read pagination parameters.
	namePattern := c.Query("name_pattern")
	tag := c.Query("tag")
	offset := c.DefaultQuery("offset", interfaces.DEFAULT_OFFEST)
	limit := c.DefaultQuery("limit", interfaces.DEFAULT_LIMIT)
	sort := c.DefaultQuery("sort", "update_time")
	direction := c.DefaultQuery("direction", interfaces.DESC_DIRECTION)

	// Trim whitespace around tags before searching.
	tag = strings.Trim(tag, " ")

	// Validate pagination query parameters.
	pageParam, err := validatePaginationQueryParameters(ctx,
		offset, limit, sort, direction, interfaces.KN_SORT)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Build the tag-list query parameters.
	parameter := interfaces.KNsQueryParams{
		NamePattern: namePattern,
		Tag:         tag,
		Branch:      interfaces.MAIN_BRANCH,
	}
	parameter.Sort = pageParam.Sort
	parameter.Direction = pageParam.Direction
	parameter.Limit = pageParam.Limit
	parameter.Offset = pageParam.Offset

	// Get knowledge network summaries.
	knList, total, err := r.kns.ListKNs(ctx, parameter)
	result := map[string]any{"entries": knList, "total_count": total}
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler ListKNs Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// Get knowledge network by ID (internal).
func (r *restHandler) GetKNByIn(c *gin.Context) {
	logger.Debug("Handler GetKNByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetKN(c, visitor)
}

// Get knowledge network by ID (external).
func (r *restHandler) GetKNByEx(c *gin.Context) {
	logger.Debug("Handler GetKNByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.GetKN(c, visitor)
}

// Get knowledge network by ID.
func (r *restHandler) GetKN(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	mode := c.DefaultQuery(interfaces.QueryParam_Mode, "")
	if mode != "" && mode != interfaces.Mode_Export {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Mode).
			WithErrorDetails(commonValidationDetail(ctx, "ModeInvalid", map[string]any{"value": mode}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	span.SetAttributes(attr.Key(interfaces.QueryParam_Mode).String(mode))

	// Statistics are optional and disabled by default.
	includeStatistics := c.DefaultQuery("include_statistics", interfaces.DEFAULT_INCLUDE_STATISTICS)
	includeStat, err := strconv.ParseBool(includeStatistics)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_KnowledgeNetwork_InvalidParameter_IncludeStatistics).
			WithErrorDetails(commonValidationDetail(ctx, "IncludeStatisticsInvalid", map[string]any{"value": includeStatistics}))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Get knowledge network details.
	kn, err := r.kns.GetKNByID(ctx, knID, branch, mode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Get concept statistics.
	if includeStat {
		statistics, err := r.kns.GetStatByKN(ctx, kn)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		kn.Statistics = statistics
	}

	// Trim heavy fields at the source when detail_level=summary; full remains the backward-compatible default.
	// Fetch complete field mappings on demand from the object-types/:ot_ids and relation-types/:rt_ids endpoints.
	if c.DefaultQuery(interfaces.QueryParam_DetailLevel, interfaces.DetailLevel_Full) == interfaces.DetailLevel_Summary {
		kn.SlimForSummary()
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler GetKN Success")
	rest.ReplyOK(c, http.StatusOK, kn)
}

func (r *restHandler) GetRelationTypePathsByIn(c *gin.Context) {
	logger.Debug("Handler GetRelationTypePathsByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetRelationTypePaths(c, visitor)
}

// Find a concept subgraph in a knowledge network (external).
func (r *restHandler) GetRelationTypePathsByEx(c *gin.Context) {
	logger.Debug("Handler GetRelationTypePathsByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.GetRelationTypePaths(c, visitor)
}

// Find a concept subgraph in a knowledge network.
func (r *restHandler) GetRelationTypePaths(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetRelationTypePaths Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	// Bind request parameters.
	query := interfaces.RelationTypePathsBaseOnSource{}
	err := c.ShouldBindJSON(&query)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		rest.ReplyError(c, httpErr)
		return
	}

	query.KNID = knID
	query.Branch = branch

	// Validate x-http-method-override.
	err = ValidateHeaderMethodOverride(ctx, c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE))
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// The path length defaults to one hop and supports up to three hops.
	err = ValidateRelationTypePathsQuery(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Verify that the knowledge network exists.
	kn, err := r.kns.GetKNByID(ctx, knID, branch, "")
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if kn == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound).
			WithErrorDetails(commonValidationDetail(ctx, "KnowledgeNetworkNotFound", map[string]any{"knowledgeNetworkID": knID}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Get knowledge network details.
	result, err := r.kns.GetRelationTypePaths(ctx, query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	httpResult := map[string]any{"entries": result}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler GetKN Success")
	rest.ReplyOK(c, http.StatusOK, httpResult)
}

// QueryKNNamesByIDs resolves knowledge network names by ID in batch for object-level authorization views.
// Requests use {"ids":[...]}; responses use {"entries":[{"id","name"}]}. Missing IDs are skipped and empty input returns empty entries.
// Authorization views must show referenced knowledge network names even without access, so this endpoint skips network filtering but still requires OAuth authentication.
func (r *restHandler) QueryKNNamesByIDs(c *gin.Context) {
	logger.Debug("Handler QueryKNNamesByIDs Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	if _, err := r.verifyOAuth(ctx, c); err != nil {
		return
	}

	req := interfaces.KNBatchNamesReq{}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if len(req.IDs) > interfaces.KN_BATCH_NAMES_MAX_IDS {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "IDsCountExceeded", map[string]any{"limit": interfaces.KN_BATCH_NAMES_MAX_IDS}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	resp, err := r.kns.GetKNNamesByIDs(ctx, req.IDs)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler QueryKNNamesByIDs Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, resp)
}

// List knowledge network resources with pagination.
func (r *restHandler) ListKnSrcs(c *gin.Context) {
	logger.Debug("tHandler ListKnSrcs Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("分页获取业务知识网络资源实例列表请求参数: [%s]", c.Request.RequestURI))

	// Read pagination parameters.
	namePattern := c.Query(RESOURCES_KEYWOED) // The unified resource platform uses keyword for resource-list searches.
	offset := c.DefaultQuery("offset", interfaces.DEFAULT_OFFEST)
	limit := c.DefaultQuery("limit", interfaces.DEFAULT_LIMIT)
	sort := c.DefaultQuery("sort", "name")
	direction := c.DefaultQuery("direction", interfaces.DESC_DIRECTION)

	// Validate pagination query parameters.
	pageParam, err := validatePaginationQueryParameters(ctx,
		offset, limit, sort, direction, interfaces.KN_SORT)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Build the tag-list query parameters.
	parameter := interfaces.KNsQueryParams{
		NamePattern: namePattern,
	}
	parameter.Sort = pageParam.Sort
	parameter.Direction = pageParam.Direction
	parameter.Limit = pageParam.Limit
	parameter.Offset = pageParam.Offset

	// Get knowledge network summaries.
	resources, total, err := r.kns.ListKnSrcs(ctx, parameter)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	result := map[string]interface{}{"entries": resources, "total_count": total}

	logger.Debug("Handler ListKnSrcs Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}
