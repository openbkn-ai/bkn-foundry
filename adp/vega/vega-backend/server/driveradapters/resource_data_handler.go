// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package driveradapters provides HTTP handlers (primary adapters).
package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/trace"

	"vega-backend/common"
	"vega-backend/common/visitor"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	resourcelogic "vega-backend/logics/resource"
)

// PostResourceDataByEx handles POST /api/vega-backend/v1/resources/:id/data (External).
// Dispatches based on X-HTTP-Method-Override header:
//
//	GET    → query (any category; returns entries + optional total_count)
//	POST   → batch create documents (dataset category only)
//	DELETE → delete documents by filter (dataset category only)
func (r *restHandler) PostResourceDataByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.postResourceData(c, visitor, false)
}

// PostResourceDataByIn handles POST /api/vega-backend/in/v1/resources/:id/data (Internal).
func (r *restHandler) PostResourceDataByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	// The internal network /in/ is the S2S boundary within the cluster: mark S2S to allow per-account authentication for internal infrastructure resources by default.
	r.postResourceData(c, visitor, true)
}

// postResourceData dispatches POST /resources/:id/data to the right branch based on
// When X-HTTP-Method-Override header.s2sInternal is true (only for /in/ internal network endpoints),
// Skip the per-account view_detail check for internal directory resources.
func (r *restHandler) postResourceData(c *gin.Context, visitor hydra.Visitor, s2sInternal bool) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	if s2sInternal {
		ctx = interfaces.WithS2SInternalAccess(ctx)
	}
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	override := strings.ToUpper(c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE))
	switch override {
	case http.MethodGet:
		r.queryResourceData(c, ctx, span)
	case http.MethodPost:
		r.createResourceData(c, ctx, span)
	case http.MethodDelete:
		r.deleteResourceDataByQuery(c, ctx, span)
	default:
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_OverrideMethod)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
	}
}

// queryResourceData handles POST /resources/:id/data + Override: GET.
// Generic resource-data query, supports any category.
func (r *restHandler) queryResourceData(c *gin.Context, ctx context.Context, span trace.Span) {
	start := time.Now()
	resourceID := c.Param("id")
	params, ok := bindResourceDataQuery(c, ctx, span)
	if !ok {
		return
	}

	resource, err := r.rs.GetByID(ctx, resourceID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, "Get resource failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if resource == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
		otellog.LogError(ctx, "Resource not found", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Reading rows needs query_data, not just view_detail. The two were split so
	// that "may see the structure" and "may read the contents" could differ
	// (#801); until this is checked the split decides nothing (#571).
	if err := r.rs.CheckResourcePermission(ctx, resourceID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		otellog.LogError(ctx, "Query resource data denied", err)
		rest.ReplyError(c, err)
		return
	}
	r.executeResourceDataQuery(c, ctx, span, resource, params, start)
}

// queryProxyResourceData reads a resource after the final PEP without falling back to caller authorization.
func (r *restHandler) queryProxyResourceData(c *gin.Context, ctx context.Context, span trace.Span) {
	start := time.Now()
	params, ok := bindResourceDataQuery(c, ctx, span)
	if !ok {
		return
	}
	resource, err := r.rs.InternalGetByID(ctx, nil, c.Param("id"))
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_Resource_InternalError_GetFailed)
		otellog.LogError(ctx, "Get proxy resource failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if resource == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
		otellog.LogError(ctx, "Proxy resource not found", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	r.executeResourceDataQuery(c, ctx, span, resource, params, start)
}

func bindResourceDataQuery(c *gin.Context, ctx context.Context, span trace.Span) (*interfaces.ResourceDataQueryParams, bool) {
	var params interfaces.ResourceDataQueryParams
	if err := common.BindPreciseJSON(c.Request.Body, &params); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Bind resource data query request failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return nil, false
	}
	if err := ValidateResourceDataQueryParams(ctx, &params); err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, "Validate resource data query params failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return nil, false
	}
	return &params, true
}

func (r *restHandler) executeResourceDataQuery(
	c *gin.Context,
	ctx context.Context,
	span trace.Span,
	resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams,
	start time.Time,
) {
	warning, err := resourcelogic.EnsureResourceQueryable(ctx, resource)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, "Resource is not queryable", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if warning != "" {
		otellog.LogWarn(ctx, "Query hit deprecated resource: "+warning)
	}
	if err := validateResourceDataQueryGroupByFields(ctx, params, resource.SchemaDefinition); err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, "Validate resource data group by fields failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	result, err := r.rds.QueryWithPaging(ctx, resource, params)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, "Query resource data failed", httpErr)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	resultData := map[string]any{
		"entries": result.Entries,
	}
	if params.NeedTotal || result.NeedTotal {
		resultData["total_count"] = result.TotalCount
	}
	if result.Paging != nil {
		resultData["paging"] = result.Paging
	}
	if warning != "" {
		resultData["warnings"] = []string{warning}
	}

	logger.Debug("Handler queryResourceData Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	emitResourceDataEvidence(c, ctx, resource, params, result)
	rest.ReplyOkWithHeaders(c, http.StatusOK, resultData, map[string]string{
		interfaces.X_REQUEST_TOOK: time.Since(start).String(),
	})
}

// createResourceData handles POST /resources/:id/data + Override: POST.
// Create one document; dataset category only.
func (r *restHandler) createResourceData(c *gin.Context, ctx context.Context, span trace.Span) {
	resource, ok := r.requireDatasetResource(c, ctx, span, c.Param("id"))
	if !ok {
		return
	}

	var document map[string]any
	if err := common.BindPreciseJSON(c.Request.Body, &document); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	docID, err := r.ds.CreateDocument(ctx, resource, document)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_Resource_InternalError_CreateFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler createResourceData Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, map[string]any{"id": docID})
}

