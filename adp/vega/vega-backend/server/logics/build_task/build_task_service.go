// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package build_task provides BuildTask management business logic.
package build_task

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
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
	"vega-backend/logics/user_mgmt"
)

var (
	btsOnce sync.Once
	btsInst interfaces.BuildTaskService
)

type buildTaskService struct {
	appSetting *common.AppSetting
	cs         interfaces.CatalogService
	ra         interfaces.ResourceAccess
	bta        interfaces.BuildTaskAccess
	mfa        interfaces.ModelFactoryAccess
	ums        interfaces.UserMgmtService
	lim        interfaces.LocalIndexManager // 删任务时 drop 其本地索引；测试注入 mock
}

var activeBuildTaskStatuses = []string{
	interfaces.BuildTaskStatusInit,
	interfaces.BuildTaskStatusRunning,
	interfaces.BuildTaskStatusStopping,
}

// NewBuildTaskService creates a new BuildTaskService.
func NewBuildTaskService(appSetting *common.AppSetting) interfaces.BuildTaskService {
	btsOnce.Do(func() {
		btsInst = &buildTaskService{
			appSetting: appSetting,
			cs:         catalog.NewCatalogService(appSetting),
			ra:         logics.RA,
			bta:        logics.BTA,
			mfa:        logics.MFA,
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			lim:        local_index.NewLocalIndexManager(appSetting),
		}
	})
	return btsInst
}

// CreateBuildTask creates a new build task. resource_id is taken from req.
func (bts *buildTaskService) CreateBuildTask(ctx context.Context, req *interfaces.CreateBuildTaskRequest) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create build task")
	defer span.End()

	resourceID := req.ResourceID
	resource, err := bts.ra.GetByID(ctx, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}

	if resource.Category != interfaces.ResourceCategoryTable {
		span.SetStatus(codes.Error, "Resource category is not table")
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails("Resource category must be table")
	}

	cat, err := bts.cs.GetByID(ctx, resource.CatalogID, false)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if cat == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if !cat.Enabled {
		span.SetStatus(codes.Error, "Catalog is disabled")
		return "", rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
	}

	if err := bts.rejectIfResourceHasActiveTask(ctx, resourceID, ""); err != nil {
		span.SetStatus(codes.Error, "Resource already has active build task")
		return "", err
	}

	if req.Mode == interfaces.BuildTaskModeStreaming {
		primaryKeys := []any{}
		if resource.SourceMetadata != nil {
			if v, ok := resource.SourceMetadata["primary_keys"]; ok {
				primaryKeys = v.([]any)
			}
		}
		if len(primaryKeys) == 0 {
			span.SetStatus(codes.Error, "Resource has no primary key for build task")
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InternalError_CreateFailed).
				WithErrorDetails("Resource has no primary key")
		}
	}

	buildTask, err := bts.newBuildTaskFromCreateRequest(ctx, resource, req)
	if err != nil {
		return "", err
	}

	if err := bts.bta.Create(ctx, buildTask); err != nil {
		otellog.LogError(ctx, "Create build task failed", err)
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}

	// 创建即入队执行：客户端创建后不会再调 /start，不入队任务会永远停在 init（界面"排队中"）。
	// 入队失败仅记日志，任务保持 init，可由 /start 重新触发
	bts.enqueueBuildTask(ctx, buildTask, interfaces.BuildTaskExecuteTypeFull)

	span.SetStatus(codes.Ok, "")
	return buildTask.ID, nil
}

func (bts *buildTaskService) rejectIfResourceHasActiveTask(ctx context.Context, resourceID string, excludeTaskID string) error {
	tasks, _, err := bts.bta.List(ctx, interfaces.BuildTasksQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 2},
		ResourceID:            resourceID,
		Statuses:              activeBuildTaskStatuses,
	})
	if err != nil {
		otellog.LogError(ctx, "Check active build task failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	for _, task := range tasks {
		if excludeTaskID != "" && task.ID == excludeTaskID {
			continue
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_Exist).
			WithErrorDetails("Resource already has an active build task")
	}
	return nil
}

