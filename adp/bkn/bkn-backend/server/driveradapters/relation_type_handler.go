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

func (r *restHandler) HandleRelationTypeGetOverrideByIn(c *gin.Context) {
	switch c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE) {
	case "", http.MethodPost:
		r.CreateRelationTypesByIn(c)
	case http.MethodGet:
		r.SearchRelationTypesByIn(c)
	default:
		httpErr := rest.NewHTTPError(rest.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_OverrideMethod)
		rest.ReplyError(c, httpErr)
	}
}

func (r *restHandler) HandleRelationTypeGetOverrideByEx(c *gin.Context) {
	switch c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE) {
	case "", http.MethodPost:
		r.CreateRelationTypesByEx(c)
	case http.MethodGet:
		r.SearchRelationTypesByEx(c)
	default:
		httpErr := rest.NewHTTPError(rest.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_OverrideMethod)
		rest.ReplyError(c, httpErr)
	}
}

// Create relation types (internal).
func (r *restHandler) CreateRelationTypesByIn(c *gin.Context) {
	logger.Debug("Handler CreateRelationTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.CreateRelationTypes(c, visitor)
}

// Create relation types (external).
func (r *restHandler) CreateRelationTypesByEx(c *gin.Context) {
	logger.Debug("Handler CreateRelationTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.CreateRelationTypes(c, visitor)
}

// Create relation types.
func (r *restHandler) CreateRelationTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler CreateRelationTypes Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account type in the context.
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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

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
		Entries []*interfaces.RelationType `json:"entries"`
	}
	err = c.ShouldBindJSON(&requestData)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	relationTypes := requestData.Entries

	// Reject an empty entries array.
	if len(relationTypes) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody).
			WithErrorDetails(commonValidationDetail(ctx, "EntriesRequired", nil))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("创建关系类请求参数: [%s,%v]", c.Request.RequestURI, relationTypes))

	// Apply the branch from the URL to all requested relation types.
	for i := range relationTypes {
		relationTypes[i].KNID = knID
		relationTypes[i].Branch = branch
	}

	// Validate model names in the request body.
	err = ValidateRelationTypes(ctx, knID, relationTypes, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Create the resources.
	// Direct relation type creation validates dependencies by default.
	rtIDs, err := r.rts.CreateRelationTypes(ctx, nil, relationTypes, mode, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Return the created resources.
	for _, relationType := range relationTypes {
		// Record an audit log after each successful creation.
		audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(visitor),
			interfaces.GenerateRelationTypeAuditObject(relationType.RTID, relationType.RTName), "")
	}

	result := []any{}
	for _, rtID := range rtIDs {
		result = append(result, map[string]any{"id": rtID})
	}

	logger.Debug("Handler CreateRelationTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// ValidateRelationTypesByIn validates relation type dependencies without persistence (internal).
func (r *restHandler) ValidateRelationTypesByIn(c *gin.Context) {
	logger.Debug("Handler ValidateRelationTypesByIn Start")
	v := visitor.GenerateVisitor(c)
	r.ValidateRelationTypesForKN(c, v)
}

// ValidateRelationTypesByEx validates relation type dependencies without persistence (external).
func (r *restHandler) ValidateRelationTypesByEx(c *gin.Context) {
	logger.Debug("Handler ValidateRelationTypesByEx Start")
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ValidateRelationTypesForKN(c, visitor)
}

// ValidateRelationTypesForKN validates relation type dependencies without persistence.
func (r *restHandler) ValidateRelationTypesForKN(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler ValidateRelationTypesForKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
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
		Entries []*interfaces.RelationType `json:"entries"`
	}
	if err = c.ShouldBindJSON(&requestData); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	relationTypes := requestData.Entries
	if len(relationTypes) == 0 {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": true})
		return
	}

	// Apply the branch from the URL to all requested relation types.
	for i := range relationTypes {
		relationTypes[i].KNID = knID
		relationTypes[i].Branch = branch
	}

	if err = ValidateRelationTypes(ctx, knID, relationTypes, strictMode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	if err = r.rts.ValidateRelationTypes(ctx, knID, branch, relationTypes, strictMode, nil, mode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": true})
}

// Update relation types (internal).
func (r *restHandler) UpdateRelationTypeByIn(c *gin.Context) {
	logger.Debug("Handler UpdateRelationTypeByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.UpdateRelationType(c, visitor)
}

// Update relation types (external).
func (r *restHandler) UpdateRelationTypeByEx(c *gin.Context) {
	logger.Debug("Handler UpdateRelationTypeByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.UpdateRelationType(c, visitor)
}

// Update relation types.
func (r *restHandler) UpdateRelationType(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler UpdateRelationType Start")
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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
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

	// Read the rt_id path parameter.
	rtID := c.Param("rt_id")
	span.SetAttributes(attr.Key("rt_id").String(rtID))

	// Bind request parameters.
	relationType := interfaces.RelationType{}
	err = c.ShouldBindJSON(&relationType)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	relationType.RTID = rtID
	relationType.KNID = knID
	relationType.Branch = branch

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("修改关系类请求参数: [%s, %v]", c.Request.RequestURI, relationType))

	// Load the existing resource by ID.
	_, exist, err = r.rts.CheckRelationTypeExistByID(ctx, knID, branch, rtID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_RelationType_RelationTypeNotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate required relation type fields, lengths, and enum values.
	err = ValidateRelationType(ctx, &relationType, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate relation type[%s] failed: %s. %v", relationType.RTName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("rt_name").String(relationType.RTName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Update the resource by ID.
	err = r.rts.UpdateRelationType(ctx, nil, &relationType, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, audit.UPDATE, audit.TransforOperator(visitor),
		interfaces.GenerateRelationTypeAuditObject(rtID, relationType.RTName), "")

	logger.Debug("Handler UpdateRelationType Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// Delete relation types in batch.
func (r *restHandler) DeleteRelationTypes(c *gin.Context) {
	logger.Debug("Handler DeleteRelationTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("删除关系类请求参数: [%s]", c.Request.RequestURI))

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
	otIDsStr := c.Param("rt_ids")
	span.SetAttributes(attr.Key("rt_ids").String(otIDsStr))

	// Parse the string into []string.
	rtIDs := common.StringToStringSlice(otIDsStr)

	// Check that all relation type IDs exist.
	var relationTypes []*interfaces.RelationTypeWithKeyField
	for _, rtID := range rtIDs {
		rtName, exist, err := r.rts.CheckRelationTypeExistByID(ctx, knID, branch, rtID)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)

			rest.ReplyError(c, httpErr)
			return
		}
		if !exist {
			httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_RelationType_RelationTypeNotFound)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}

		relationTypes = append(relationTypes, &interfaces.RelationTypeWithKeyField{RTID: rtID, RTName: rtName})
	}

	// Delete relation types in batch.
	err = r.rts.DeleteRelationTypesByIDs(ctx, nil, knID, branch, rtIDs)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record audit logs for each item.
	for _, relationType := range relationTypes {
		audit.NewWarnLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(visitor),
			interfaces.GenerateRelationTypeAuditObject(relationType.RTID, relationType.RTName), audit.SUCCESS, "")
	}

	logger.Debug("Handler DeleteRelationTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// List relation types with pagination (internal).
func (r *restHandler) ListRelationTypesByIn(c *gin.Context) {
	logger.Debug("Handler ListRelationTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.ListRelationTypes(c, visitor)
}

// List relation types with pagination (external).
func (r *restHandler) ListRelationTypesByEx(c *gin.Context) {
	logger.Debug("Handler ListRelationTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ListRelationTypes(c, visitor)
}

// List relation types with pagination.
func (r *restHandler) ListRelationTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("ListRelationTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("分页获取关系类列表请求参数: [%s]", c.Request.RequestURI))

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
	sourceObjectTypeIDs := c.QueryArray("source_object_type_id")
	targetObjectTypeIDs := c.QueryArray("target_object_type_id")
	boundObjectTypeIDs := c.QueryArray("bound_object_type_id")

	offset := c.DefaultQuery("offset", interfaces.DEFAULT_OFFEST)
	limit := c.DefaultQuery("limit", interfaces.DEFAULT_LIMIT)
	sort := c.DefaultQuery("sort", "update_time")
	direction := c.DefaultQuery("direction", interfaces.DESC_DIRECTION)

	// Trim whitespace around tags before searching.
	tag = strings.Trim(tag, " ")

	// Validate pagination query parameters.
	pageParam, err := validatePaginationQueryParameters(ctx,
		offset, limit, sort, direction, interfaces.RELATION_TYPE_SORT)
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
	parameter := interfaces.RelationTypesQueryParams{
		NamePattern: namePattern,
		Tag:         tag,
		Branch:      branch,
		KNID:        knID,
	}

	// Assign the value when it is present.
	if len(sourceObjectTypeIDs) > 0 {
		parameter.SourceObjectTypeIDs = sourceObjectTypeIDs
	}
	if len(targetObjectTypeIDs) > 0 {
		parameter.TargetObjectTypeIDs = targetObjectTypeIDs
	}
	if len(boundObjectTypeIDs) > 0 {
		parameter.BoundObjectTypeIDs = boundObjectTypeIDs
	}

	parameter.Sort = pageParam.Sort
	parameter.Direction = pageParam.Direction
	parameter.Limit = pageParam.Limit
	parameter.Offset = pageParam.Offset

	// var result map[string]any
	// if simpleInfo {
	// Get relation type summaries.
	otList, total, err := r.rts.ListRelationTypes(ctx, parameter)
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

	logger.Debug("Handler ListRelationTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	emitRelationTypeSchemaRead(ctx, c, visitor, "bkn.schema.relation_type.list", knID, branch, nil, otList, int64(total))
	rest.ReplyOK(c, http.StatusOK, result)
}

// Get relation type by ID (internal).
func (r *restHandler) GetRelationTypesByIn(c *gin.Context) {
	logger.Debug("Handler GetRelationTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetRelationTypes(c, visitor)
}

// Get relation type by ID (external).
func (r *restHandler) GetRelationTypesByEx(c *gin.Context) {
	logger.Debug("Handler ListRelationTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.GetRelationTypes(c, visitor)
}

// Get relation type by ID.
func (r *restHandler) GetRelationTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetRelationTypes Start")
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
	rtIDsStr := c.Param("rt_ids")
	span.SetAttributes(attr.Key("rt_ids").String(rtIDsStr))

	// Parse the string into []string.
	rtIDs := common.StringToStringSlice(rtIDsStr)

	// Get relation type details; include data-view filters only when include_view is set.
	result, err := r.rts.GetRelationTypesByIDs(ctx, knID, branch, rtIDs)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	httpResult := map[string]any{"entries": result}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler GetRelationTypes Success")
	emitRelationTypeSchemaRead(ctx, c, visitor, "bkn.schema.relation_type.get", knID, branch, rtIDs, result, int64(len(result)))
	rest.ReplyOK(c, http.StatusOK, httpResult)
}

// Search relation types (external).
func (r *restHandler) SearchRelationTypesByIn(c *gin.Context) {
	logger.Debug("Handler SearchRelationTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.SearchRelationTypes(c, visitor)
}

// Search relation types (external).
func (r *restHandler) SearchRelationTypesByEx(c *gin.Context) {
	logger.Debug("Handler SearchRelationTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.SearchRelationTypes(c, visitor)
}

// Search object types.
func (r *restHandler) SearchRelationTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("SearchRelationTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("检索对象类请求参数: [%s]", c.Request.RequestURI))

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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	query.KNID = knID
	query.Branch = branch
	query.ModuleType = interfaces.MODULE_TYPE_RELATION_TYPE

	// Validate concept type values and restrict filter fields to the supported allowlist.
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
	result, err := r.rts.SearchRelationTypes(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler SearchRelationTypes Success")
	rest.ReplyOK(c, http.StatusOK, result)
}
