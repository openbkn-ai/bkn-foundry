// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// CascadeDeleteBuildTasks 删除 filter 命中的所有构建任务及其本地索引，
// 让"删资源"/"删数据连接(catalog)"不再留下孤儿任务行或孤儿索引。
//
// filter 须设 ResourceID（删单个资源）或 CatalogID（删整个 catalog 下全部资源）之一。
// 任一命中任务处于 running/stopping → 整体拒绝（HasRunningExecution），不删任何东西，
// 避免删一半留下不一致；用户需先停止再删。
//
// 索引 drop 失败仅记日志、不阻断（与既有"索引删除失败不影响资源删除"语义一致）；
// 任务行删除失败才返回错误。放在 logics 包是因为它同时被 resource 与 catalog 两个
// service 复用，而 logics/build_task 反向依赖 logics/catalog（放那会成环）。
func CascadeDeleteBuildTasks(ctx context.Context, bta interfaces.BuildTaskAccess, lim interfaces.LocalIndexManager, filter interfaces.BuildTasksQueryParams) error {
	// Limit=0 → 不分页，取全部命中任务（含历史任务，连同其孤儿索引一并清）
	filter.Limit = 0
	filter.Offset = 0
	tasks, err := bta.InternalList(ctx, filter)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	// 先整体校验运行态：有任务在跑就拒绝，绝不删一半
	running := make([]string, 0)
	for _, t := range tasks {
		if t.Status == interfaces.BuildTaskStatusRunning || t.Status == interfaces.BuildTaskStatusStopping {
			running = append(running, t.ID)
		}
	}
	if len(running) > 0 {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_HasRunningExecution).
			WithErrorDetails(map[string]any{"running_ids": running})
	}

	// Drop indexes on a best-effort basis, then delete all task rows together.
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		idx := interfaces.BuildIndexName(t.ResourceID, t.ID)
		if err := lim.DeleteIndex(ctx, idx); err != nil {
			logger.Errorf("cascade delete: drop index %s failed: %v", idx, err)
		}
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	deleted, err := bta.DeleteByIDs(ctx, ids)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}
	if deleted != int64(len(ids)) {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_DeleteFailed).
			WithErrorDetails(map[string]any{"expected": len(ids), "deleted": deleted})
	}
	return nil
}
