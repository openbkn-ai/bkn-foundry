// Copyright openbkn.ai
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
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"vega-backend/common"
	"vega-backend/common/visitor"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// CreateSemanticUnderstandingTaskByEx handles POST /api/vega-backend/v1/semantic-understanding-tasks.
func (r *restHandler) CreateSemanticUnderstandingTaskByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.createSemanticUnderstandingTask(c, visitor)
}

// CreateSemanticUnderstandingTaskByIn handles POST /api/vega-backend/in/v1/semantic-understanding-tasks.
func (r *restHandler) CreateSemanticUnderstandingTaskByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.createSemanticUnderstandingTask(c, visitor)
}

func (r *restHandler) createSemanticUnderstandingTask(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	var req interfaces.CreateSemanticUnderstandingTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	var (
		task *interfaces.SemanticUnderstandingTask
		err  error
	)
	switch req.Scope {
	case interfaces.SemanticUnderstandingTaskScopeResource:
		if req.ResourceID == "" {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
				WithErrorDetails("resource_id is required")
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		task, err = r.suts.CreateResourceTask(ctx, req.ResourceID, &req)
	case interfaces.SemanticUnderstandingTaskScopeCatalog:
		if req.CatalogID == "" {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
				WithErrorDetails("catalog_id is required")
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		task, err = r.suts.CreateCatalogTask(ctx, req.CatalogID, &req)
	default:
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails("scope must be resource or catalog")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, task)
}

// ListSemanticUnderstandingTasksByEx handles GET /api/vega-backend/v1/semantic-understanding-tasks.
func (r *restHandler) ListSemanticUnderstandingTasksByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.listSemanticUnderstandingTasks(c, visitor)
}

// ListSemanticUnderstandingTasksByIn handles GET /api/vega-backend/in/v1/semantic-understanding-tasks.
func (r *restHandler) ListSemanticUnderstandingTasksByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.listSemanticUnderstandingTasks(c, visitor)
}

func (r *restHandler) listSemanticUnderstandingTasks(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	params, err := parseSemanticUnderstandingTaskListParams(ctx, c)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	tasks, total, err := r.suts.List(ctx, params)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{
		"entries":     tasks,
		"total_count": total,
	})
}

// GetSemanticUnderstandingTaskByEx handles GET /api/vega-backend/v1/semantic-understanding-tasks/:id.
func (r *restHandler) GetSemanticUnderstandingTaskByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.getSemanticUnderstandingTask(c, visitor)
}

// GetSemanticUnderstandingTaskByIn handles GET /api/vega-backend/in/v1/semantic-understanding-tasks/:id.
func (r *restHandler) GetSemanticUnderstandingTaskByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.getSemanticUnderstandingTask(c, visitor)
}

func (r *restHandler) getSemanticUnderstandingTask(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	task, err := r.suts.GetByID(ctx, c.Param("id"))
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if task == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_SemanticUnderstandingTask_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, task)
}

// DeleteSemanticUnderstandingTasksByEx handles DELETE /api/vega-backend/v1/semantic-understanding-tasks/:ids.
func (r *restHandler) DeleteSemanticUnderstandingTasksByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.deleteSemanticUnderstandingTasks(c, visitor)
}

// DeleteSemanticUnderstandingTasksByIn handles DELETE /api/vega-backend/in/v1/semantic-understanding-tasks/:ids.
func (r *restHandler) DeleteSemanticUnderstandingTasksByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.deleteSemanticUnderstandingTasks(c, visitor)
}

func (r *restHandler) deleteSemanticUnderstandingTasks(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	ids := make([]string, 0)
	for _, rawID := range strings.Split(c.Param("ids"), ",") {
		if id := strings.TrimSpace(rawID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails("ids path parameter is required")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	ignoreMissing := strings.EqualFold(c.Query("ignore_missing"), "true")
	if err := r.suts.Delete(ctx, ids, ignoreMissing); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

func parseSemanticUnderstandingTaskListParams(ctx context.Context, c *gin.Context) (interfaces.SemanticUnderstandingTaskQueryParams, error) {
	params := interfaces.SemanticUnderstandingTaskQueryParams{}

	offset := common.GetQueryOrDefault(c, "offset", interfaces.DEFAULT_OFFSET)
	limit := common.GetQueryOrDefault(c, "limit", interfaces.DEFAULT_LIMIT)
	sort := common.GetQueryOrDefault(c, "sort", "create_time")
	direction := common.GetQueryOrDefault(c, "direction", interfaces.DESC_DIRECTION)

	pageParam, err := validatePaginationQueryParams(ctx, offset, limit, sort, direction, interfaces.SEMANTIC_UNDERSTANDING_TASK_SORT)
	if err != nil {
		return params, err
	}

	scope := c.Query("scope")
	if scope != "" && scope != interfaces.SemanticUnderstandingTaskScopeResource && scope != interfaces.SemanticUnderstandingTaskScopeCatalog {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails("scope must be resource or catalog")
	}

	if active, _ := strconv.ParseBool(c.Query("active")); active {
		params.Statuses = interfaces.SemanticUnderstandingTaskActiveStatuses
	} else if raw := c.Query("status"); raw != "" {
		statuses, err := parseSemanticUnderstandingTaskStatuses(ctx, raw)
		if err != nil {
			return params, err
		}
		params.Statuses = statuses
	}

	params.PaginationQueryParams = pageParam
	params.Scope = scope
	params.CatalogID = c.Query("catalog_id")
	params.ResourceID = c.Query("resource_id")
	return params, nil
}

func parseSemanticUnderstandingTaskStatuses(ctx context.Context, raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	statuses := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		if !isValidSemanticUnderstandingTaskStatus(s) {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
				WithErrorDetails(fmt.Sprintf("invalid status: %s", s))
		}
		statuses = append(statuses, s)
	}
	return statuses, nil
}

func isValidSemanticUnderstandingTaskStatus(status string) bool {
	switch status {
	case interfaces.SemanticUnderstandingTaskStatusPending,
		interfaces.SemanticUnderstandingTaskStatusRunning,
		interfaces.SemanticUnderstandingTaskStatusSucceeded,
		interfaces.SemanticUnderstandingTaskStatusFailed:
		return true
	}
	return false
}
