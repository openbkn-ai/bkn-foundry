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
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/rs/xid"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/user_mgmt"
)

var (
	dtServiceOnce sync.Once
	dtService     interfaces.DiscoverTaskService
)

const debugQueueSize = 100

type discoverTaskService struct {
	appSetting *common.AppSetting
	client     *asynq.Client
	dta        interfaces.DiscoverTaskAccess
	ums        interfaces.UserMgmtService

	debugTaskQueue chan *asynq.Task
}

// NewDiscoverTaskService creates or returns the singleton DiscoverTaskService.
func NewDiscoverTaskService(appSetting *common.AppSetting) interfaces.DiscoverTaskService {
	dtServiceOnce.Do(func() {
		var client *asynq.Client
		if !common.GetDebugMode() && logics.AQA != nil {
			client = logics.AQA.CreateClient()
		}
		dtService = &discoverTaskService{
			appSetting: appSetting,
			client:     client,
			dta:        logics.DTA,
			ums:        user_mgmt.NewUserMgmtService(appSetting),

			debugTaskQueue: make(chan *asynq.Task, debugQueueSize),
		}
	})
	return dtService
}

// DebugTaskQueue returns the in-process discover task queue used in DEBUG_MODE.
func (dts *discoverTaskService) DebugTaskQueue() <-chan *asynq.Task {
	return dts.debugTaskQueue
}

// Create creates a new DiscoverTask and enqueues it to the task queue.
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

	if err := dts.enqueueTask(ctx, task.ID); err != nil {
		return "", err
	}

	return task.ID, nil
}

func (dts *discoverTaskService) enqueueTask(ctx context.Context, taskID string) error {
	payload, err := sonic.Marshal(&interfaces.DiscoverTaskMessage{
		TaskID: taskID,
	})
	if err != nil {
		otellog.LogError(ctx, "Failed to marshal discover task", err)
		return err
	}

	asynqTask := asynq.NewTask(interfaces.DiscoverTaskType, payload)
	if common.GetDebugMode() || dts.client == nil {
		dts.debugTaskQueue <- asynqTask
		logger.Infof("Enqueued debug discover task: id=%s, type=%s", taskID, asynqTask.Type())
		return nil
	}

	info, err := dts.client.Enqueue(asynqTask,
		asynq.Queue(interfaces.HighQueue),
		asynq.MaxRetry(interfaces.TaskMaxRetryCount),
		asynq.Timeout(math.MaxInt64),
		asynq.Deadline(time.Unix(math.MaxInt64/1000000000, math.MaxInt64%1000000000)),
	)
	if err != nil {
		otellog.LogError(ctx, "Failed to enqueue discover task", err)
		return err
	}

	logger.Infof("Enqueued discover task: id=%s, type=%s, queue=%s", info.ID, info.Type, info.Queue)
	return nil
}

// CreateScheduled method removed - scheduled tasks are now managed by DiscoverScheduleService

// GetByID retrieves a DiscoverTask by ID.
func (dts *discoverTaskService) GetByID(ctx context.Context, id string) (*interfaces.DiscoverTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.GetByID")
	defer span.End()

	task, err := dts.dta.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		span.SetStatus(codes.Error, "Discover task not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_DiscoverTask_NotFound)
	}
	if err := dts.ums.GetAccountNames(ctx, []*interfaces.AccountInfo{&task.Creator}); err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_DiscoverTask_InternalError_GetAccountNamesFailed).WithErrorDetails(err.Error())
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
func (dts *discoverTaskService) List(ctx context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTask, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.List")
	defer span.End()

	tasks, total, err := dts.dta.List(ctx, params)
	if err != nil {
		return nil, 0, err
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(tasks))
	for _, t := range tasks {
		accountInfos = append(accountInfos, &t.Creator)
	}
	if err := dts.ums.GetAccountNames(ctx, accountInfos); err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_DiscoverTask_InternalError_GetAccountNamesFailed).WithErrorDetails(err.Error())
	}
	return tasks, total, nil
}

// UpdateStatus updates a DiscoverTask's status.
func (dts *discoverTaskService) UpdateStatus(ctx context.Context, id, status, message string, stime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.UpdateStatus")
	defer span.End()

	return dts.dta.UpdateStatus(ctx, id, status, message, stime)
}

func (dts *discoverTaskService) InternalUpdateStatus(ctx context.Context, id, status, message string, stime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalUpdateStatus")
	defer span.End()

	return dts.dta.UpdateStatus(ctx, id, status, message, stime)
}

// UpdateResult updates a DiscoverTask's result.
func (dts *discoverTaskService) UpdateResult(ctx context.Context, id string, result *interfaces.DiscoverResult, stime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.UpdateResult")
	defer span.End()

	return dts.dta.UpdateResult(ctx, id, result, stime)
}

func (dts *discoverTaskService) InternalUpdateResult(ctx context.Context, id string, result *interfaces.DiscoverResult, stime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.InternalUpdateResult")
	defer span.End()

	return dts.dta.UpdateResult(ctx, id, result, stime)
}

// CheckExistByStatuses checks if DiscoverTasks exists by catalog ID and statuses.
func (dts *discoverTaskService) CheckExistByStatuses(ctx context.Context, catalogID string, statuses []string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.CheckExistByStatuses")
	defer span.End()

	return dts.dta.CheckExistByStatuses(ctx, catalogID, statuses)
}

// Delete atomically deletes discover tasks by IDs after pre-validating existence and status.
//
// Behavior:
//   - Input ids are de-duplicated.
//   - Loads each id; if any task is in pending/running, returns 409 HasRunningExecution
//     with {running_ids: [...]}. This check cannot be bypassed.
//   - If any id is missing, returns 404 NotFound with {missing_ids: [...]} unless
//     ignoreMissing=true (then missing ids are silently dropped from the delete set).
//   - Deletes pass-through tasks one-by-one. Mid-loop errors return 500 (rare, bounded
//     by pre-validation).
func (dts *discoverTaskService) Delete(ctx context.Context, ids []string, ignoreMissing bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverTaskService.Delete")
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

	for _, id := range toDelete {
		if err := dts.dta.Delete(ctx, id); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Delete discover_task %s failed", id), err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverTask_InternalError_DeleteFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
