// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package discover_task provides DiscoverTask business logic.
package discover_task

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/rs/xid"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	"vega-backend/logics/user_mgmt"
)

var (
	dtServiceOnce sync.Once
	dtService     interfaces.DiscoverTaskService
)

const discoverTaskDispatchBuffer = 1

type discoverTaskService struct {
	appSetting *common.AppSetting
	cs         interfaces.CatalogService
	dta        interfaces.DiscoverTaskAccess
	ums        interfaces.UserMgmtService

	dispatchCh chan struct{}
}

// NewDiscoverTaskService creates or returns the singleton DiscoverTaskService.
func NewDiscoverTaskService(appSetting *common.AppSetting) interfaces.DiscoverTaskService {
	dtServiceOnce.Do(func() {
		dtService = &discoverTaskService{
			appSetting: appSetting,
			cs:         catalog.NewCatalogService(appSetting),
			dta:        logics.DTA,
			ums:        user_mgmt.NewUserMgmtService(appSetting),

			dispatchCh: make(chan struct{}, discoverTaskDispatchBuffer),
		}
	})
	return dtService
}

func (dts *discoverTaskService) DispatchSignal() <-chan struct{} {
	return dts.dispatchCh
}

func (dts *discoverTaskService) RequestDispatch() {
	select {
	case dts.dispatchCh <- struct{}{}:
	default:
	}
}

// Create persists a new DiscoverTask and notifies the local database-backed worker.
// Create 创建一个新的发现任务
// 参数:
//   - ctx: 上下文，用于传递请求范围的数据和取消信号
//   - catalogID: 目录ID，用于标识要执行发现任务的目录
//
// 返回值:
//   - string: 创建的任务ID
//   - error: 错误信息，如果创建失败则返回错误
func (dts *discoverTaskService) Create(ctx context.Context, req *interfaces.CreateDiscoverTaskRequest) (string, error) {
	// 使用分布式追踪系统创建一个span，用于追踪服务调用
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.Create")
	defer span.End() // 确保span在函数结束时结束

	// Get account info from context
	accountInfo := interfaces.AccountInfo{}
	if ai, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo); ok {
		accountInfo = ai
	}

	now := time.Now().UnixMilli()
	task := &interfaces.DiscoverTask{
		ID:          xid.New().String(),
		CatalogID:   req.CatalogID,
		ScheduleID:  req.ScheduleID,
		Strategy:    req.Strategy,
		TriggerType: req.TriggerType,
		Status:      interfaces.DiscoverTaskStatusPending,
		Progress:    0,
		Message:     "",
		Creator:     accountInfo,
		CreateTime:  now,
	}

	// 1. Write to database
	if err := dts.dta.Create(ctx, task); err != nil {
		otellog.LogError(ctx, "Failed to create discover task", err)
		return "", err
	}

	dts.RequestDispatch()

	return task.ID, nil
}

// CreateScheduled method removed - scheduled tasks are now managed by DiscoverScheduleService

// GetByID retrieves a DiscoverTask by ID.
func (dts *discoverTaskService) GetByID(ctx context.Context, id string) (*interfaces.DiscoverTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.GetByID")
	defer span.End()

	task, err := dts.dta.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get discover task failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if task == nil {
		span.SetStatus(codes.Error, "Discover task not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_DiscoverTask_NotFound)
	}
	if err := dts.populateDiscoverTaskReferences(ctx, []*interfaces.DiscoverTask{task}); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate discover task references: %v", err)
	}
	if err := dts.ums.GetAccountNames(ctx, []*interfaces.AccountInfo{&task.Creator}); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate discover task account names: %v", err)
	}
	return task, nil
}

func (dts *discoverTaskService) InternalGetByID(ctx context.Context, id string) (*interfaces.DiscoverTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalGetByID")
	defer span.End()

	task, err := dts.dta.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return task, nil
}

// List lists DiscoverTasks for a catalog.
func (dts *discoverTaskService) List(ctx context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.List")
	defer span.End()

	tasks, total, err := dts.dta.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List discover tasks failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if err := dts.populateDiscoverTaskSummaryReferences(ctx, tasks); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate discover task references: %v", err)
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(tasks))
	for _, t := range tasks {
		accountInfos = append(accountInfos, &t.Creator)
	}
	if err := dts.ums.GetAccountNames(ctx, accountInfos); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate discover task account names: %v", err)
	}
	return tasks, total, nil
}

func (dts *discoverTaskService) InternalList(ctx context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalList")
	defer span.End()

	tasks, err := dts.dta.InternalList(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List discover tasks failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return tasks, nil
}

// populateDiscoverTaskSummaryReferences 批量补齐当前页任务关联目录的展示名称。
func (dts *discoverTaskService) populateDiscoverTaskSummaryReferences(ctx context.Context, tasks []*interfaces.DiscoverTaskSummary) error {
	catalogIDs := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.CatalogID == "" {
			continue
		}
		if _, exists := seen[task.CatalogID]; !exists {
			seen[task.CatalogID] = struct{}{}
			catalogIDs = append(catalogIDs, task.CatalogID)
		}
	}
	if len(catalogIDs) == 0 {
		return nil
	}

	catalogs, err := dts.cs.InternalGetByIDs(ctx, catalogIDs)
	if err != nil {
		return err
	}
	catalogsByID := make(map[string]*interfaces.Catalog, len(catalogs))
	for _, catalog := range catalogs {
		catalogsByID[catalog.ID] = catalog
	}
	for _, task := range tasks {
		if catalog := catalogsByID[task.CatalogID]; catalog != nil {
			task.CatalogName = catalog.Name
		}
	}
	return nil
}