func (bts *buildTaskService) newBuildTaskFromCreateRequest(ctx context.Context, resource *interfaces.Resource, req *interfaces.CreateBuildTaskRequest) (*interfaces.BuildTask, error) {
	embeddingModel, modelDimensions, err := bts.normalizeEmbeddingModel(ctx, req.EmbeddingModel, req.EmbeddingFields, req.ModelDimensions)
	if err != nil {
		return nil, err
	}

	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	now := time.Now().UnixMilli()
	return &interfaces.BuildTask{
		ID:               xid.New().String(),
		ResourceID:       resource.ID,
		CatalogID:        resource.CatalogID,
		Status:           interfaces.BuildTaskStatusInit,
		Mode:             req.Mode,
		Creator:          accountInfo,
		CreateTime:       now,
		Updater:          accountInfo,
		UpdateTime:       now,
		EmbeddingFields:  req.EmbeddingFields,
		BuildKeyFields:   req.BuildKeyFields,
		EmbeddingModel:   embeddingModel,
		ModelDimensions:  modelDimensions,
		FulltextFields:   req.FulltextFields,
		FulltextAnalyzer: req.FulltextAnalyzer,
	}, nil
}

func (bts *buildTaskService) normalizeEmbeddingModel(ctx context.Context, embeddingModel string, embeddingFields string, modelDimensions int) (string, int, error) {
	if embeddingModel == "" && embeddingFields != "" {
		embeddingModel = interfaces.DEFAULT_EMBEDDING_MODEL
	}
	if embeddingModel == "" {
		return "", modelDimensions, nil
	}
	// embedding_model 统一归一化为模型 ID 存储：传入是模型名则解析为 ID 并补全维度；
	// 传入已是模型 ID 时 GetModelByName 按名查不到（err != nil），此时若已带维度则原样保留为 ID。
	// 既解析不到又没维度则无法建向量索引，按错误处理。
	if model, err := bts.mfa.GetModelByName(ctx, embeddingModel); err == nil {
		embeddingModel = model.ModelID
		if modelDimensions == 0 {
			modelDimensions = model.EmbeddingDim
		}
	} else if modelDimensions == 0 {
		return "", 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}
	return embeddingModel, modelDimensions, nil
}

// enqueueBuildTask 按任务模式投递到 asynq 队列；入队失败仅记录日志，任务保持当前状态
func (bts *buildTaskService) enqueueBuildTask(ctx context.Context, buildTask *interfaces.BuildTask, executeType string) {
	payload, err := sonic.Marshal(&interfaces.BatchBuildTaskMessage{
		TaskID:      buildTask.ID,
		ExecuteType: executeType,
	})
	if err != nil {
		otellog.LogError(ctx, "Marshal build task message failed", err)
		return
	}

	typename := interfaces.BuildTaskTypeBatch
	if buildTask.Mode == interfaces.BuildTaskModeStreaming {
		typename = interfaces.BuildTaskTypeStreaming
	}
	asynqTask := asynq.NewTask(typename, payload)
	client := logics.AQA.CreateClient()
	if _, err := client.Enqueue(asynqTask,
		asynq.Queue(interfaces.DefaultQueue),
		asynq.MaxRetry(interfaces.BUILD_TASK_MAX_RETRY_COUNT),
		asynq.Timeout(math.MaxInt64),
		asynq.Deadline(time.Unix(math.MaxInt64/1000000000, math.MaxInt64%1000000000)),
	); err != nil {
		otellog.LogError(ctx, "Enqueue build task failed", err)
	} else {
		logger.Infof("Build task %s enqueued for execution", buildTask.ID)
	}
}

