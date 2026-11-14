// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common/visitor"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// =========================== POST /build-schedules ===========================

// CreateBuildScheduleByEx handles POST /api/vega-backend/v1/build-schedules (External).
func (r *restHandler) CreateBuildScheduleByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.createBuildSchedule(c, visitor)
}

// CreateBuildScheduleByIn handles POST /api/vega-backend/in/v1/build-schedules (Internal).
func (r *restHandler) CreateBuildScheduleByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.createBuildSchedule(c, visitor)
}

func (r *restHandler) createBuildSchedule(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	var req interfaces.BuildScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	scheduleID, err := r.bss.Create(ctx, &req)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_CreateFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler CreateBuildSchedule Success")
	c.Set(operationAuditTargetIDKey, scheduleID)
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, gin.H{"id": scheduleID})
}

// =========================== GET /build-schedules ===========================

// ListBuildSchedulesByEx handles GET /api/vega-backend/v1/build-schedules (External).
func (r *restHandler) ListBuildSchedulesByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.listBuildSchedules(c, visitor)
}

// ListBuildSchedulesByIn handles GET /api/vega-backend/in/v1/build-schedules (Internal).
func (r *restHandler) ListBuildSchedulesByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.listBuildSchedules(c, visitor)
}

func (r *restHandler) listBuildSchedules(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	offset := common.GetQueryOrDefault(c, "offset", interfaces.DEFAULT_OFFSET)
	limit := common.GetQueryOrDefault(c, "limit", interfaces.DEFAULT_LIMIT)
	sort := common.GetQueryOrDefault(c, "sort", interfaces.BuildScheduleSortUpdateTime)
	direction := common.GetQueryOrDefault(c, "direction", interfaces.DESC_DIRECTION)

	pageParam, err := validatePaginationQueryParams(ctx,
		offset, limit, sort, direction, interfaces.BUILD_SCHEDULE_SORT)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.ErrorCode,
			httpErr.ErrorDetails), nil)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	params := interfaces.BuildScheduleQueryParams{
		PaginationQueryParams: pageParam,
		Name:                  strings.TrimSpace(c.Query("name")),
		CatalogID:             c.Query("catalog_id"),
		ResourceID:            c.Query("resource_id"),
		ExecuteType:           c.Query("execute_type"),
	}
	if enabledStr := c.Query("enabled"); enabledStr != "" {
		v, err := strconv.ParseBool(enabledStr)
		if err != nil {
			httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(fmt.Sprintf("invalid enabled value: %s", enabledStr))
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
		params.Enabled = &v
	}

	if params.ExecuteType != "" && params.ExecuteType != interfaces.BuildTaskExecuteTypeFull && params.ExecuteType != interfaces.BuildTaskExecuteTypeIncremental {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidExecuteType)
		rest.ReplyError(c, httpErr)
		return
	}

	entries, total, err := r.bss.List(ctx, params)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, gin.H{"entries": entries, "total_count": total})
}

// =========================== GET /build-schedules/:id ===========================

// GetBuildScheduleByEx handles GET /api/vega-backend/v1/build-schedules/:id (External).
func (r *restHandler) GetBuildScheduleByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.getBuildSchedule(c, visitor)
}

// GetBuildScheduleByIn handles GET /api/vega-backend/in/v1/build-schedules/:id (Internal).
func (r *restHandler) GetBuildScheduleByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.getBuildSchedule(c, visitor)
}

func (r *restHandler) getBuildSchedule(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("id")
	schedule, err := r.bss.GetByID(ctx, id)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if schedule == nil {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildSchedule_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, schedule)
}

// =========================== PUT /build-schedules/:id ===========================

// UpdateBuildScheduleByEx handles PUT /api/vega-backend/v1/build-schedules/:id (External).
func (r *restHandler) UpdateBuildScheduleByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.updateBuildSchedule(c, visitor)
}

// UpdateBuildScheduleByIn handles PUT /api/vega-backend/in/v1/build-schedules/:id (Internal).
func (r *restHandler) UpdateBuildScheduleByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.updateBuildSchedule(c, visitor)
}

func (r *restHandler) updateBuildSchedule(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("id")

	current, err := r.bss.GetByID(ctx, id)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if current == nil {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildSchedule_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	var req interfaces.BuildScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if err := validateExpectedUpdateTime(ctx, req.ExpectedUpdateTime); err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if err := r.bss.Update(ctx, current, &req); err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_UpdateFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debug("Handler UpdateBuildSchedule Success")
	c.Set("operation_audit_target_names", []string{current.Name})

	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// =========================== DELETE /build-schedules/:id ===========================

// DeleteBuildScheduleByEx handles DELETE /api/vega-backend/v1/build-schedules/:id (External).
func (r *restHandler) DeleteBuildScheduleByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.deleteBuildSchedule(c, visitor)
}

// DeleteBuildScheduleByIn handles DELETE /api/vega-backend/in/v1/build-schedules/:id (Internal).
func (r *restHandler) DeleteBuildScheduleByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.deleteBuildSchedule(c, visitor)
}

func (r *restHandler) deleteBuildSchedule(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("id")

	current, err := r.bss.GetByID(ctx, id)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if current == nil {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildSchedule_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if err := r.bss.Delete(ctx, current); err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_DeleteFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	c.Set("operation_audit_target_names", []string{current.Name})

	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// =========================== POST /build-schedules/:id/enable ===========================

// EnableBuildScheduleByEx handles POST /api/vega-backend/v1/build-schedules/:id/enable (External).
func (r *restHandler) EnableBuildScheduleByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.setBuildScheduleEnabled(c, visitor, true)
}

// EnableBuildScheduleByIn handles POST /api/vega-backend/in/v1/build-schedules/:id/enable (Internal).
func (r *restHandler) EnableBuildScheduleByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.setBuildScheduleEnabled(c, visitor, true)
}

// DisableBuildScheduleByEx handles POST /api/vega-backend/v1/build-schedules/:id/disable (External).
func (r *restHandler) DisableBuildScheduleByEx(c *gin.Context) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.setBuildScheduleEnabled(c, visitor, false)
}

// DisableBuildScheduleByIn handles POST /api/vega-backend/in/v1/build-schedules/:id/disable (Internal).
func (r *restHandler) DisableBuildScheduleByIn(c *gin.Context) {
	visitor := visitor.GenerateVisitor(c)
	r.setBuildScheduleEnabled(c, visitor, false)
}

// setBuildScheduleEnabled 封装启用和禁用调度的公共逻辑。
// 重复设置相同状态时仍通过 update_time 校验读取后的并发更新。
func (r *restHandler) setBuildScheduleEnabled(c *gin.Context, visitor hydra.Visitor, enabled bool) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	id := c.Param("id")

	current, err := r.bss.GetByID(ctx, id)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if current == nil {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildSchedule_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if err := r.bss.UpdateEnabled(ctx, current, enabled); err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_BuildSchedule_InternalError_UpdateFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	c.Set("operation_audit_target_names", []string{current.Name})

	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}
