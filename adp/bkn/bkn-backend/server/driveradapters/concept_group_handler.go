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

// Create concept groups (internal).
func (r *restHandler) CreateConceptGroupByIn(c *gin.Context) {
	logger.Debug("Handler CreateConceptGroupByIn Start")
	// Internal endpoints read user_id from the header.
	visitor := visitor.GenerateVisitor(c)
	r.CreateConceptGroup(c, visitor)
}

// Create concept groups (external).
func (r *restHandler) CreateConceptGroupByEx(c *gin.Context) {
	logger.Debug("Handler CreateConceptGroupByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.CreateConceptGroup(c, visitor)
}

// Create concept groups.
func (r *restHandler) CreateConceptGroup(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler CreateConceptGroup Start")
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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
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

	// Bind one concept group request object.
	cg := interfaces.ConceptGroup{}
	err = c.ShouldBindJSON(&cg)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	cg.KNID = knID
	cg.Branch = branch

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("创建概念分组请求参数: [%s,%v]", c.Request.RequestURI, cg))

	// Validate that the imported model is a concept group.
	if cg.ModuleType != "" && cg.ModuleType != interfaces.MODULE_TYPE_CONCEPT_GROUP {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
			WithErrorDetails(commonValidationDetail(ctx, "ConceptGroupModuleTypeInvalid", nil))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate required concept group creation fields, lengths, and enum values.
	err = ValidateConceptGroup(ctx, &cg)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate concept group[%s] failed: %s. %v", cg.CGName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("cg_name").String(cg.CGName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate each populated object type, relation type, action type, and concept group in the knowledge network.
	if len(cg.ObjectTypes) > 0 {
		err = ValidateObjectTypes(ctx, cg.KNID, cg.ObjectTypes, strictMode)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(cg.RelationTypes) > 0 {
		err = ValidateRelationTypes(ctx, cg.KNID, cg.RelationTypes, strictMode)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(cg.ActionTypes) > 0 {
		err = ValidateActionTypes(ctx, cg.KNID, cg.ActionTypes, strictMode)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}

	// Create the concept group.
	cgID, err := r.cgs.CreateConceptGroup(ctx, nil, &cg, mode, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record an audit log after successful creation.
	audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(visitor),
		interfaces.GenerateConceptGroupAuditObject(knID, cg.CGName), "")

	logger.Debug("Handler CreateConceptGroup Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusCreated, map[string]any{"id": cgID})
}

// ValidateConceptGroupsByIn validates concept group dependencies without persistence (internal).
func (r *restHandler) ValidateConceptGroupsByIn(c *gin.Context) {
	logger.Debug("Handler ValidateConceptGroupsByIn Start")
	v := visitor.GenerateVisitor(c)
	r.ValidateConceptGroups(c, v)
}

// ValidateConceptGroupsByEx validates concept group dependencies without persistence (external).
func (r *restHandler) ValidateConceptGroupsByEx(c *gin.Context) {
	logger.Debug("Handler ValidateConceptGroupsByEx Start")
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ValidateConceptGroups(c, visitor)
}

// ValidateConceptGroups validates concept group dependencies without persistence.
func (r *restHandler) ValidateConceptGroups(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler ValidateConceptGroups Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
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
		Entries []*interfaces.ConceptGroup `json:"entries"`
	}
	if err = c.ShouldBindJSON(&requestData); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	conceptGroups := requestData.Entries
	if len(conceptGroups) == 0 {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]bool{"valid": true})
		return
	}

	for _, cg := range conceptGroups {
		cg.KNID = knID
		cg.Branch = branch
	}

	for _, cg := range conceptGroups {
		if err = ValidateConceptGroup(ctx, cg); err != nil {
			oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
			rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
			return
		}
	}
	if err = r.cgs.ValidateConceptGroups(ctx, knID, branch, conceptGroups, strictMode, nil, mode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]bool{"valid": true})
}

// Update concept groups (internal).
func (r *restHandler) UpdateConceptGroupByIn(c *gin.Context) {
	logger.Debug("Handler UpdateConceptGroupByIn Start")
	// Internal endpoints read user_id from the header.
	visitor := visitor.GenerateVisitor(c)
	r.UpdateConceptGroup(c, visitor)
}