// deleteResourceDataByQuery handles POST /resources/:id/data + Override: DELETE.
// Delete documents by filter; dataset category only. Body must carry a non-empty filter.
func (r *restHandler) deleteResourceDataByQuery(c *gin.Context, ctx context.Context, span trace.Span) {
	start := time.Now()

	resource, ok := r.requireDatasetResource(c, ctx, span, c.Param("id"))
	if !ok {
		return
	}

	var params interfaces.ResourceDataQueryParams
	if err := common.BindPreciseJSON(c.Request.Body, &params); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if params.FilterCondition == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails("filter is required for delete-by-query (empty filter would delete all documents)")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	var actualCond *interfaces.FilterCondCfg
	if err := mapstructure.Decode(params.FilterCondition, &actualCond); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterCondition).
			WithErrorDetails(fmt.Sprintf("mapstructure decode filters failed: %s", err.Error()))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	params.FilterCondCfg = actualCond

	if err := r.ds.DeleteDocumentsByQuery(ctx, resource, &params); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler deleteResourceDataByQuery Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOkWithHeaders(c, http.StatusNoContent, nil, map[string]string{
		interfaces.X_REQUEST_TOOK: time.Since(start).String(),
	})
}

// =========================== PUT /resources/:id/data ===========================

// =========================== GET /resources/:id/data/:docid ===========================

// GetResourceDataDocByEx handles GET /api/vega-backend/v1/resources/:id/data/:docid (External).
func (r *restHandler) GetResourceDataDocByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.getResourceDataDoc(c, visitor, false)
}

// GetResourceDataDocByIn handles GET /api/vega-backend/in/v1/resources/:id/data/:docid (Internal).
func (r *restHandler) GetResourceDataDocByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	// The internal network /in/ is the S2S boundary within the cluster: mark S2S to allow per-account authentication for internal infrastructure resources by default.
	r.getResourceDataDoc(c, visitor, true)
}

// getResourceDataDoc reads one or more documents. When s2sInternal is true (only for /in/ internal network endpoints),
// Skip the per-account view_detail check for internal directory resources.
func (r *restHandler) getResourceDataDoc(c *gin.Context, visitor hydra.Visitor, s2sInternal bool) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	if s2sInternal {
		ctx = interfaces.WithS2SInternalAccess(ctx)
	}
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	resource, ok := r.requireDatasetResource(c, ctx, span, c.Param("id"))
	if !ok {
		return
	}

	// Reading documents is reading rows, so it needs query_data for the same
	// reason the paged query does (#571): loading the resource only proves the
	// caller may see the table's structure, and this endpoint hands back its
	// contents.
	if err := r.rs.CheckResourcePermission(ctx, resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		otellog.LogError(ctx, "Get resource data document denied", err)
		rest.ReplyError(c, err)
		return
	}

	warning, err := resourcelogic.EnsureResourceQueryable(ctx, resource)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if warning != "" {
		otellog.LogWarn(ctx, "Query hit deprecated resource: "+warning)
	}

	docIDs := parseRawIDs(c.Param("docid"))
	if len(docIDs) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("at least one document id is required")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	ignoreMissing := strings.EqualFold(strings.TrimSpace(c.Query("ignore_missing")), "true")

	documents, err := r.ds.GetDocuments(ctx, resource, docIDs, ignoreMissing)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	entries := make([]map[string]any, 0, len(documents))
	for _, document := range documents {
		if document != nil {
			entries = append(entries, document)
		}
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"entries": entries})
}

// =========================== PUT /resources/:id/data/:docid ===========================

