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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"ontology-query/common/visitor"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

// Object data query by object type (internal).
func (r *restHandler) GetObjectsInObjectTypeByIn(c *gin.Context) {
	logger.Debug("Handler GetObjectsInObjectTypeByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetObjectsInObjectType(c, visitor)
}

// Object data query by object type (external).
func (r *restHandler) GetObjectsInObjectTypeByEx(c *gin.Context) {
	logger.Debug("Handler GetObjectsInObjectTypeByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)

	defer span.End()

	// Verify the access token.
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.GetObjectsInObjectType(c, visitor)
}

// Object data query by object type.
func (r *restHandler) GetObjectsInObjectType(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetObjectsInObjectType Start")
	startTime := time.Now()

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set related API attributes on the trace.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Record API call parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("对象数据查询请求参数: [%s,%v]", c.Request.RequestURI, c.Request.Body))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	span.SetAttributes(attr.Key("kn_id").String(knID))

	// Read the ID list.
	otID := c.Param("ot_id")
	span.SetAttributes(attr.Key("ot_id").String(otID))

	// Accept the branch parameter.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("branch").String(branch))

	// todo: paginated query.

	// Whether to include object type information.
	includeTypeInfo := c.DefaultQuery("include_type_info", interfaces.DEFAULT_INCLUDE_TYPE_INFO)
	// Whether to include logical-property calculation parameters.
	IncludeLogicParams := c.DefaultQuery("include_logic_params", interfaces.DEFAULT_INCLUDE_LOGIC_PARAMS)
	// Whether to ignore persisted data and use virtual queries; default is false.
	ignoringStoreCache := c.DefaultQuery("ignoring_store_cache", interfaces.DEFAULT_IGNORING_STORE_CACHE)
	// List of system fields to exclude.
	excludeSystemProperties := c.QueryArray("exclude_system_properties")

	// Validate query parameters.
	objectsQueryParas, err := validateObjectsQueryParameters(ctx, includeTypeInfo, ignoringStoreCache, IncludeLogicParams, excludeSystemProperties)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		// Log the exception.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	err = ValidateHeaderMethodOverride(ctx, c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE))
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}
	// Instant-query parameters: time (start and end), isInstantQuery, and interval = 1.
	// Bind request parameters.
	query := interfaces.ObjectQueryBaseOnObjectType{}
	err = c.ShouldBindJSON(&query)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Paramter Failed:%s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	query.KNID = knID
	query.Branch = branch
	query.ObjectTypeID = otID
	query.CommonQueryParameters = objectsQueryParas

	err = validateObjectSearchRequest(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	// Execute the query.
	result, err := r.ots.GetObjectsByObjectTypeID(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	// Set success attributes on the trace.
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)

	result.OverallMs = time.Now().UnixMilli() - startTime.UnixMilli()
	emitObjectQueryEvidence(c, ctx, visitor, &query, &result)
	rest.ReplyOK(c, http.StatusOK, result)

}

// Object data query by object type (internal).
func (r *restHandler) GetObjectsPropertiesByIn(c *gin.Context) {
	logger.Debug("Handler GetObjectsPropertiesByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	r.GetObjectsProperties(c, visitor)
}

// Object data query by object type (external).
func (r *restHandler) GetObjectsPropertiesByEx(c *gin.Context) {
	logger.Debug("Handler GetObjectsPropertiesByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)

	defer span.End()

	// Verify the access token.
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.GetObjectsProperties(c, visitor)
}

// Object data query by object type.
func (r *restHandler) GetObjectsProperties(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetObjectsProperties Start")
	startTime := time.Now()

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set related API attributes on the trace.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Record API call parameters: c.Request.RequestURI and body.
	otellog.LogInfo(ctx, fmt.Sprintf("对象属性值查询请求参数: [%s,%v]", c.Request.RequestURI, c.Request.Body))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	span.SetAttributes(attr.Key("kn_id").String(knID))

	// Read the ID list.
	otID := c.Param("ot_id")
	span.SetAttributes(attr.Key("ot_id").String(otID))

	// Accept the branch parameter.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("branch").String(branch))

	// Whether to include object type information.
	includeTypeInfo := c.DefaultQuery("include_type_info", interfaces.DEFAULT_INCLUDE_TYPE_INFO)
	// List of system fields to exclude.
	excludeSystemProperties := c.QueryArray("exclude_system_properties")

	// Validate query parameters.
	objectsQueryParas, err := validateObjectsQueryParameters(ctx, includeTypeInfo,
		interfaces.DEFAULT_IGNORING_STORE_CACHE,
		interfaces.DEFAULT_INCLUDE_LOGIC_PARAMS,
		excludeSystemProperties)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		// Log the exception.
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	err = ValidateHeaderMethodOverride(ctx, c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE))
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}
	// Bind request parameters.
	query := interfaces.ObjectPropertyValueQuery{}
	err = c.ShouldBindJSON(&query)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Paramter Failed:%s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	query.KNID = knID
	query.Branch = branch
	query.ObjectTypeID = otID
	query.CommonQueryParameters = objectsQueryParas

	err = validateObjectPropertyValueQuery(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	// Execute the query.
	result, err := r.ots.GetObjectPropertyValue(ctx, &query)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		// Set error attributes on the trace.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	// Set success attributes on the trace.
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)

	result.OverallMs = time.Now().UnixMilli() - startTime.UnixMilli()
	rest.ReplyOK(c, http.StatusOK, result)

}