// populateDiscoverTaskReferences 批量补齐任务关联目录的展示名称。
func (dts *discoverTaskService) populateDiscoverTaskReferences(ctx context.Context, tasks []*interfaces.DiscoverTask) error {
	catalogIDs := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.CatalogID == "" {
			continue
		}
		if _, exists := seen[task.CatalogID]; !exists {
			seen[task.CatalogID] = struct{}{}
			catalogIDs = append(catalogIDs, task.CatalogID)
		}
	}
	if len(catalogIDs) == 0 {
		return nil
	}

	catalogs, err := dts.cs.InternalGetByIDs(ctx, catalogIDs)
	if err != nil {
		return err
	}
	catalogsByID := make(map[string]*interfaces.Catalog, len(catalogs))
	for _, catalog := range catalogs {
		catalogsByID[catalog.ID] = catalog
	}
	for _, task := range tasks {
		if catalog := catalogsByID[task.CatalogID]; catalog != nil {
			task.CatalogName = catalog.Name
		}
	}
	return nil
}

func (dts *discoverTaskService) InternalMarkRunning(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalMarkRunning")
	defer span.End()

	return dts.dta.MarkRunning(ctx, id, time.Now().UnixMilli())
}

func (dts *discoverTaskService) InternalUpdateProgress(ctx context.Context, id string, progress int, message string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalUpdateProgress")
	defer span.End()

	return dts.dta.UpdateProgress(ctx, id, progress, message, time.Now().UnixMilli())
}

func (dts *discoverTaskService) InternalMarkCancelled(ctx context.Context, id string, message string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalMarkCancelled")
	defer span.End()

	return dts.dta.MarkCancelled(ctx, id, message, time.Now().UnixMilli())
}

func (dts *discoverTaskService) InternalMarkFailed(ctx context.Context, id string, message string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalMarkFailed")
	defer span.End()

	return dts.dta.MarkFailed(ctx, id, message, time.Now().UnixMilli())
}

func (dts *discoverTaskService) InternalMarkCompleted(ctx context.Context, id string, result *interfaces.DiscoverResult) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalMarkCompleted")
	defer span.End()

	return dts.dta.MarkCompleted(ctx, id, result, time.Now().UnixMilli())
}

// Delete atomically deletes discover tasks by IDs after pre-validating existence and status.
//
// Behavior:
//   - Input ids are de-duplicated.
//   - Loads each id; if any task is in pending/running, returns 409 HasRunningExecution
//     with {running_ids: [...]}. This check cannot be bypassed.
//   - If any id is missing, returns 404 NotFound with {missing_ids: [...]} unless
//     ignoreMissing=true (then missing ids are silently dropped from the delete set).
//   - Deletes all validated tasks in one database statement.
func (dts *discoverTaskService) DeleteByIDs(ctx context.Context, ids []string, ignoreMissing bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.DeleteByIDs")
	defer span.End()

	// Dedupe ids while preserving order.
	seen := make(map[string]struct{}, len(ids))
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	toDelete := make([]string, 0, len(uniqueIDs))
	missingIDs := make([]string, 0)
	runningIDs := make([]string, 0)

	for _, id := range uniqueIDs {
		task, err := dts.dta.GetByID(ctx, id)
		if err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Get discover_task %s failed", id), err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverTask_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}
		if task == nil {
			missingIDs = append(missingIDs, id)
			continue
		}
		if task.Status == interfaces.DiscoverTaskStatusPending || task.Status == interfaces.DiscoverTaskStatusRunning {
			runningIDs = append(runningIDs, id)
			continue
		}
		toDelete = append(toDelete, id)
	}

	if len(runningIDs) > 0 {
		span.SetStatus(codes.Error, "Some tasks are pending or running")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_DiscoverTask_HasRunningExecution).
			WithErrorDetails(map[string]any{"running_ids": runningIDs})
	}
	if len(missingIDs) > 0 && !ignoreMissing {
		span.SetStatus(codes.Error, "Some discover tasks not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_DiscoverTask_NotFound).
			WithErrorDetails(map[string]any{"missing_ids": missingIDs})
	}
	if len(toDelete) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	deleted, err := dts.dta.DeleteByIDs(ctx, toDelete)
	if err != nil {
		otellog.LogError(ctx, "Delete discover tasks failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}
	if deleted != int64(len(toDelete)) {
		err = fmt.Errorf("expected to delete %d discover tasks, deleted %d", len(toDelete), deleted)
		otellog.LogError(ctx, "Delete discover tasks affected unexpected rows", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