// PutResourceDataDocByEx handles PUT /api/vega-backend/v1/resources/:id/data/:docid (External).
// Single-document update; docid from path takes precedence over any `_id` field in body.
func (r *restHandler) PutResourceDataDocByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.putResourceDataDoc(c, visitor, false)
}

// PutResourceDataDocByIn handles PUT /api/vega-backend/in/v1/resources/:id/data/:docid (Internal).
func (r *restHandler) PutResourceDataDocByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	// The internal network /in/ is the S2S boundary within the cluster: mark S2S to allow per-account authentication for internal infrastructure resources by default.
	r.putResourceDataDoc(c, visitor, true)
}

// putResourceDataDoc updates a single document. When s2sInternal is true (only for /in/ internal network endpoints),
// Skip the per-account view_detail check for internal directory resources.
func (r *restHandler) putResourceDataDoc(c *gin.Context, visitor hydra.Visitor, s2sInternal bool) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	if s2sInternal {
		ctx = interfaces.WithS2SInternalAccess(ctx)
	}
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	resource, ok := r.requireDatasetResource(c, ctx, span, c.Param("id"))
	if !ok {
		return
	}

	docID := strings.TrimSpace(c.Param("docid"))
	if docID == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("exactly one document id is required")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	var doc map[string]any
	if err := common.BindPreciseJSON(c.Request.Body, &doc); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if bodyID, exists := doc["_id"]; exists {
		if s, ok := bodyID.(string); !ok || s != docID {
			logger.Warnf("PutResourceDataDoc: body._id (%v) overridden by path docid (%s)", bodyID, docID)
		}
	}
	doc["_id"] = docID

	if err := r.ds.ReplaceDocument(ctx, resource, docID, doc); err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_Resource_InternalError_UpdateFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler putResourceDataDoc Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"id": docID})
}

// =========================== DELETE /resources/:id/data/:docid ===========================

// DeleteResourceDataByEx handles DELETE /api/vega-backend/v1/resources/:id/data/:docid (External).
// Deletes one or more documents by IDs. Missing IDs reject the whole request by default;
// callers can opt into skipping them with ?ignore_missing=true.
func (r *restHandler) DeleteResourceDataByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.deleteResourceData(c, visitor, false)
}

// DeleteResourceDataByIn handles DELETE /api/vega-backend/in/v1/resources/:id/data/:docid (Internal).
func (r *restHandler) DeleteResourceDataByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	// The internal network /in/ is the S2S boundary within the cluster: mark S2S to allow per-account authentication for internal infrastructure resources by default.
	r.deleteResourceData(c, visitor, true)
}

// deleteResourceData deletes documents in batches by ID. When s2sInternal is true (only for /in/ internal network endpoints),
// Skip the per-account view_detail check for internal directory resources.
func (r *restHandler) deleteResourceData(c *gin.Context, visitor hydra.Visitor, s2sInternal bool) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	if s2sInternal {
		ctx = interfaces.WithS2SInternalAccess(ctx)
	}
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	resource, ok := r.requireDatasetResource(c, ctx, span, c.Param("id"))
	if !ok {
		return
	}

	docIDs := parseRawIDs(c.Param("docid"))
	if len(docIDs) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("at least one document id is required")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	ignoreMissing := strings.EqualFold(strings.TrimSpace(c.Query("ignore_missing")), "true")

	documents, err := r.ds.GetDocuments(ctx, resource, docIDs, ignoreMissing)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Strict mode reaches this point only after every requested document has
	// been found, so DeleteDocuments cannot partially apply a missing-ID batch.
	// Tolerant mode deletes only the IDs that survived the same preflight.
	idsToDelete := make([]string, 0, len(docIDs))
	for i, document := range documents {
		if document != nil {
			idsToDelete = append(idsToDelete, docIDs[i])
		}
	}
	if len(idsToDelete) == 0 {
		oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
		rest.ReplyOK(c, http.StatusNoContent, nil)
		return
	}
	if err := r.ds.DeleteDocuments(ctx, resource, idsToDelete); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler deleteResourceData Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// =========================== helpers ===========================

// requireDatasetResource loads resource by id and verifies it exists with category=dataset.
// On failure replies with the appropriate HTTP error and returns ok=false.
func (r *restHandler) requireDatasetResource(c *gin.Context, ctx context.Context, span trace.Span, id string) (*interfaces.Resource, bool) {
	resource, err := r.rs.GetByID(ctx, id)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return nil, false
	}
	if resource == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return nil, false
	}
	if resource.Category != interfaces.ResourceCategoryDataset {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails(fmt.Sprintf("operation requires resource category=dataset, got: %s", resource.Category))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return nil, false
	}
	return resource, true
}
