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

	"bkn-backend/common"
	"bkn-backend/common/visitor"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func (r *restHandler) HandleActionTypeGetOverrideByIn(c *gin.Context) {
	switch c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE) {
	case "", http.MethodPost:
		r.CreateActionTypesByIn(c)
	case http.MethodGet:
		r.SearchActionTypesByIn(c)
	default:
		httpErr := rest.NewHTTPError(rest.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_OverrideMethod)
		rest.ReplyError(c, httpErr)
	}
}

func (r *restHandler) HandleActionTypeGetOverrideByEx(c *gin.Context) {
	switch c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE) {
	case "", http.MethodPost:
		r.CreateActionTypesByEx(c)
	case http.MethodGet:
		r.SearchActionTypesByEx(c)
	default:
		httpErr := rest.NewHTTPError(rest.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_OverrideMethod)
		rest.ReplyError(c, httpErr)
	}
}

// Create action types (internal).
func (r *restHandler) CreateActionTypesByIn(c *gin.Context) {
	logger.Debug("Handler CreateActionTypesByIn Start")
	// Internal endpoints read account_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.CreateActionTypes(c, visitor)
}

// Create action types (external).
func (r *restHandler) CreateActionTypesByEx(c *gin.Context) {
	logger.Debug("Handler CreateActionTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.CreateActionTypes(c, visitor)
}

// Create action types.
func (r *restHandler) CreateActionTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler CreateActionTypes Start")
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

	// Read query parameters.
	mode := c.DefaultQuery(interfaces.QueryParam_ImportMode, interfaces.ImportMode_Normal)
	httpErr := validateImportMode(ctx, mode)
	if httpErr != nil {
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Whether to validate dependencies, default true. Parse priority: strict_mode > validate_dependency (legacy) > true
	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	// Verify that the knowledge network exists.
	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
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

	// Bind request parameters.
	var requestData struct {
		Entries []*interfaces.ActionType `json:"entries"`
	}
	err = c.ShouldBindJSON(&requestData)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	actionTypes := requestData.Entries

	// Reject an empty entries array.
	if len(actionTypes) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody).
			WithErrorDetails(commonValidationDetail(ctx, "EntriesRequired", nil))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("创建行动类请求参数: [%s,%v]", c.Request.RequestURI, actionTypes))

	// Apply the branch from the URL to all requested action types.
	for i := range actionTypes {
		actionTypes[i].KNID = knID
		actionTypes[i].Branch = branch
	}

	// Validate model names in the request body.
	err = ValidateActionTypes(ctx, knID, actionTypes, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Create the resources.
	atIDs, err := r.ats.CreateActionTypes(ctx, nil, actionTypes, mode, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Return the created resources.
	for _, actionType := range actionTypes {
		// Record an audit log after each successful creation.
		audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(visitor),
			interfaces.GenerateActionTypeAuditObject(actionType.ATID, actionType.ATName), "")
	}

	result := []any{}
	for _, atID := range atIDs {
		result = append(result, map[string]any{"id": atID})
	}

	logger.Debug("Handler CreateActionTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// ValidateActionTypesByIn validates action type dependencies without persistence (internal).
func (r *restHandler) ValidateActionTypesByIn(c *gin.Context) {
	logger.Debug("Handler ValidateActionTypesByIn Start")
	v := visitor.GenerateVisitor(c)
	r.ValidateActionTypesForKN(c, v)
}

// ValidateActionTypesByEx validates action type dependencies without persistence (external).
func (r *restHandler) ValidateActionTypesByEx(c *gin.Context) {
	logger.Debug("Handler ValidateActionTypesByEx Start")
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ValidateActionTypesForKN(c, visitor)
}

// ValidateActionTypesForKN validates action type dependencies without persistence.
func (r *restHandler) ValidateActionTypesForKN(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler ValidateActionTypesForKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
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

	var requestData struct {
		Entries []*interfaces.ActionType `json:"entries"`
	}
	if err = c.ShouldBindJSON(&requestData); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	actionTypes := requestData.Entries
	if len(actionTypes) == 0 {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]bool{"valid": true})
		return
	}

	// Apply the branch from the URL to all requested action types.
	for i := range actionTypes {
		actionTypes[i].KNID = knID
		actionTypes[i].Branch = branch
	}

	if err = ValidateActionTypes(ctx, knID, actionTypes, strictMode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	if err = r.ats.ValidateActionTypes(ctx, knID, branch, actionTypes, strictMode, nil, mode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": true})
}

// Update action types (internal).
func (r *restHandler) UpdateActionTypeByIn(c *gin.Context) {
	logger.Debug("Handler UpdateActionTypeByIn Start")
	// Internal endpoints read account_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.UpdateActionType(c, visitor)
}

// Update action types (external).
func (r *restHandler) UpdateActionTypeByEx(c *gin.Context) {
	logger.Debug("Handler UpdateActionTypeByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.UpdateActionType(c, visitor)
}

// Update action types.
func (r *restHandler) UpdateActionType(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler UpdateActionType Start")
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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Verify that the knowledge network exists.
	var exist bool
	_, exist, err = r.kns.CheckKNExistByID(ctx, knID, branch)
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

	// Read the at_id path parameter.
	atID := c.Param("at_id")
	span.SetAttributes(attr.Key("at_id").String(atID))

	// Bind request parameters.
	actionType := interfaces.ActionType{}
	err = c.ShouldBindJSON(&actionType)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	actionType.ATID = atID
	actionType.KNID = knID
	actionType.Branch = branch

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("修改行动类请求参数: [%s, %v]", c.Request.RequestURI, actionType))

	// Load the existing resource by ID.
	oldATName, exist, err := r.ats.CheckActionTypeExistByID(ctx, knID, branch, atID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionType_ActionTypeNotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate required action type fields, lengths, and enum values.
	err = ValidateActionType(ctx, &actionType, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate action type[%s] failed: %s. %v", actionType.ATName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("at_name").String(actionType.ATName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// When the name or group changes, ensure the new name is available.
	ifNameModify := false
	if oldATName != actionType.ATName {
		ifNameModify = true
		_, exist, err = r.ats.CheckActionTypeExistByName(ctx, knID, branch, actionType.ATName)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		if exist {
			httpErr := rest.NewHTTPError(ctx, http.StatusForbidden,
				berrors.BknBackend_ActionType_ActionTypeNameExisted)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	actionType.IfNameModify = ifNameModify

	// Update the resource by ID.
	err = r.ats.UpdateActionType(ctx, nil, &actionType, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, audit.UPDATE, audit.TransforOperator(visitor),
		interfaces.GenerateActionTypeAuditObject(atID, actionType.ATName), "")

	logger.Debug("Handler UpdateActionType Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// Delete action types in batch.
func (r *restHandler) DeleteActionTypes(c *gin.Context) {
	logger.Debug("Handler DeleteActionTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("删除行动类请求参数: [%s]", c.Request.RequestURI))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	// Verify that the knowledge network exists.
	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
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

	// Read the comma-separated ID list.
	atIDsStr := c.Param("at_ids")
	span.SetAttributes(attr.Key("at_ids").String(atIDsStr))

	// Parse the string into []string.
	atIDs := common.StringToStringSlice(atIDsStr)

	// Check that all action type IDs exist.
	var actionTypes []*interfaces.ActionTypeWithKeyField
	for _, atID := range atIDs {
		atName, exist, err := r.ats.CheckActionTypeExistByID(ctx, knID, branch, atID)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)

			rest.ReplyError(c, httpErr)
			return
		}
		if !exist {
			httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionType_ActionTypeNotFound)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}

		actionTypes = append(actionTypes, &interfaces.ActionTypeWithKeyField{ATID: atID, ATName: atName})
	}

	// Delete action types in batch.
	err = r.ats.DeleteActionTypesByIDs(ctx, nil, knID, branch, atIDs)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record audit logs for each item.
	for _, actionType := range actionTypes {
		audit.NewWarnLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(visitor),
			interfaces.GenerateActionTypeAuditObject(actionType.ATID, actionType.ATName), audit.SUCCESS, "")
	}

	logger.Debug("Handler DeleteActionTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// List action types with pagination (internal).
func (r *restHandler) ListActionTypesByIn(c *gin.Context) {
	logger.Debug("Handler ListActionTypesByIn Start")
	// Internal endpoints read account_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.ListActionTypes(c, visitor)
}

// List action types with pagination (external).
func (r *restHandler) ListActionTypesByEx(c *gin.Context) {
	logger.Debug("Handler ListActionTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ListActionTypes(c, visitor)
}

// List action types with pagination.
func (r *restHandler) ListActionTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("ListActionTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("分页获取行动类列表请求参数: [%s]", c.Request.RequestURI))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	// Verify that the knowledge network exists.
	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
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

	// Read pagination parameters.
	namePattern := c.Query("name_pattern")
	tag := c.Query("tag")
	objectTypeID := c.Query("object_type_id")
	actionType := c.Query("action_type")
	offset := c.DefaultQuery("offset", interfaces.DEFAULT_OFFEST)
	limit := c.DefaultQuery("limit", interfaces.DEFAULT_LIMIT)
	sort := c.DefaultQuery("sort", "update_time")
	direction := c.DefaultQuery("direction", interfaces.DESC_DIRECTION)

	// Trim whitespace around tags before searching.
	tag = strings.Trim(tag, " ")

	// Validate pagination query parameters.
	pageParam, err := validatePaginationQueryParameters(ctx,
		offset, limit, sort, direction, interfaces.ACTION_TYPE_SORT)
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
	parameter := interfaces.ActionTypesQueryParams{
		NamePattern: namePattern,
		Tag:         tag,
		Branch:      branch,
		KNID:        knID,
		ActionType:  actionType,
	}
	if objectTypeID != "" {
		parameter.ObjectTypeIDs = []string{objectTypeID}
	}
	parameter.Sort = pageParam.Sort
	parameter.Direction = pageParam.Direction
	parameter.Limit = pageParam.Limit
	parameter.Offset = pageParam.Offset

	// var result map[string]any
	// if simpleInfo {
	// Get action type summaries.
	otList, total, err := r.ats.ListActionTypes(ctx, parameter)
	result := map[string]any{"entries": otList, "total_count": total}
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

	logger.Debug("Handler ListActionTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	emitActionTypeSchemaRead(ctx, c, visitor, "bkn.schema.action_type.list", knID, branch, nil, otList, int64(total))
	rest.ReplyOK(c, http.StatusOK, result)
}

// Get action type by ID (internal).
func (r *restHandler) GetActionTypesByIn(c *gin.Context) {
	logger.Debug("Handler GetActionTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetActionTypes(c, visitor)
}

// Get action type by ID (external).
func (r *restHandler) GetActionTypesByEx(c *gin.Context) {
	logger.Debug("Handler ListActionTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.GetActionTypes(c, visitor)
}

// Get action type by ID.
func (r *restHandler) GetActionTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetActionTypes Start")
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

	// Verify that the knowledge network exists.
	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
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

	// Read the ID list.
	atIDsStr := c.Param("at_ids")
	span.SetAttributes(attr.Key("at_ids").String(atIDsStr))

	// Parse the string into []string.
	atIDs := common.StringToStringSlice(atIDsStr)

	// Get action type details; include data-view filters only when include_view is set.
	actionTypes, err := r.ats.GetActionTypesByIDs(ctx, knID, branch, atIDs)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	httpResult := map[string]any{"entries": actionTypes}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler GetActionTypes Success")
	emitActionTypeSchemaRead(ctx, c, visitor, "bkn.schema.action_type.get", knID, branch, atIDs, actionTypes, int64(len(actionTypes)))
	rest.ReplyOK(c, http.StatusOK, httpResult)
}

// Search relation types (external).
func (r *restHandler) SearchActionTypesByIn(c *gin.Context) {
	logger.Debug("Handler SearchActionTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.SearchActionTypes(c, visitor)
}

// Search relation types (external).
func (r *restHandler) SearchActionTypesByEx(c *gin.Context) {
	logger.Debug("Handler SearchActionTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.SearchActionTypes(c, visitor)
}

// Search action types.
func (r *restHandler) SearchActionTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("SearchActionTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("检索行动类请求参数: [%s]", c.Request.RequestURI))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	// Verify that the knowledge network exists.
	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
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

	// Bind request parameters.
	query := interfaces.ConceptsQuery{}
	err = c.ShouldBindJSON(&query)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	query.KNID = knID
	query.Branch = branch
	query.ModuleType = interfaces.MODULE_TYPE_ACTION_TYPE

	// TODO: validate concept type values and restrict filter fields to the supported allowlist.
	if query.Limit == 0 {
		query.Limit = interfaces.DEFAULT_CONCEPT_SEARCH_LIMIT
	}

	if query.Sort == nil {
		query.Sort = []*interfaces.SortParams{
			{
				Field:     interfaces.OPENSEARCH_SCORE_FIELD,
				Direction: interfaces.DESC_DIRECTION,
			},
			{
				Field:     interfaces.CONCEPT_ID_FIELD,
				Direction: interfaces.ASC_DIRECTION,
			},
		}
	}

	err = validateConceptsQuery(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Search concepts.
	result, err := r.ats.SearchActionTypes(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler SearchActionTypes Success")
	rest.ReplyOK(c, http.StatusOK, result)
}
