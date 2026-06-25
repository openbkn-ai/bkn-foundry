// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kweaver-ai/kweaver-go-lib/audit"
	"github.com/kweaver-ai/kweaver-go-lib/hydra"
	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/kweaver-ai/kweaver-go-lib/otel/otellog"
	"github.com/kweaver-ai/kweaver-go-lib/otel/oteltrace"
	"github.com/kweaver-ai/kweaver-go-lib/rest"

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

	resource, err := r.rs.GetByID(ctx, req.ResourceID)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if req.BuildKeyFields == "" && req.Mode == interfaces.BuildTaskModeBatch {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails("build_key_fields is required for batch mode")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	schemaFields := make(map[string]string, len(resource.SchemaDefinition))
	for _, prop := range resource.SchemaDefinition {
		schemaFields[prop.Name] = prop.Type
	}
	if req.BuildKeyFields != "" {
		for _, key := range strings.Split(req.BuildKeyFields, ",") {
			key = strings.TrimSpace(key)
			if _, ok := schemaFields[key]; key != "" && !ok {
				httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("build_key_field '%s' not found in resource schema", key))
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
		}
	}
	if req.EmbeddingFields != "" {
		for _, field := range strings.Split(req.EmbeddingFields, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			fieldType, ok := schemaFields[field]
			if !ok {
				httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("embedding_field '%s' not found in resource schema", field))
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
			// 向量化只对文本字段有意义：非 string/text 字段在运行时会被当作空文本静默跳过，
			// 产出永远为空的 _vector 列且进度照常 100%，这里直接拦下
			if fieldType != interfaces.DataType_String && fieldType != interfaces.DataType_Text {
				httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("embedding_field '%s' has type '%s', only string/text fields can be embedded", field, fieldType))
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
		}
	}
	if req.FulltextFields != "" {
		for _, field := range strings.Split(req.FulltextFields, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			fieldType, ok := schemaFields[field]
			if !ok {
				httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("fulltext_field '%s' not found in resource schema", field))
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
			// 全文检索只对文本字段有意义：string 加 text 子字段、text 主字段分词；
			// 其它类型无分词语义，直接拦下
			if fieldType != interfaces.DataType_String && fieldType != interfaces.DataType_Text {
				httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("fulltext_field '%s' has type '%s', only string/text fields support fulltext", field, fieldType))
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
		}
	}

	taskID, err := r.bts.CreateBuildTask(ctx, &req)
	if err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, "build", audit.TransforOperator(visitor),
		interfaces.GenerateResourceAuditObject(req.ResourceID, ""), "")

	logger.Debug("Handler CreateBuildTask Success")
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, map[string]any{
		"id":          taskID,
		"resource_id": req.ResourceID,
		"status":      interfaces.BuildTaskStatusInit,
	})
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
	buildTask, err := r.bts.GetBuildTaskByID(ctx, taskID)
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

	tasks, total, err := r.bts.ListBuildTasks(ctx, params)
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
// `ids` is a comma-separated list. Optional query: ?ignore_missing=true
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

	if err := r.bts.DeleteBuildTasks(ctx, ids, ignoreMissing); err != nil {
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
	// body is optional; bind errors are tolerated
	_ = c.ShouldBindJSON(&req)

	if err := r.bts.StartBuildTask(ctx, taskID, req.ExecuteType); err != nil {
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

// =========================== PUT /build-tasks/:id ===========================

func (r *restHandler) UpdateBuildTaskConfigByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.updateBuildTaskConfig(c, visitor)
}

func (r *restHandler) UpdateBuildTaskConfigByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.updateBuildTaskConfig(c, visitor)
}

func (r *restHandler) updateBuildTaskConfig(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	taskID := c.Param("id")
	var req interfaces.UpdateBuildTaskConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if err := r.bts.UpdateBuildTaskConfig(ctx, taskID, &req); err != nil {
		httpErr := err.(*rest.HTTPError)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	audit.NewInfoLog(audit.OPERATION, "update", audit.TransforOperator(visitor),
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
	if err := r.bts.StopBuildTask(ctx, taskID); err != nil {
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
