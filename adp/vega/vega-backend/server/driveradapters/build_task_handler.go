// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/audit"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"vega-backend/common"
	"vega-backend/common/visitor"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// =========================== POST /build-tasks ===========================

// CreateBuildTaskByEx handles POST /api/vega-backend/v1/build-tasks (External).
func (r *restHandler) CreateBuildTaskByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.createBuildTask(c, visitor)
}

// CreateBuildTaskByIn handles POST /api/vega-backend/in/v1/build-tasks (Internal).
func (r *restHandler) CreateBuildTaskByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.createBuildTask(c, visitor)
}

func (r *restHandler) createBuildTask(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	var req interfaces.CreateBuildTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	taskID, err := r.bts.Create(ctx, &req)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, "build", audit.TransforOperator(visitor),
		interfaces.GenerateResourceAuditObject(req.ResourceID, ""), "")

	logger.Debug("Handler Create Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, map[string]any{"id": taskID})
}

// =========================== GET /build-tasks/:id ===========================

func (r *restHandler) GetBuildTaskByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.getBuildTask(c, visitor)
}

func (r *restHandler) GetBuildTaskByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.getBuildTask(c, visitor)
}

func (r *restHandler) getBuildTask(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	taskID := c.Param("id")
	buildTask, err := r.bts.GetByID(ctx, taskID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, buildTask)
}

// =========================== GET /build-tasks ===========================

// parseBuildTaskListParams 解析并校验 GET /build-tasks 的全部 query:
// 分页(offset/limit)、排序(order_by/order)、过滤(status 多值 / mode)。
// 排序与过滤均下沉服务端,排序全局先于分页(见 build_task_access.List);
// total_count 始终为过滤后全量条数。
func parseBuildTaskListParams(ctx context.Context, c *gin.Context) (interfaces.BuildTasksQueryParams, error) {
	params := interfaces.BuildTasksQueryParams{}

	// 分页:offset / limit(沿用既有规则,limit=-1 表示不分页)
	offset := common.GetQueryOrDefault(c, "offset", interfaces.DEFAULT_OFFSET)
	limit := common.GetQueryOrDefault(c, "limit", interfaces.DEFAULT_LIMIT)
	off, err := strconv.Atoi(offset)
	if err != nil || off < interfaces.MIN_OFFSET {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Offset).
			WithErrorDetails(fmt.Sprintf("invalid offset: %s", offset))
	}
	lim, err := strconv.Atoi(limit)
	if err != nil || (limit != interfaces.NO_LIMIT && (lim < interfaces.MIN_LIMIT || lim > interfaces.MAX_LIMIT)) {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Limit).
			WithErrorDetails(fmt.Sprintf("invalid limit: %s", limit))
	}
	params.Offset = off
	params.Limit = lim

	// 排序:order_by / order
	orderBy := common.GetQueryOrDefault(c, "order_by", interfaces.BuildTaskOrderByCreatedAt)
	if !isValidBuildTaskOrderBy(orderBy) {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Sort).
			WithErrorDetails(fmt.Sprintf("invalid order_by: %s", orderBy))
	}
	order := common.GetQueryOrDefault(c, "order", interfaces.DESC_DIRECTION)
	if order != interfaces.ASC_DIRECTION && order != interfaces.DESC_DIRECTION {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Direction).
			WithErrorDetails(fmt.Sprintf("invalid order: %s", order))
	}
	params.OrderBy = orderBy
	params.Order = order

	// 过滤:读取重复传递的 status 参数。
	if raw := c.QueryArray("status"); len(raw) > 0 {
		statuses, err := parseTaskStatuses(ctx, raw, isValidBuildTaskStatus, verrors.VegaBackend_BuildTask_InvalidStatus)
		if err != nil {
			return params, err
		}
		params.Statuses = statuses
	}

	// 过滤:mode
	mode := c.Query("mode")
	if mode != "" && !isValidBuildTaskMode(mode) {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_Mode).
			WithErrorDetails(fmt.Sprintf("invalid mode: %s", mode))
	}

	params.ResourceID = c.Query("resource_id")
	params.CatalogID = c.Query("catalog_id")
	params.Mode = mode
	return params, nil
}

func (r *restHandler) ListBuildTasksByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.listBuildTasks(c, visitor)
}

func (r *restHandler) ListBuildTasksByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.listBuildTasks(c, visitor)
}

func (r *restHandler) listBuildTasks(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	params, err := parseBuildTaskListParams(ctx, c)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description,
			httpErr.BaseError.ErrorDetails), nil)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	tasks, total, err := r.bts.List(ctx, params)
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

// =========================== DELETE /build-tasks/:ids ===========================

// DeleteBuildTasksByEx handles DELETE /build-tasks/:ids (External).
// `ids` is a comma-separated list.
// Optional query: ?ignore_missing=true&delete_active_index=true
func (r *restHandler) DeleteBuildTasksByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.deleteBuildTasks(c, visitor)
}

func (r *restHandler) DeleteBuildTasksByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.deleteBuildTasks(c, visitor)
}

func (r *restHandler) deleteBuildTasks(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	idsStr := c.Param("ids")
	ids := make([]string, 0)
	for _, id := range strings.Split(idsStr, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
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
	deleteActiveIndex := strings.EqualFold(c.Query("delete_active_index"), "true")

	if err := r.bts.Delete(ctx, ids, ignoreMissing, deleteActiveIndex); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	for _, id := range ids {
		audit.NewWarnLog(audit.OPERATION, audit.DELETE, audit.TransforOperator(visitor),
			interfaces.GenerateResourceAuditObject(id, ""), audit.SUCCESS, "")
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// =========================== POST /build-tasks/:id/start ===========================

func (r *restHandler) StartBuildTaskByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.startBuildTask(c, visitor)
}

func (r *restHandler) StartBuildTaskByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.startBuildTask(c, visitor)
}

func (r *restHandler) startBuildTask(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	taskID := c.Param("id")
	var req interfaces.StartBuildTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if err := r.bts.Start(ctx, taskID, req.Reset); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, "start", audit.TransforOperator(visitor),
		interfaces.GenerateResourceAuditObject(taskID, ""), "")

	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, nil)
}

// =========================== POST /build-tasks/:id/stop ===========================

func (r *restHandler) StopBuildTaskByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.stopBuildTask(c, visitor)
}

func (r *restHandler) StopBuildTaskByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.stopBuildTask(c, visitor)
}

func (r *restHandler) stopBuildTask(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	taskID := c.Param("id")
	if err := r.bts.Stop(ctx, taskID); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, "stop", audit.TransforOperator(visitor),
		interfaces.GenerateResourceAuditObject(taskID, ""), "")

	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	rest.ReplyOK(c, http.StatusAccepted, nil)
}

// =========================== helpers ===========================
