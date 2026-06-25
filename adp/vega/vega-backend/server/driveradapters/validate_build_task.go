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
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kweaver-ai/kweaver-go-lib/rest"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// parseBuildTaskListParams 解析并校验 GET /build-tasks 的全部 query:
// 分页(offset/limit)、排序(order_by/order)、过滤(status 多值 / active 快捷 / mode)。
// 排序与过滤均下沉服务端,排序全局先于分页(见 build_task_access.List),
// 保证「构建中」永远排在第一页;total_count 始终为过滤后全量条数。
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
	orderBy := common.GetQueryOrDefault(c, "order_by", interfaces.DEFAULT_BUILD_TASK_ORDER_BY)
	if !isValidBuildTaskOrderBy(orderBy) {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Sort).
			WithErrorDetails(fmt.Sprintf("invalid order_by: %s", orderBy))
	}
	order := common.GetQueryOrDefault(c, "order", interfaces.DEFAULT_BUILD_TASK_ORDER)
	if order != interfaces.ASC_DIRECTION && order != interfaces.DESC_DIRECTION {
		return params, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Direction).
			WithErrorDetails(fmt.Sprintf("invalid order: %s", order))
	}
	params.OrderBy = orderBy
	params.Order = order

	// 过滤:active=true 快捷 = running+init,优先于 status;否则解析 status 多值(逗号分隔)
	if active, _ := strconv.ParseBool(c.Query("active")); active {
		params.Statuses = []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusInit}
	} else if raw := c.Query("status"); raw != "" {
		statuses, err := parseBuildTaskStatuses(ctx, raw)
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

// parseBuildTaskStatuses 把逗号分隔的 status 拆成切片,逐个校验为后端枚举;任一非法即报错。
// 空段(多余逗号/空白)跳过。
func parseBuildTaskStatuses(ctx context.Context, raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	statuses := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		if !isValidBuildTaskStatus(s) {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidStatus).
				WithErrorDetails(fmt.Sprintf("invalid status: %s", s))
		}
		statuses = append(statuses, s)
	}
	return statuses, nil
}

func isValidBuildTaskOrderBy(o string) bool {
	switch o {
	case interfaces.BuildTaskOrderByDefault,
		interfaces.BuildTaskOrderByCreatedAt,
		interfaces.BuildTaskOrderByUpdatedAt,
		interfaces.BuildTaskOrderByStatus,
		interfaces.BuildTaskOrderByMode:
		return true
	}
	return false
}

func isValidBuildTaskStatus(s string) bool {
	switch s {
	case interfaces.BuildTaskStatusInit,
		interfaces.BuildTaskStatusRunning,
		interfaces.BuildTaskStatusStopping,
		interfaces.BuildTaskStatusStopped,
		interfaces.BuildTaskStatusCompleted,
		interfaces.BuildTaskStatusFailed:
		return true
	}
	return false
}

func isValidBuildTaskMode(m string) bool {
	switch m {
	case interfaces.BuildTaskModeStreaming,
		interfaces.BuildTaskModeBatch:
		return true
	}
	return false
}
