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

// Get an object subgraph by start point, direction, and path length (internal).
func (r *restHandler) GetObjectsSubgraphByIn(c *gin.Context) {
	logger.Debug("Handler GetObjectsSubgraphByIn Start")
	// Internal endpoints read user_id from the header and defer authorization to the permission check.
	// Construct a visitor for the internal request.
	visitor := visitor.GenerateVisitor(c)
	// Query type; default is expanding the subgraph from the start point.
	queryType := c.DefaultQuery("query_type", "")
	switch queryType {
	case "":
		r.GetObjectsSubgraph(c, visitor)
	case interfaces.QUERY_TYPE_RELATION_TYPE_PATH:
		r.GetObjectsSubgraphByTypePath(c, visitor)
	}
}

// Get an object subgraph by start point, direction, and path length (external).
func (r *restHandler) GetObjectsSubgraphByEx(c *gin.Context) {
	logger.Debug("Handler GetObjectsSubgraphByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)

	defer span.End()

	// Verify the access token.
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}

	// Query type; default is expanding the subgraph from the start point.
	queryType := c.DefaultQuery("query_type", "")
	switch queryType {
	case "":
		r.GetObjectsSubgraph(c, visitor)
	case interfaces.QUERY_TYPE_RELATION_TYPE_PATH:
		r.GetObjectsSubgraphByTypePath(c, visitor)
	}

}

// Object data query by object type.
func (r *restHandler) GetObjectsSubgraph(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetObjectsSubgraph Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("对象子图查询请求参数: [%s,%v]", c.Request.RequestURI, c.Request.Body))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	span.SetAttributes(attr.Key("kn_id").String(knID))

	// Accept the branch parameter.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("branch").String(branch))

	// Whether to include logical-property calculation parameters.
	includeLogicParams := c.DefaultQuery("include_logic_params", interfaces.DEFAULT_INCLUDE_LOGIC_PARAMS)
	// Whether to ignore persisted data and use virtual queries; default is false.
	ignoringStoreCache := c.DefaultQuery("ignoring_store_cache", interfaces.DEFAULT_IGNORING_STORE_CACHE)
	// List of system fields to exclude.
	excludeSystemProperties := c.QueryArray("exclude_system_properties")
	// Validate query parameters.
	queryParams, err := validateSugraphQueryParameters(ctx, includeLogicParams, ignoringStoreCache, excludeSystemProperties)
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
	query := interfaces.SubGraphQueryBaseOnSource{}
	err = c.ShouldBindJSON(&query)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Paramter Failed:%s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	query.KNID = knID
	query.Branch = branch
	query.CommonQueryParameters = queryParams
	query.PathQuotaManager = &interfaces.PathQuotaManager{
		TotalLimit: interfaces.MAX_PATHS,
	}

	err = validateSubgraphSearchRequest(ctx, &query)
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
	result, err := r.kns.SearchSubgraph(ctx, &query)
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
	emitSubgraphEvidence(c, ctx, visitor, knID, branch, "bkn.relation.query", safeSubgraphSourceQueryShape(&query), &result)
	rest.ReplyOK(c, http.StatusOK, result)
}