// Update concept groups (external).
func (r *restHandler) UpdateConceptGroupByEx(c *gin.Context) {
	logger.Debug("Handler UpdateConceptGroupByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.UpdateConceptGroup(c, visitor)
}

// Update concept groups.
func (r *restHandler) UpdateConceptGroup(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler UpdateConceptGroup Start")
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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

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
	cgID := c.Param("cg_id")
	span.SetAttributes(attr.Key("cg_id").String(cgID))

	// Bind request parameters.
	cg := interfaces.ConceptGroup{}
	err = c.ShouldBindJSON(&cg)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	cg.CGID = cgID
	cg.KNID = knID
	cg.Branch = branch // Read the concept group branch from the query parameter.

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("修改概念分组请求参数: [%s, %v]", c.Request.RequestURI, cg))

	// Load the existing resource by ID..
	oldKNName, exist, err := r.cgs.CheckConceptGroupExistByID(ctx, knID, branch, cgID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ConceptGroup_ConceptGroupNotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate required concept group fields, lengths, and enum values.
	err = ValidateConceptGroup(ctx, &cg)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate concept group[%s] failed: %s. %v", cg.CGName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("kn_name").String(cg.CGName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// When the name or group changes, ensure the new name is available.
	ifNameModify := false
	if oldKNName != cg.CGName {
		ifNameModify = true
		_, exist, err = r.cgs.CheckConceptGroupExistByName(ctx, knID, branch, cg.CGName)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		if exist {
			httpErr := rest.NewHTTPError(ctx, http.StatusForbidden,
				berrors.BknBackend_ConceptGroup_ConceptGroupNameExisted)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	cg.IfNameModify = ifNameModify

	// Update the resource by ID.
	err = r.cgs.UpdateConceptGroup(ctx, nil, &cg, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, audit.UPDATE, audit.TransforOperator(visitor),
		interfaces.GenerateConceptGroupAuditObject(knID, cg.CGName), "")

	logger.Debug("Handler UpdateConceptGroup Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// Delete concept groups in batch.
func (r *restHandler) DeleteConceptGroup(c *gin.Context) {
	logger.Debug("Handler DeleteConceptGroup Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("删除概念分组请求参数: [%s]", c.Request.RequestURI))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

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
	cgID := c.Param("cg_id")
	span.SetAttributes(attr.Key("cg_id").String(cgID))

	// Check that all action type IDs exist.
	cgName, exist, err := r.cgs.CheckConceptGroupExistByID(ctx, knID, branch, cgID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)

		rest.ReplyError(c, httpErr)
		return
	}
	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ConceptGroup_ConceptGroupNotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Delete concept groups in batch.
	err = r.cgs.DeleteConceptGroupByID(ctx, nil, knID, branch, cgID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record audit logs for each item.
	audit.NewWarnLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(visitor),
		interfaces.GenerateConceptGroupAuditObject(knID, cgName), audit.SUCCESS, "")

	logger.Debug("Handler DeleteConceptGroup Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// List concept groups with pagination (internal).
func (r *restHandler) ListConceptGroupsByIn(c *gin.Context) {
	logger.Debug("Handler ListConceptGroupsByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.ListConceptGroups(c, visitor)
}

// List concept groups with pagination (external).
func (r *restHandler) ListConceptGroupsByEx(c *gin.Context) {
	logger.Debug("Handler ListConceptGroupsByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ListConceptGroups(c, visitor)
}

// List concept groups with pagination.
func (r *restHandler) ListConceptGroups(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("ListConceptGroups Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("分页获取概念分组列表请求参数: [%s]", c.Request.RequestURI))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

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
	parameter := interfaces.ConceptGroupsQueryParams{
		NamePattern: namePattern,
		Tag:         tag,
		KNID:        knID,
		Branch:      branch,
	}
	parameter.Sort = pageParam.Sort
	parameter.Direction = pageParam.Direction
	parameter.Limit = pageParam.Limit
	parameter.Offset = pageParam.Offset

	// Get concept group summaries.
	knList, total, err := r.cgs.ListConceptGroups(ctx, parameter)
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

	logger.Debug("Handler ListConceptGroups Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// Get concept group by ID (internal).
func (r *restHandler) GetConceptGroupByIn(c *gin.Context) {
	logger.Debug("Handler GetKNByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetConceptGroup(c, visitor)
}

// Get concept group by ID (external).
func (r *restHandler) GetConceptGroupByEx(c *gin.Context) {
	logger.Debug("Handler GetKNByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.GetConceptGroup(c, visitor)
}

// Get concept group by ID.
func (r *restHandler) GetConceptGroup(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetConceptGroup Start")
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
			berrors.BknBackend_ConceptGroup_InvalidParameter_IncludeStatistics).
			WithErrorDetails(commonValidationDetail(ctx, "IncludeStatisticsInvalid", map[string]any{"value": includeStatistics}))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Read the ID list for one concept group.
	cgID := c.Param("cg_id")
	span.SetAttributes(attr.Key("cg_id").String(cgID))

	// Get concept group details.
	cg, err := r.cgs.GetConceptGroupByID(ctx, knID, branch, cgID, mode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Get concept statistics.
	if includeStat {
		statistics, err := r.cgs.GetStatByConceptGroup(ctx, cg)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		cg.Statistics = statistics
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler GetConceptGroup Success")
	rest.ReplyOK(c, http.StatusOK, cg)
}

// Create concept groups (internal).
func (r *restHandler) AddObjectTypesToConceptGroupByIn(c *gin.Context) {
	logger.Debug("Handler AddObjectTypesToConceptGroupByIn Start")
	// Internal endpoints read user_id from the header.
	visitor := visitor.GenerateVisitor(c)
	r.AddObjectTypesToConceptGroup(c, visitor)
}

// Create concept groups (external).
func (r *restHandler) AddObjectTypesToConceptGroupByEx(c *gin.Context) {
	logger.Debug("Handler AddObjectTypesToConceptGroupByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.AddObjectTypesToConceptGroup(c, visitor)
}

// Create concept groups.
func (r *restHandler) AddObjectTypesToConceptGroup(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler AddObjectTypesToConceptGroup Start")
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

	// Read the at_id path parameter.
	cgID := c.Param("cg_id")
	span.SetAttributes(attr.Key("cg_id").String(cgID))

	// Load the existing resource by ID..
	_, exist, err = r.cgs.CheckConceptGroupExistByID(ctx, knID, branch, cgID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ConceptGroup_ConceptGroupNotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Bind object type request parameters.
	var requestData struct {
		Entries []interfaces.ID `json:"entries"`
	}
	err = c.ShouldBindJSON(&requestData)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Whether to validate dependencies, default true
	strictModeStr := c.Query(interfaces.QueryParam_StrictMode)
	if strictModeStr == "" {
		strictModeStr = "true"
	}
	strictMode, parseErr := strconv.ParseBool(strictModeStr)
	if parseErr != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ConceptGroup_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "StrictModeInvalid", map[string]any{"value": strictModeStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Create the knowledge network.
	otCGIDs, err := r.cgs.AddObjectTypesToConceptGroup(ctx, nil, knID, branch, cgID, requestData.Entries, interfaces.ImportMode_Normal, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Return the created resources.
	result := []any{}
	for i, id := range otCGIDs {
		result = append(result, map[string]any{"id": id})
		// Record an audit log after successful creation.
		audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(visitor),
			interfaces.GenerateConceptGroupRelationAuditObject(id, fmt.Sprintf("%s-%s-%s-%s", knID, branch, cgID, requestData.Entries[i].ID)), "")
	}

	logger.Debug("Handler AddObjectTypeToGroup Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// Create concept groups (internal).
func (r *restHandler) DeleteObjectTypesFromGroupByIn(c *gin.Context) {
	logger.Debug("Handler DeleteObjectTypesFromGroupByIn Start")
	// Internal endpoints read user_id from the header.
	visitor := visitor.GenerateVisitor(c)
	r.DeleteObjectTypesFromGroup(c, visitor)
}

// Create concept groups (external).
func (r *restHandler) DeleteObjectTypesFromGroupByEx(c *gin.Context) {
	logger.Debug("Handler DeleteObjectTypesFromGroupByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.DeleteObjectTypesFromGroup(c, visitor)
}

// Create concept groups.
func (r *restHandler) DeleteObjectTypesFromGroup(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler DeleteObjectTypesFromGroup Start")
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

	// Read the at_id path parameter.
	cgID := c.Param("cg_id")
	span.SetAttributes(attr.Key("cg_id").String(cgID))

	// Load the existing resource by ID..
	_, exist, err = r.cgs.CheckConceptGroupExistByID(ctx, knID, branch, cgID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ConceptGroup_ConceptGroupNotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Read the comma-separated ID list.
	otIDsStr := c.Param("ot_ids")
	span.SetAttributes(attr.Key("ot_ids").String(otIDsStr))

	// Parse the string into []string.
	otIDs := common.StringToStringSlice(otIDsStr)
	// De-duplicate IDs before querying.
	otIDArr := common.DuplicateSlice(otIDs)
	// Check that all object type IDs are bound to the concept group.
	cgRelations, err := r.cgs.ListConceptGroupRelations(ctx, interfaces.ConceptGroupRelationsQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Limit: -1,
		},
		KNID:        knID,
		Branch:      branch,
		CGIDs:       []string{cgID},
		ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		OTIDs:       otIDArr,
	})
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if len(cgRelations) != len(otIDArr) {
		errStr := fmt.Sprintf("Exists any object types not in the concept group [%s] knowledge network [%s] branch [%s], expect relations num is [%d], actual relations num is [%d]",
			cgID, knID, branch, len(otIDs), len(otIDArr))

		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound,
			berrors.BknBackend_ConceptGroup_ConceptGroupRelationNotExisted).WithErrorDetails(errStr)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Delete object types in batch.
	err = r.cgs.DeleteObjectTypesFromGroup(ctx, nil, knID, branch, cgID, otIDArr)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record audit logs for each item.
	for _, cgr := range cgRelations {
		audit.NewWarnLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(visitor),
			interfaces.GenerateObjectTypeAuditObject(cgr.ID,
				fmt.Sprintf("%s-%s-%s-%s-%s", cgr.KNID, cgr.Branch, cgr.CGID, cgr.ConceptType, cgr.ConceptID)), audit.SUCCESS, "")
	}

	logger.Debug("Handler DeleteObjectTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}