// GetBuildTaskByID retrieves a build task by ID.
func (bts *buildTaskService) GetBuildTaskByID(ctx context.Context, id string) (*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get build task")
	defer span.End()

	buildTask, err := bts.bta.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get build task failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if buildTask == nil {
		span.SetStatus(codes.Error, "Build task not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound)
	}

	accountInfos := []*interfaces.AccountInfo{&buildTask.Creator, &buildTask.Updater}
	if err := bts.ums.GetAccountNames(ctx, accountInfos); err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_BuildTask_InternalError_GetAccountNamesFailed).WithErrorDetails(err.Error())
	}

	buildTask.IndexHealth = computeIndexHealth(buildTask)
	span.SetStatus(codes.Ok, "")
	return buildTask, nil
}

// computeIndexHealth 按当前计数派生各索引健康度（不落库）。embedding 与 fulltext
// 相互独立：fulltext 随同步即时生效，建了即 ok；embedding 要等向量写满才算 ok。
// 仅在终态给出 ok/partial/failed，进行中统一 building，避免把中途进度误报成失败。
func computeIndexHealth(bt *interfaces.BuildTask) *interfaces.IndexHealth {
	h := &interfaces.IndexHealth{Embedding: "none", Fulltext: "none"}
	if bt.FulltextFields != "" {
		h.Fulltext = "ok"
	}
	switch {
	case bt.EmbeddingFields == "":
		h.Embedding = "none"
	case bt.Status == interfaces.BuildTaskStatusRunning || bt.Status == interfaces.BuildTaskStatusInit:
		h.Embedding = "building"
	case bt.SyncedCount == 0:
		// 无数据可向量化，空索引视为可用
		h.Embedding = "ok"
	case bt.VectorizedCount >= bt.SyncedCount:
		h.Embedding = "ok"
	case bt.VectorizedCount == 0:
		h.Embedding = "failed"
	default:
		h.Embedding = "partial"
	}
	h.Usable = h.Embedding == "none" || h.Embedding == "ok"
	return h
}

// GetBuildTaskByResourceID retrieves a build task by resource ID.
func (bts *buildTaskService) GetBuildTaskByResourceID(ctx context.Context, resourceID string) (*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get build task by resource ID")
	defer span.End()

	buildTask, err := bts.bta.GetByResourceID(ctx, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get build task failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	if buildTask != nil {
		accountInfos := []*interfaces.AccountInfo{&buildTask.Creator, &buildTask.Updater}
		if err := bts.ums.GetAccountNames(ctx, accountInfos); err != nil {
			span.SetStatus(codes.Error, "GetAccountNames error")
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_BuildTask_InternalError_GetAccountNamesFailed).WithErrorDetails(err.Error())
		}
		buildTask.IndexHealth = computeIndexHealth(buildTask)
	}

	span.SetStatus(codes.Ok, "")
	return buildTask, nil
}

// ListBuildTasks retrieves build tasks with filters and pagination.
func (bts *buildTaskService) ListBuildTasks(ctx context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List build tasks")
	defer span.End()

	buildTasks, total, err := bts.bta.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List build tasks failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(buildTasks)*2)
	for _, bt := range buildTasks {
		accountInfos = append(accountInfos, &bt.Creator, &bt.Updater)
		bt.IndexHealth = computeIndexHealth(bt)
	}
	if err := bts.ums.GetAccountNames(ctx, accountInfos); err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_BuildTask_InternalError_GetAccountNamesFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return buildTasks, total, nil
}