// Object data query by object type.
func (r *restHandler) GetObjectsSubgraphByTypePath(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetObjectsSubgraphByTypePath Start")
	// startTime := time.Now()

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
	otellog.LogInfo(ctx, fmt.Sprintf("对象子图查询请求参数: [%s,%v]", c.Request.RequestURI, c.Request.Body))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	span.SetAttributes(attr.Key("kn_id").String(knID))

	// Accept the branch parameter.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("branch").String(branch))

	// Whether to include logical-property calculation parameters.
	includeLogicParams := c.DefaultQuery("include_logic_params", interfaces.DEFAULT_INCLUDE_LOGIC_PARAMS)
	// Whether to ignore persisted data and use virtual queries; default is false.
	ignoringStoreCache := c.DefaultQuery("ignoring_store_cache", interfaces.DEFAULT_IGNORING_STORE_CACHE)
	// List of system fields to exclude.
	excludeSystemProperties := c.QueryArray("exclude_system_properties")
	// Validate query parameters.
	queryParams, err := validateSugraphQueryParameters(ctx, includeLogicParams, ignoringStoreCache, excludeSystemProperties)
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
	paths := interfaces.QueryRelationTypePaths{}
	err = c.ShouldBindJSON(&paths)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Paramter Failed:%s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}
	query := interfaces.SubGraphQueryBaseOnTypePath{
		Paths:                 paths,
		KNID:                  knID,
		Branch:                branch,
		CommonQueryParameters: queryParams,
	}

	err = validateSubgraphQueryByPathRequest(ctx, &query)
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
	result, err := r.kns.SearchSubgraphByTypePath(ctx, &query)
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

	// result.OverallMs = time.Now().UnixMilli() - startTime.UnixMilli()
	emitSubgraphEntriesEvidence(c, ctx, visitor, knID, branch, paths, result)
	rest.ReplyOK(c, http.StatusOK, result)
}

// Build a relation subgraph from a set of object instances (external).
func (r *restHandler) GetObjectsSubgraphByObjectsByEx(c *gin.Context) {
	logger.Debug("Handler GetObjectsSubgraphByObjectsByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)

	defer span.End()

	// Verify the access token.
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}

	r.GetObjectsSubgraphByObjects(c, visitor)
}

// Build a relation subgraph from a set of object instances (internal).
func (r *restHandler) GetObjectsSubgraphByObjectsByIn(c *gin.Context) {
	logger.Debug("Handler GetObjectsSubgraphByObjectsByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.GetObjectsSubgraphByObjects(c, visitor)
}

// Build a relation subgraph from a set of object instances (common handler).
func (r *restHandler) GetObjectsSubgraphByObjects(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler GetObjectsSubgraphByObjects Start")
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
	otellog.LogInfo(ctx, fmt.Sprintf("基于一组对象实例组织关系子图查询请求参数: [%s,%v]", c.Request.RequestURI, c.Request.Body))

	// Read the kn_id path parameter.
	knID := c.Param("kn_id")
	span.SetAttributes(attr.Key("kn_id").String(knID))

	// Accept the branch parameter.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("branch").String(branch))

	// Whether to include object type information.
	includeTypeInfo := c.DefaultQuery("include_type_info", interfaces.DEFAULT_INCLUDE_TYPE_INFO)
	// Whether to include logical-property calculation parameters.
	includeLogicParams := c.DefaultQuery("include_logic_params", interfaces.DEFAULT_INCLUDE_LOGIC_PARAMS)
	// Whether to ignore persisted data and use virtual queries; default is false.
	ignoringStoreCache := c.DefaultQuery("ignoring_store_cache", interfaces.DEFAULT_IGNORING_STORE_CACHE)
	// List of system fields to exclude.
	excludeSystemProperties := c.QueryArray("exclude_system_properties")
	// Validate query parameters.
	queryParams, err := validateObjectsQueryParameters(ctx, includeTypeInfo, ignoringStoreCache, includeLogicParams, excludeSystemProperties)
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

	// Bind request parameters.
	query := interfaces.SubGraphQueryBaseOnObjects{}
	err = c.ShouldBindJSON(&query)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Paramter Failed:%s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), httpErr)

		rest.ReplyError(c, httpErr)

		return
	}

	query.KNID = knID
	query.Branch = branch
	query.CommonQueryParameters = queryParams

	err = validateSubgraphQueryByObjectsRequest(ctx, &query)
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
	result, err := r.kns.SearchSubgraphByObjects(ctx, &query)
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
	emitSubgraphEvidence(c, ctx, visitor, knID, branch, "bkn.relation.query", safeSubgraphByObjectsQueryShape(&query), &result)
	rest.ReplyOK(c, http.StatusOK, result)
}
