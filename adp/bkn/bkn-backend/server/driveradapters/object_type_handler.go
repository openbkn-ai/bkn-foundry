// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/audit"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
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

func (r *restHandler) HandleObjectTypeGetOverrideByIn(c *gin.Context) {
	switch c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE) {
	case "", http.MethodPost:
		r.CreateObjectTypesByIn(c)
	case http.MethodGet:
		r.SearchObjectTypesByIn(c)
	default:
		httpErr := rest.NewHTTPError(rest.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_OverrideMethod)
		rest.ReplyError(c, httpErr)
	}
}

func (r *restHandler) HandleObjectTypeGetOverrideByEx(c *gin.Context) {
	switch c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE) {
	case "", http.MethodPost:
		r.CreateObjectTypesByEx(c)
	case http.MethodGet:
		r.SearchObjectTypesByEx(c)
	default:
		httpErr := rest.NewHTTPError(rest.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_OverrideMethod)
		rest.ReplyError(c, httpErr)
	}
}

// Create object types (internal).
func (r *restHandler) CreateObjectTypesByIn(c *gin.Context) {
	logger.Debug("Handler CreateObjectTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.CreateObjectTypes(c, visitor)
}

// Create object types (external).
func (r *restHandler) CreateObjectTypesByEx(c *gin.Context) {
	logger.Debug("Handler CreateObjectTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.CreateObjectTypes(c, visitor)
}

// Create object types.
func (r *restHandler) CreateObjectTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler CreateObjectTypes Start")
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

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
	)

	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
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
		Entries []*interfaces.ObjectType `json:"entries"`
	}
	err = c.ShouldBindJSON(&requestData)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	objectTypes := requestData.Entries

	// Reject an empty entries array.
	if len(objectTypes) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody).
			WithErrorDetails(commonValidationDetail(ctx, "EntriesRequired", nil))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("创建对象类请求参数: [%s,%v]", c.Request.RequestURI, objectTypes))

	// Apply the branch from the URL to all requested object types.
	for i := range objectTypes {
		objectTypes[i].KNID = knID
		objectTypes[i].Branch = branch
	}

	// Validate model names in the request body.
	err = ValidateObjectTypes(ctx, knID, objectTypes, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Create the resources.
	otIDs, err := r.createObjectTypes(ctx, knID, branch, objectTypes, mode, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Return the created resources.
	for _, objectType := range objectTypes {
		// Record an audit log after each successful creation.
		audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(visitor),
			interfaces.GenerateObjectTypeAuditObject(objectType.OTID, objectType.OTName), "")
	}

	result := []any{}
	for _, otID := range otIDs {
		result = append(result, map[string]any{"id": otID})
	}

	logger.Debug("Handler CreateObjectTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// ValidateObjectTypesByIn validates object type dependencies without persistence (internal).
func (r *restHandler) ValidateObjectTypesByIn(c *gin.Context) {
	logger.Debug("Handler ValidateObjectTypesByIn Start")
	v := visitor.GenerateVisitor(c)
	r.ValidateObjectTypesForKN(c, v)
}

// ValidateObjectTypesByEx validates object type dependencies without persistence (external).
func (r *restHandler) ValidateObjectTypesByEx(c *gin.Context) {
	logger.Debug("Handler ValidateObjectTypesByEx Start")
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ValidateObjectTypesForKN(c, visitor)
}

// ValidateObjectTypesForKN validates object type dependencies without persistence.
func (r *restHandler) ValidateObjectTypesForKN(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler ValidateObjectTypesForKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
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
		Entries []*interfaces.ObjectType `json:"entries"`
	}
	if err = c.ShouldBindJSON(&requestData); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	objectTypes := requestData.Entries
	if len(objectTypes) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody).
			WithErrorDetails(commonValidationDetail(ctx, "EntriesRequired", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Apply the branch from the URL to all requested action types.
	for i := range objectTypes {
		objectTypes[i].KNID = knID
		objectTypes[i].Branch = branch
	}
	if err = ValidateObjectTypes(ctx, knID, objectTypes, strictMode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	if err = r.ots.ValidateObjectTypes(ctx, knID, branch, objectTypes, strictMode, nil, mode); err != nil {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
		rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": false, "detail": err.Error()})
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"valid": true})
}

// Update object types (internal).
func (r *restHandler) UpdateObjectTypeByIn(c *gin.Context) {
	logger.Debug("Handler UpdateObjectTypeByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.UpdateObjectType(c, visitor)
}

// Update object types (external).
func (r *restHandler) UpdateObjectTypeByEx(c *gin.Context) {
	logger.Debug("Handler UpdateObjectTypeByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.UpdateObjectType(c, visitor)
}

// Update object types.
func (r *restHandler) UpdateObjectType(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler UpdateObjectType Start")
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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
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

	// Read the ot_id path parameter.
	otID := c.Param("ot_id")
	span.SetAttributes(attr.Key("ot_id").String(otID))

	// Bind request parameters.
	objectType := interfaces.ObjectType{}
	err = c.ShouldBindJSON(&objectType)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	objectType.OTID = otID
	objectType.KNID = knID
	objectType.Branch = branch

	// Record API request parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("修改对象类请求参数: [%s, %v]", c.Request.RequestURI, objectType))

	// Load the existing resource by ID.
	oldObjectTypeName, exist, err := r.ots.CheckObjectTypeExistByID(ctx, knID, branch, otID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate required object type fields, lengths, and enum values.
	err = ValidateObjectType(ctx, &objectType, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate object type[%s] failed: %s. %v", objectType.OTName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("ot_name").String(objectType.OTName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// When the name or group changes, ensure the new name is available.
	ifNameModify := false
	if oldObjectTypeName != objectType.OTName {
		ifNameModify = true
		_, exist, err = r.ots.CheckObjectTypeExistByName(ctx, knID, branch, objectType.OTName)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		if exist {
			httpErr := rest.NewHTTPError(ctx, http.StatusForbidden,
				berrors.BknBackend_ObjectType_ObjectTypeNameExisted)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	objectType.IfNameModify = ifNameModify

	// Update the resource by ID.
	err = r.updateObjectType(ctx, &objectType, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, audit.UPDATE, audit.TransforOperator(visitor),
		interfaces.GenerateObjectTypeAuditObject(otID, objectType.OTName), "")

	logger.Debug("Handler UpdateObjectType Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// Update object type data properties.
func (r *restHandler) UpdateDataProperties(c *gin.Context) {
	logger.Debug("Handler UpdateDataProperties Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	// Verify the access token.
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account information in the context.
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

	// Whether to validate vector index embedding model deps, default true (same as UpdateObjectType).
	strictModeStr := c.DefaultQuery(interfaces.QueryParam_StrictMode, "true")
	strictMode, err := strconv.ParseBool(strictModeStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
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

	// Read the ot_id path parameter.
	otID := c.Param("ot_id")
	span.SetAttributes(attr.Key("ot_id").String(otID))

	// Load the existing resource by ID.
	objectType, err := r.ots.GetObjectTypeByID(ctx, nil, knID, branch, otID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if objectType == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	propertyNamesStr := c.Param("property_names")
	span.SetAttributes(attr.Key("property_names").String(propertyNamesStr))

	propertyNames := common.StringToStringSlice(propertyNamesStr)

	// Bind request parameters.
	var requestData struct {
		Entries []*interfaces.DataProperty `json:"entries"`
	}
	err = c.ShouldBindJSON(&requestData)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate data properties.
	err = ValidateDataProperties(ctx, propertyNames, requestData.Entries, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Update the resource by ID.
	err = r.ots.UpdateDataProperties(ctx, objectType, requestData.Entries, strictMode)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, audit.UPDATE, audit.TransforOperator(visitor),
		interfaces.GenerateObjectTypeAuditObject(otID, objectType.OTName), "")

	logger.Debug("Handler UpdateObjectType Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// Delete object types in batch.
func (r *restHandler) DeleteObjectTypes(c *gin.Context) {
	logger.Debug("Handler DeleteObjectTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("删除对象类请求参数: [%s]", c.Request.RequestURI))

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
	otIDsStr := c.Param("ot_ids")
	span.SetAttributes(attr.Key("ot_ids").String(otIDsStr))

	// Parse the string into []string.
	otIDs := common.StringToStringSlice(otIDsStr)

	// Read force_delete; it defaults to false.
	forceDeleteStr := c.DefaultQuery("force_delete", interfaces.DEFAULT_FORCE_DELETE)
	forceDelete, err := strconv.ParseBool(forceDeleteStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "ForceDeleteInvalid", map[string]any{"value": forceDeleteStr}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	span.SetAttributes(attr.Key("force_delete").Bool(forceDelete))

	// Check that all object type IDs exist.
	var objectTypes []*interfaces.ObjectTypeWithKeyField
	for _, otID := range otIDs {
		// Validate the object type ID in the specified knowledge network.
		otName, exist, err := r.ots.CheckObjectTypeExistByID(ctx, knID, branch, otID)
		if err != nil {
			httpErr := err.(*rest.HTTPError)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)

			rest.ReplyError(c, httpErr)
			return
		}
		if !exist {
			httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound)

			// Set trace attributes for the error.
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}

		objectTypes = append(objectTypes, &interfaces.ObjectTypeWithKeyField{OTID: otID, OTName: otName})
	}

	// When force_delete is false, verify that no relation type references the object type.
	if !forceDelete {
		// Query relation types to check whether the object type is referenced.
		// Query relation types that use the object type as their source.
		relationTypes, _, err := r.rts.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
			KNID:               knID,
			Branch:             branch,
			BoundObjectTypeIDs: otIDs,
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
		})
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}

		// Return an error when a relation type references the object type.
		if len(relationTypes) > 0 {
			// Collect names of referencing relation types.
			relationTypeNames := make([]string, 0, len(relationTypes))
			for _, rt := range relationTypes {
				relationTypeNames = append(relationTypeNames, rt.RTName)
			}
			errorDetails := i18n.Translate(rest.GetLanguageByCtx(ctx),
				"BknBackend.ObjectType.InvalidParameter.Detail.BoundByRelationTypes",
				map[string]any{"relationTypeNames": strings.Join(relationTypeNames, ", ")})
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_ObjectTypeBoundByRelationType).
				WithErrorDetails(errorDetails)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}

		// Verify that no action type references the object type.
		actionTypes, _, err := r.ats.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
			KNID:          knID,
			Branch:        branch,
			ObjectTypeIDs: otIDs,
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
		})
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		if len(actionTypes) > 0 {
			actionTypeNames := make([]string, 0, len(actionTypes))
			for _, at := range actionTypes {
				actionTypeNames = append(actionTypeNames, at.ATName)
			}
			errorDetails := i18n.Translate(rest.GetLanguageByCtx(ctx),
				"BknBackend.ObjectType.InvalidParameter.Detail.BoundByActionTypes",
				map[string]any{"actionTypeNames": strings.Join(actionTypeNames, ", ")})
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_ObjectTypeBoundByActionType).
				WithErrorDetails(errorDetails)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}

	// Delete object types in batch.
	err = r.deleteObjectTypes(ctx, knID, branch, otIDs)
	if err != nil {
		// Guard against a plain downstream error: normalize it to 500 instead of panicking into a 502.
		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(commonValidationDetail(ctx, "InternalRequestFailed", nil))
		}
		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record audit logs for each item.
	for _, objectType := range objectTypes {
		audit.NewWarnLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(visitor),
			interfaces.GenerateObjectTypeAuditObject(objectType.OTID, objectType.OTName), audit.SUCCESS, "")
	}

	logger.Debug("Handler DeleteObjectTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// List object types with pagination (internal).
func (r *restHandler) ListObjectTypesByIn(c *gin.Context) {
	logger.Debug("Handler ListObjectTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.ListObjectTypes(c, visitor)
}

// List object types with pagination (external).
func (r *restHandler) ListObjectTypesByEx(c *gin.Context) {
	logger.Debug("Handler ListObjectTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.ListObjectTypes(c, visitor)
}

// List object types with pagination.
func (r *restHandler) ListObjectTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("ListObjectTypes Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("分页获取对象类列表请求参数: [%s]", c.Request.RequestURI))

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
	offset := c.DefaultQuery("offset", interfaces.DEFAULT_OFFEST)
	limit := c.DefaultQuery("limit", interfaces.DEFAULT_LIMIT)
	sort := c.DefaultQuery("sort", "update_time")
	direction := c.DefaultQuery("direction", interfaces.DESC_DIRECTION)

	// Trim whitespace around tags before searching.
	tag = strings.Trim(tag, " ")

	// Validate pagination query parameters.
	pageParam, err := validatePaginationQueryParameters(ctx,
		offset, limit, sort, direction, interfaces.OBJECT_TYPE_SORT)
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
	parameter := interfaces.ObjectTypesQueryParams{
		NamePattern: namePattern,
		Tag:         tag,
		Branch:      branch,
		KNID:        knID,
	}
	parameter.Sort = pageParam.Sort
	parameter.Direction = pageParam.Direction
	parameter.Limit = pageParam.Limit
	parameter.Offset = pageParam.Offset

	// var result map[string]any
	// if simpleInfo {
	// Get object type summaries.
	otList, total, err := r.ots.ListObjectTypes(ctx, nil, parameter)
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

	logger.Debug("Handler ListObjectTypes Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	emitObjectTypeSchemaRead(ctx, c, visitor, "bkn.schema.object_type.list", knID, branch, nil, otList, int64(total))
	rest.ReplyOK(c, http.StatusOK, result)
}

// Get object type by ID (internal).
func (r *restHandler) GetObjectTypesByIn(c *gin.Context) {
	logger.Debug("Handler GetObjectTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetObjectTypes(c, visitor)
}

// Get object type by ID (external).
func (r *restHandler) GetObjectTypesByEx(c *gin.Context) {
	logger.Debug("Handler GetObjectTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.GetObjectTypes(c, visitor)
}

// Get object type by ID.
func (r *restHandler) GetObjectTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetObjectTypes Start")
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
	otIDsStr := c.Param("ot_ids")
	span.SetAttributes(attr.Key("ot_ids").String(otIDsStr))

	// Parse the string into []string.
	otIDs := common.StringToStringSlice(otIDsStr)

	// Get object type details; include data-view filters only when include_view is set.
	result, err := r.ots.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	httpResult := map[string]any{"entries": result}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler GetObjectTypes Success")
	emitObjectTypeSchemaRead(ctx, c, visitor, "bkn.schema.object_type.get", knID, branch, otIDs, result, int64(len(result)))
	rest.ReplyOK(c, http.StatusOK, httpResult)
}

func (r *restHandler) GetObjectTypeSampleDataByIn(c *gin.Context) {
	logger.Debug("Handler GetObjectTypeSampleDataByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.GetObjectTypeSampleData(c, visitor)
}

func (r *restHandler) GetObjectTypeSampleDataByEx(c *gin.Context) {
	logger.Debug("Handler GetObjectTypeSampleDataByEx Start")
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.GetObjectTypeSampleData(c, visitor)
}

func (r *restHandler) GetObjectTypeSampleData(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetObjectTypeSampleData Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	otID := c.Param("ot_ids")
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "LimitIntegerRequired", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "OffsetIntegerRequired", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	needTotal := c.DefaultQuery("need_total", "true") != "false"
	var searchAfter []any
	if rawSearchAfter := strings.TrimSpace(c.Query("search_after")); rawSearchAfter != "" {
		if err := json.Unmarshal([]byte(rawSearchAfter), &searchAfter); err != nil {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(commonValidationDetail(ctx, "SearchAfterArrayRequired", nil))
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}

	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("branch").String(branch),
		attr.Key("ot_id").String(otID),
	)

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

	result, err := r.ots.GetObjectTypeSampleData(ctx, knID, branch, otID, interfaces.ObjectTypeSampleDataQueryParams{
		Limit:       limit,
		NeedTotal:   needTotal,
		Offset:      offset,
		SearchAfter: searchAfter,
	})
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler GetObjectTypeSampleData Success")
	rest.ReplyOK(c, http.StatusOK, result)
}

// Search object types (external).
func (r *restHandler) SearchObjectTypesByIn(c *gin.Context) {
	logger.Debug("Handler SearchObjectTypesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.SearchObjectTypes(c, visitor)
}

// Search object types (external).
func (r *restHandler) SearchObjectTypesByEx(c *gin.Context) {
	logger.Debug("Handler SearchObjectTypesByEx Start")
	// Verify the access token.
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.SearchObjectTypes(c, visitor)
}

// Search object types.
func (r *restHandler) SearchObjectTypes(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("SearchObjectTypes Start")
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
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "RequestBindingFailed", nil))

		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	query.KNID = knID
	query.Branch = branch
	query.ModuleType = interfaces.MODULE_TYPE_OBJECT_TYPE

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
	result, err := r.ots.SearchObjectTypes(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debug("Handler SearchObjectTypes Success")
	rest.ReplyOK(c, http.StatusOK, result)
}