// StartBuildTask transitions a task from {init/stopped/completed, failed task auto retry} to running.
// Note: persisted status remains init/stopped/completed until the worker picks it up — clients should poll.
func (bts *buildTaskService) StartBuildTask(ctx context.Context, taskID string, executeType string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Start build task")
	defer span.End()

	if executeType == "" {
		executeType = interfaces.BuildTaskExecuteTypeIncremental
	}
	if executeType != interfaces.BuildTaskExecuteTypeIncremental && executeType != interfaces.BuildTaskExecuteTypeFull {
		span.SetStatus(codes.Error, "Invalid execute type")
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidExecuteType).
			WithErrorDetails("Invalid execute type")
	}

	buildTask, err := bts.bta.GetByID(ctx, taskID)
	if err != nil {
		span.SetStatus(codes.Error, "Get build task failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if buildTask == nil {
		span.SetStatus(codes.Error, "Build task not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound)
	}
	if executeType == interfaces.BuildTaskExecuteTypeFull && buildTask.Status != interfaces.BuildTaskStatusFailed {
		span.SetStatus(codes.Error, "Invalid full rebuild state")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails("full rebuild is only allowed for failed tasks; create a new build task instead")
	}
	// failed 也允许重启：否则失败任务成死胡同，只能删除重建
	if buildTask.Status != interfaces.BuildTaskStatusInit &&
		buildTask.Status != interfaces.BuildTaskStatusStopped &&
		buildTask.Status != interfaces.BuildTaskStatusCompleted &&
		buildTask.Status != interfaces.BuildTaskStatusFailed {
		span.SetStatus(codes.Error, "Invalid state transition for start")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails(fmt.Sprintf("cannot start task in status: %s", buildTask.Status))
	}

	cat, err := bts.cs.GetByID(ctx, buildTask.CatalogID, false)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if cat == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if !cat.Enabled {
		span.SetStatus(codes.Error, "Catalog is disabled")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
	}

	if err := bts.rejectIfResourceHasActiveTask(ctx, buildTask.ResourceID, buildTask.ID); err != nil {
		span.SetStatus(codes.Error, "Resource already has active build task")
		return err
	}

	// 入队前先置回 init：worker 出队时会跳过 stopped/stopping 的任务
	// （防止排队中被停止的任务复活），stopped 状态直接入队会被误跳过。
	// running 仍由 worker 实际执行时落账。
	if buildTask.Status != interfaces.BuildTaskStatusInit {
		if err := bts.bta.UpdateStatus(ctx, taskID, map[string]any{"status": interfaces.BuildTaskStatusInit}); err != nil {
			otellog.LogError(ctx, "Update build task status failed", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_UpdateFailed).
				WithErrorDetails(err.Error())
		}
	}

	bts.enqueueBuildTask(ctx, buildTask, executeType)

	span.SetStatus(codes.Ok, "")
	return nil
}

// StopBuildTask transitions a task from running to stopping.
// Note: persisted status remains running until the worker advances it — clients should poll.
func (bts *buildTaskService) StopBuildTask(ctx context.Context, taskID string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Stop build task")
	defer span.End()

	buildTask, err := bts.bta.GetByID(ctx, taskID)
	if err != nil {
		span.SetStatus(codes.Error, "Get build task failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if buildTask == nil {
		span.SetStatus(codes.Error, "Build task not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound)
	}
	if buildTask.Status != interfaces.BuildTaskStatusRunning &&
		buildTask.Status != interfaces.BuildTaskStatusStopping &&
		buildTask.Status != interfaces.BuildTaskStatusInit {
		span.SetStatus(codes.Error, "Invalid state transition for stop")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails(fmt.Sprintf("cannot stop task in status: %s", buildTask.Status))
	}

	// running → stopping：通知 worker 在批间检查点退出。
	// stopping → stopped：兜底强制落停。worker 已不在（asynq 任务耗尽重试/服务重启）
	// 时 stopping 永远不会被推进，任务卡死无法删除，二次 stop 即强制完结。
	// init → stopped：排队中尚无 worker 观察 stopping，直接落停；
	// 出队时 worker 检查到 stopped 即跳过，不会复活执行。
	targetStatus := interfaces.BuildTaskStatusStopping
	if buildTask.Status == interfaces.BuildTaskStatusStopping ||
		buildTask.Status == interfaces.BuildTaskStatusInit {
		targetStatus = interfaces.BuildTaskStatusStopped
	}
	updates := map[string]any{
		"status": targetStatus,
	}
	if err := bts.bta.UpdateStatus(ctx, taskID, updates); err != nil {
		otellog.LogError(ctx, "Update build task status failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteBuildTasks atomically deletes build tasks by IDs after pre-validating existence and status.
//
// Behavior:
//   - Loads each id; if any missing, returns 404 BuildTask.NotFound with {missing_ids: [...]}
//     unless ignoreMissing=true (then missing ids are dropped from the delete set).
//   - If any task is in running/stopping status, returns 409 HasRunningExecution with {running_ids: [...]}.
//     This check cannot be bypassed.
//   - If any task owns the resource's current LocalIndexName, returns 409 ActiveIndexInUse
//     unless deleteActiveIndex=true. When deleteActiveIndex=true, clears LocalIndexName before deleting.
//   - Deletes pass-through tasks one-by-one. Mid-loop errors return 500 (rare, bounded by pre-validation).
func (bts *buildTaskService) DeleteBuildTasks(ctx context.Context, ids []string, ignoreMissing bool, deleteActiveIndex bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete build tasks")
	defer span.End()

	toDelete := make([]*interfaces.BuildTask, 0, len(ids))
	missingIDs := make([]string, 0)
	runningIDs := make([]string, 0)
	activeIndexes := make([]map[string]string, 0)
	activeResources := make(map[string]*interfaces.Resource)

	for _, id := range ids {
		buildTask, err := bts.bta.GetByID(ctx, id)
		if err != nil {
			span.SetStatus(codes.Error, "Get build task failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}
		if buildTask == nil {
			missingIDs = append(missingIDs, id)
			continue
		}
		if buildTask.Status == interfaces.BuildTaskStatusRunning || buildTask.Status == interfaces.BuildTaskStatusStopping {
			runningIDs = append(runningIDs, id)
			continue
		}
		resource, err := bts.ra.GetByID(ctx, buildTask.ResourceID)
		if err != nil {
			span.SetStatus(codes.Error, "Get resource failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}
		if resource != nil {
			idx := interfaces.BuildIndexName(buildTask.ResourceID, buildTask.ID)
			if resource.LocalIndexName == idx {
				activeIndexes = append(activeIndexes, map[string]string{
					"resource_id":   buildTask.ResourceID,
					"build_task_id": buildTask.ID,
					"index_name":    idx,
				})
				activeResources[buildTask.ID] = resource
			}
		}
		toDelete = append(toDelete, buildTask)
	}

	if len(runningIDs) > 0 {
		span.SetStatus(codes.Error, "Some tasks are running or stopping")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_HasRunningExecution).
			WithErrorDetails(map[string]any{"running_ids": runningIDs})
	}
	if len(missingIDs) > 0 && !ignoreMissing {
		span.SetStatus(codes.Error, "Some build tasks not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound).
			WithErrorDetails(map[string]any{"missing_ids": missingIDs})
	}
	if len(activeIndexes) > 0 && !deleteActiveIndex {
		span.SetStatus(codes.Error, "Some build task indexes are currently used by resources")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_ActiveIndexInUse).
			WithErrorDetails(map[string]any{"active_indexes": activeIndexes})
	}
	if deleteActiveIndex {
		for taskID, resource := range activeResources {
			resource.LocalIndexName = ""
			if err := bts.ra.Update(ctx, resource); err != nil {
				span.SetStatus(codes.Error, "Clear active local index failed")
				return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
					WithErrorDetails(map[string]any{
						"build_task_id": taskID,
						"resource_id":   resource.ID,
						"error":         err.Error(),
					})
			}
		}
	}

	for _, bt := range toDelete {
		// 先 drop 索引（尽力，失败仅记日志），再删任务行——与删资源/删 catalog 的级联
		// 语义一致，避免 UI 单任务删除留下孤儿索引（#66 只覆盖了资源/目录两条路径）。
		idx := interfaces.BuildIndexName(bt.ResourceID, bt.ID)
		if err := bts.lim.DeleteIndex(ctx, idx); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Drop index %s for build task %s failed", idx, bt.ID), err)
		}
		if err := bts.bta.Delete(ctx, bt.ID); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Delete build task %s failed", bt.ID), err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_DeleteFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
