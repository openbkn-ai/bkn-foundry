// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package semantic_understanding_task provides semantic-understanding task management.
package semantic_understanding_task

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/rs/xid"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	resourcelogic "vega-backend/logics/resource"
	"vega-backend/logics/resource_data"
	"vega-backend/logics/user_mgmt"
)

var (
	sutServiceOnce sync.Once
	sutService     interfaces.SemanticUnderstandingTaskService
)

const semanticTaskWakeupBuffer = 1

type semanticUnderstandingTaskService struct {
	appSetting *common.AppSetting
	cs         interfaces.CatalogService
	rs         interfaces.ResourceService
	rds        interfaces.ResourceDataService
	suta       interfaces.SemanticUnderstandingTaskAccess
	ums        interfaces.UserMgmtService

	wakeCh chan struct{}
}

func NewSemanticUnderstandingTaskService(appSetting *common.AppSetting) interfaces.SemanticUnderstandingTaskService {
	sutServiceOnce.Do(func() {
		sutService = &semanticUnderstandingTaskService{
			appSetting: appSetting,
			cs:         catalog.NewCatalogService(appSetting),
			rs:         resourcelogic.NewResourceService(appSetting),
			rds:        resource_data.NewResourceDataService(appSetting),
			suta:       logics.SUTA,
			ums:        user_mgmt.NewUserMgmtService(appSetting),

			wakeCh: make(chan struct{}, semanticTaskWakeupBuffer),
		}
	})
	return sutService
}

func (suts *semanticUnderstandingTaskService) DispatchSignal() <-chan struct{} {
	return suts.wakeCh
}

func (suts *semanticUnderstandingTaskService) RequestDispatch() {
	select {
	case suts.wakeCh <- struct{}{}:
	default:
	}
}

func (suts *semanticUnderstandingTaskService) CreateResourceTask(ctx context.Context, resourceID string, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.CreateResourceTask")
	defer span.End()

	if resourceID == "" {
		span.SetStatus(codes.Error, "Resource id is required")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("resource_id is required")
	}

	resource, err := suts.rs.InternalGetByID(ctx, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}

	// InternalGetByID above deliberately skips authorization, so this is the only
	// thing standing between an unauthorized caller and a task that reads the
	// table's unmasked sample rows (bkn-studio#342).
	if err := suts.rs.CheckResourcePermission(ctx, resourceID, interfaces.OPERATION_TYPE_TASK_MANAGE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return nil, err
	}

	task, err := normalizeResourceSemanticUnderstandingRequest(resource, req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid semantic understanding task request")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails(err.Error())
	}
	if req.IncludeSampleRows {
		// Sample rows are the table's actual contents, sent unmasked to the
		// semantic service and then kept in the task's input snapshot. Asking for
		// them is a data read, so it needs query_data on top of task_manage —
		// otherwise task_manage alone would be a way around the read permission
		// (#571).
		if err := suts.rs.CheckResourcePermission(ctx, resourceID,
			interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
			span.SetStatus(codes.Error, "Permission denied")
			return nil, err
		}
		if _, err := resourcelogic.EnsureResourceQueryable(ctx, resource); err != nil {
			logger.Warnf("Skipping semantic sample rows because resource is not queryable: resource_id=%s, category=%s, error=%v", resource.ID, resource.Category, err)
		} else if err := suts.attachUnmaskedSampleRows(ctx, resource, task); err != nil {
			logger.Warnf("Failed to attach semantic sample rows; continuing without samples: resource_id=%s, category=%s, error=%v", resource.ID, resource.Category, err)
		}
	}
	return suts.createTask(ctx, task)
}

func (suts *semanticUnderstandingTaskService) CreateCatalogTask(ctx context.Context, catalogID string, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.CreateCatalogTask")
	defer span.End()

	if catalogID == "" {
		span.SetStatus(codes.Error, "Catalog id is required")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("catalog_id is required")
	}

	catalog, err := suts.cs.InternalGetByID(ctx, catalogID, false)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	resources, err := suts.rs.InternalGetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog resources failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}

	// Same hole as the resource path: the catalog was fetched internally, which
	// skips authorization (bkn-studio#342).
	if err := suts.cs.CheckCatalogPermission(ctx, catalogID, interfaces.OPERATION_TYPE_TASK_MANAGE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return nil, err
	}

	task, err := normalizeCatalogSemanticUnderstandingRequest(catalog, resources, req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid semantic understanding task request")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails(err.Error())
	}
	return suts.createTask(ctx, task)
}

func (suts *semanticUnderstandingTaskService) createTask(ctx context.Context, task *interfaces.SemanticUnderstandingTask) (*interfaces.SemanticUnderstandingTask, error) {
	activeTask, err := suts.suta.FindActiveByInputHash(ctx, task.Scope, task.InputHash)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	if activeTask != nil {
		return activeTask, nil
	}

	accountInfo := accountInfoFromContext(ctx)

	now := time.Now().UnixMilli()
	task.ID = xid.New().String()
	task.Status = interfaces.SemanticUnderstandingTaskStatusPending
	task.Creator = accountInfo
	task.CreateTime = now
	if err := suts.suta.Create(ctx, task); err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_CreateResourcesFailed).
			WithErrorDetails(err.Error())
	}
	suts.RequestDispatch()

	return task, nil
}

// checkTaskPermission authorizes an operation on one semantic-understanding task
// through whatever it was created against: a resource-scoped task is judged on
// its table, a catalog-scoped one on its catalog.
//
// Reads matter as much as writes here. The task's input snapshot holds the
// table's unmasked sample rows, so an unguarded detail endpoint hands out
// exactly what the creation guard was added to protect (#571).
func (suts *semanticUnderstandingTaskService) checkTaskPermission(ctx context.Context,
	task *interfaces.SemanticUnderstandingTask, op string) error {

	if task.Scope == interfaces.SemanticUnderstandingTaskScopeResource && task.ResourceID != "" {
		resource, err := suts.rs.InternalGetByID(ctx, task.ResourceID)
		if err != nil {
			return err
		}
		if resource != nil {
			return suts.rs.CheckResourcePermission(ctx, task.ResourceID, op)
		}
		// 表已被删除,任务不随之级联删除、只会被 worker 标成 cancelled。此时判在
		// 已消失的父上会永远 403——任务既看不了也删不掉,永久滞留在列表里。往上
		// 退到它所属的目录。
	}
	if task.CatalogID != "" {
		// InternalGetByID 对不存在的目录返回 404 错误而不是 (nil, nil),所以这里
		// 要按「取不到就是没了」处理,不能把错误直接抛出去——否则删掉整个目录之后,
		// 它下面的任务连兜底那一级都走不到,谁都读不了也删不掉。
		catalog, err := suts.cs.InternalGetByID(ctx, task.CatalogID, false)
		if err == nil && catalog != nil {
			return suts.cs.CheckCatalogPermission(ctx, task.CatalogID, op)
		}
	}
	// 父都不在了。这类孤儿只剩清理价值,判在目录类型的通配授权上:持有它的人本来
	// 就看得见每一个目录,多看一条没有父的任务不构成新的暴露,而没有这条出路,
	// 孤儿任务就是永久滞留。
	//
	// 问的是 catalog:* 而不是 resource:*——后者在本次收敛里只剩两个读动词,拿它
	// 判 task_manage 会永远为假,兜底就又成了死代码。
	if typeWide, err := suts.cs.HasTypeWideGrant(ctx, op); err != nil {
		return err
	} else if typeWide {
		return nil
	}
	return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
		WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", op))
}

func (suts *semanticUnderstandingTaskService) GetByID(ctx context.Context, id string) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.GetByID")
	defer span.End()

	task, err := suts.suta.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get semantic understanding task failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	if task == nil {
		span.SetStatus(codes.Error, "Semantic understanding task not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_SemanticUnderstandingTask_NotFound)
	}
	if err := suts.checkTaskPermission(ctx, task, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return nil, err
	}
	if err := suts.populateSemanticUnderstandingTaskReferences(ctx, []*interfaces.SemanticUnderstandingTask{task}); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate semantic understanding task references: %v", err)
	}
	if err := suts.ums.GetAccountNames(ctx, []*interfaces.AccountInfo{&task.Creator}); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate semantic understanding task account names: %v", err)
	}
	return task, nil
}

func (suts *semanticUnderstandingTaskService) InternalGetByID(ctx context.Context, id string) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalGetByID")
	defer span.End()

	task, err := suts.suta.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get semantic understanding task failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	return task, nil
}

func (suts *semanticUnderstandingTaskService) InternalSetApplied(ctx context.Context, tx *sql.Tx, id string, applied bool, applyDetailJSON string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalSetApplied")
	defer span.End()

	return suts.suta.SetApplied(ctx, tx, id, applied, applyDetailJSON)
}

func (suts *semanticUnderstandingTaskService) List(ctx context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.List")
	defer span.End()

	// A task is listed through the parent it was created against (#269): a
	// resource-scoped one through its table, a catalog-scoped one through its
	// catalog. Both are decided over the page that came back rather than inside
	// the query, for the same reason the build task listing does it — and with
	// the same consequence: total is unfiltered and pages may come back short.

	tasks, total, err := suts.suta.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List semantic understanding tasks failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	{
		tasks, err = suts.filterTasksByParent(ctx, tasks)
		if err != nil {
			span.SetStatus(codes.Error, "Filter tasks by parent failed")
			return nil, 0, err
		}
	}

	if err := suts.populateSemanticUnderstandingTaskSummaryReferences(ctx, tasks); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate semantic understanding task references: %v", err)
	}
	return tasks, total, nil
}

func (suts *semanticUnderstandingTaskService) InternalList(ctx context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTaskSummary, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalList")
	defer span.End()

	tasks, err := suts.suta.InternalList(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List semantic understanding tasks failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return tasks, nil
}

// PopulateSemanticUnderstandingTaskSummaryReferences batch task completion list associated directory and resources display name.
func (suts *semanticUnderstandingTaskService) populateSemanticUnderstandingTaskSummaryReferences(ctx context.Context, tasks []*interfaces.SemanticUnderstandingTaskSummary) error {
	catalogIDs := make([]string, 0, len(tasks))
	catalogIDSet := make(map[string]struct{}, len(tasks))
	resourceIDs := make([]string, 0, len(tasks))
	resourceIDSet := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.CatalogID != "" {
			if _, exists := catalogIDSet[task.CatalogID]; !exists {
				catalogIDSet[task.CatalogID] = struct{}{}
				catalogIDs = append(catalogIDs, task.CatalogID)
			}
		}
		if task.ResourceID != "" {
			if _, exists := resourceIDSet[task.ResourceID]; !exists {
				resourceIDSet[task.ResourceID] = struct{}{}
				resourceIDs = append(resourceIDs, task.ResourceID)
			}
		}
	}

	var referenceErrors []error
	resourcesByID := make(map[string]*interfaces.Resource, len(resourceIDs))
	if len(resourceIDs) > 0 {
		resources, err := suts.rs.InternalGetByIDs(ctx, resourceIDs)
		if err != nil {
			referenceErrors = append(referenceErrors, err)
		} else {
			for _, resource := range resources {
				resourcesByID[resource.ID] = resource
			}
		}
	}
	catalogsByID := make(map[string]*interfaces.Catalog, len(catalogIDs))
	if len(catalogIDs) > 0 {
		catalogs, err := suts.cs.InternalGetByIDs(ctx, catalogIDs)
		if err != nil {
			referenceErrors = append(referenceErrors, err)
		} else {
			for _, catalog := range catalogs {
				catalogsByID[catalog.ID] = catalog
			}
		}
	}
	for _, task := range tasks {
		if resource := resourcesByID[task.ResourceID]; resource != nil {
			task.ResourceName = resource.Name
		}
		if catalog := catalogsByID[task.CatalogID]; catalog != nil {
			task.CatalogName = catalog.Name
		}
	}
	return errors.Join(referenceErrors...)
}

// PopulateSemanticUnderstandingTaskReferences batch task associated directory and resource, filling the page display name.
func (suts *semanticUnderstandingTaskService) populateSemanticUnderstandingTaskReferences(ctx context.Context, tasks []*interfaces.SemanticUnderstandingTask) error {
	catalogIDs := make([]string, 0, len(tasks))
	catalogIDSet := make(map[string]struct{}, len(tasks))
	resourceIDs := make([]string, 0, len(tasks))
	resourceIDSet := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.CatalogID != "" {
			if _, exists := catalogIDSet[task.CatalogID]; !exists {
				catalogIDSet[task.CatalogID] = struct{}{}
				catalogIDs = append(catalogIDs, task.CatalogID)
			}
		}
		if task.ResourceID != "" {
			if _, exists := resourceIDSet[task.ResourceID]; !exists {
				resourceIDSet[task.ResourceID] = struct{}{}
				resourceIDs = append(resourceIDs, task.ResourceID)
			}
		}
	}

	var referenceErrors []error
	resourcesByID := make(map[string]*interfaces.Resource, len(resourceIDs))
	if len(resourceIDs) > 0 {
		resources, err := suts.rs.InternalGetByIDs(ctx, resourceIDs)
		if err != nil {
			referenceErrors = append(referenceErrors, err)
		} else {
			for _, resource := range resources {
				resourcesByID[resource.ID] = resource
			}
		}
	}
	catalogsByID := make(map[string]*interfaces.Catalog, len(catalogIDs))
	if len(catalogIDs) > 0 {
		catalogs, err := suts.cs.InternalGetByIDs(ctx, catalogIDs)
		if err != nil {
			referenceErrors = append(referenceErrors, err)
		} else {
			for _, catalog := range catalogs {
				catalogsByID[catalog.ID] = catalog
			}
		}
	}
	for _, task := range tasks {
		if resource := resourcesByID[task.ResourceID]; resource != nil {
			task.ResourceName = resource.Name
		}
		if catalog := catalogsByID[task.CatalogID]; catalog != nil {
			task.CatalogName = catalog.Name
		}
	}
	return errors.Join(referenceErrors...)
}

func (suts *semanticUnderstandingTaskService) DeleteByIDs(ctx context.Context, ids []string, ignoreMissing bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.DeleteByIDs")
	defer span.End()

	seen := make(map[string]struct{}, len(ids))
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	tasks, err := suts.suta.GetByIDs(ctx, uniqueIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Get semantic understanding tasks failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}

	// Checked before anything is deleted: a batch is one transaction, so one
	// unauthorized id stops the whole request rather than deleting the rest.
	for _, task := range tasks {
		if err := suts.checkTaskPermission(ctx, task, interfaces.OPERATION_TYPE_TASK_MANAGE); err != nil {
			span.SetStatus(codes.Error, "Permission denied")
			return err
		}
	}

	toDelete := make([]string, 0, len(tasks))
	runningIDs := make([]string, 0)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.Status == interfaces.SemanticUnderstandingTaskStatusPending || task.Status == interfaces.SemanticUnderstandingTaskStatusRunning {
			runningIDs = append(runningIDs, task.ID)
			continue
		}
		toDelete = append(toDelete, task.ID)
	}

	if len(runningIDs) > 0 {
		span.SetStatus(codes.Error, "Some semantic understanding tasks are pending or running")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_HasRunningExecution).
			WithErrorDetails(map[string]any{"running_ids": runningIDs})
	}
	if len(tasks) != len(uniqueIDs) && !ignoreMissing {
		taskByID := make(map[string]struct{}, len(tasks))
		for _, task := range tasks {
			if task != nil {
				taskByID[task.ID] = struct{}{}
			}
		}
		missingIDs := make([]string, 0, len(uniqueIDs)-len(tasks))
		for _, id := range uniqueIDs {
			if _, ok := taskByID[id]; !ok {
				missingIDs = append(missingIDs, id)
			}
		}
		span.SetStatus(codes.Error, "Some semantic understanding tasks not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_SemanticUnderstandingTask_NotFound).
			WithErrorDetails(map[string]any{"missing_ids": missingIDs})
	}

	if _, err := suts.suta.DeleteByIDs(ctx, toDelete); err != nil {
		span.SetStatus(codes.Error, "Delete semantic understanding tasks failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_SemanticUnderstandingTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (suts *semanticUnderstandingTaskService) InternalMarkRunning(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalMarkRunning")
	defer span.End()

	return suts.suta.MarkRunning(ctx, id, time.Now().UnixMilli())
}

func (suts *semanticUnderstandingTaskService) InternalSetAgentTaskID(ctx context.Context, id string, agentTaskID string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalSetAgentTaskID")
	defer span.End()

	if agentTaskID == "" {
		return false, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails("agent_task_id is required")
	}
	return suts.suta.SetAgentTaskID(ctx, id, agentTaskID)
}

func (suts *semanticUnderstandingTaskService) InternalMarkCompleted(ctx context.Context, tx *sql.Tx, id string, resultJSON string, confidence float64, confidenceDetailJSON string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalMarkCompleted")
	defer span.End()

	if confidence < 0 || confidence > 1 {
		return false, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails("confidence must be between 0 and 1")
	}
	return suts.suta.MarkCompleted(ctx, tx, id, resultJSON, confidence, confidenceDetailJSON, time.Now().UnixMilli())
}

func (suts *semanticUnderstandingTaskService) InternalMarkFailed(ctx context.Context, id string, failureDetail string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalMarkFailed")
	defer span.End()

	return suts.suta.MarkFailed(ctx, id, failureDetail, time.Now().UnixMilli())
}

func (suts *semanticUnderstandingTaskService) InternalMarkCancelled(ctx context.Context, id string, failureDetail string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.InternalMarkCancelled")
	defer span.End()

	return suts.suta.MarkCancelled(ctx, id, failureDetail, time.Now().UnixMilli())
}

func normalizeResourceSemanticUnderstandingRequest(resource *interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	normalized := defaultSemanticUnderstandingRequest()
	if req != nil {
		*normalized = *req
		if req.ApplyMode == "" {
			normalized.ApplyMode = interfaces.SemanticUnderstandingApplyModeFillEmpty
		}
		if normalized.ConfidenceThreshold == nil {
			defaultThreshold := interfaces.DefaultSemanticUnderstandingConfidenceThreshold
			normalized.ConfidenceThreshold = &defaultThreshold
		}
	}
	if err := validateSemanticUnderstandingRequest(normalized); err != nil {
		return nil, err
	}
	if normalized.IncludeSampleRows {
		if normalized.SamplePolicy == nil {
			return nil, fmt.Errorf("sample_policy is required when include_sample_rows is true")
		}
		if normalized.SamplePolicy.Masked {
			return nil, fmt.Errorf("masked sample rows are not supported yet")
		}
		if normalized.SamplePolicy.MaxRows <= 0 || normalized.SamplePolicy.MaxRows > interfaces.MaxSemanticUnderstandingSampleRows {
			return nil, fmt.Errorf("sample_policy.max_rows must be between 1 and %d", interfaces.MaxSemanticUnderstandingSampleRows)
		}
	}
	input, inputHash, err := buildResourceSemanticUnderstandingInput(resource, normalized)
	if err != nil {
		return nil, err
	}
	return &interfaces.SemanticUnderstandingTask{
		Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
		CatalogID:           resource.CatalogID,
		ResourceID:          resource.ID,
		AgentID:             interfaces.SemanticUnderstandingResourceAgentID,
		Input:               input,
		InputHash:           inputHash,
		ApplyMode:           normalized.ApplyMode,
		ConfidenceThreshold: *normalized.ConfidenceThreshold,
	}, nil
}

func (suts *semanticUnderstandingTaskService) attachUnmaskedSampleRows(ctx context.Context, resource *interfaces.Resource, task *interfaces.SemanticUnderstandingTask) error {
	if suts.rds == nil {
		return fmt.Errorf("resource data service is unavailable")
	}
	var input interfaces.SemanticUnderstandingResourceAgentInput
	if err := sonic.Unmarshal([]byte(task.Input), &input); err != nil {
		return fmt.Errorf("unmarshal semantic understanding input: %w", err)
	}
	if input.Options.SamplePolicy == nil || input.Options.SamplePolicy.Masked {
		return fmt.Errorf("unmasked sample policy is required")
	}
	fields := make([]string, 0, len(resource.SchemaDefinition))
	for _, property := range resource.SchemaDefinition {
		if property != nil && property.Name != "" {
			fields = append(fields, property.Name)
		}
	}
	result, err := suts.rds.QueryWithPaging(ctx, resource,
		&interfaces.ResourceDataQueryParams{
			Limit:        input.Options.SamplePolicy.MaxRows,
			OutputFields: fields,
		})
	if err != nil {
		return fmt.Errorf("read sample rows: %w", err)
	}
	input.SampleRows = []map[string]any{}
	if result != nil && result.Entries != nil {
		var truncated bool
		input.SampleRows, truncated, err = limitSemanticUnderstandingSampleRows(result.Entries)
		if err != nil {
			return fmt.Errorf("limit sample rows: %w", err)
		}
		if truncated {
			logger.Warnf("Semantic sample rows truncated by payload cap: resource_id=%s, category=%s, kept %d of %d rows", resource.ID, resource.Category, len(input.SampleRows), len(result.Entries))
		}
	}
	inputJSON, _, err := marshalSemanticUnderstandingInput(input)
	if err != nil {
		return fmt.Errorf("marshal semantic understanding input: %w", err)
	}
	task.Input = inputJSON
	return nil
}

// limitSemanticUnderstandingSampleRows keeps sample data useful for semantic
// inference without allowing large text or binary values to exhaust the task
// input or agent context. It never mutates connector query results.
func limitSemanticUnderstandingSampleRows(rows []map[string]any) ([]map[string]any, bool, error) {
	limited := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if len(limited) >= interfaces.MaxSemanticUnderstandingSampleRows {
			break
		}
		limitedRow := make(map[string]any, len(row))
		for key, value := range row {
			limitedRow[key] = limitSemanticUnderstandingSampleValue(value)
		}

		candidate := append(limited, limitedRow)
		candidateJSON, err := sonic.Marshal(candidate)
		if err != nil {
			return nil, false, err
		}
		if len(candidateJSON) > interfaces.MaxSemanticUnderstandingSamplePayloadBytes {
			return limited, true, nil
		}
		limited = candidate
	}
	return limited, false, nil
}

func limitSemanticUnderstandingSampleValue(value any) any {
	switch typedValue := value.(type) {
	case string:
		// Table connectors convert []byte values to string before returning rows.
		// Invalid UTF-8 therefore represents binary data in the actual query path.
		if !utf8.ValidString(typedValue) {
			return semanticUnderstandingBinarySampleValue(len(typedValue))
		}
		return truncateSemanticUnderstandingSampleString(typedValue)
	case []byte:
		return semanticUnderstandingBinarySampleValue(len(typedValue))
	case map[string]any:
		limited := make(map[string]any, len(typedValue))
		for key, nestedValue := range typedValue {
			limited[key] = limitSemanticUnderstandingSampleValue(nestedValue)
		}
		return limited
	case []any:
		limited := make([]any, len(typedValue))
		for index, nestedValue := range typedValue {
			limited[index] = limitSemanticUnderstandingSampleValue(nestedValue)
		}
		return limited
	default:
		return value
	}
}

func truncateSemanticUnderstandingSampleString(value string) string {
	if len(value) <= interfaces.MaxSemanticUnderstandingSampleValueRunes || utf8.RuneCountInString(value) <= interfaces.MaxSemanticUnderstandingSampleValueRunes {
		return value
	}
	runeCount := 0
	for byteIndex := range value {
		if runeCount == interfaces.MaxSemanticUnderstandingSampleValueRunes-1 {
			return value[:byteIndex] + "…"
		}
		runeCount++
	}
	return value
}

func semanticUnderstandingBinarySampleValue(length int) string {
	return fmt.Sprintf("[binary content omitted; original length: %d bytes]", length)
}

func normalizeCatalogSemanticUnderstandingRequest(catalog *interfaces.Catalog, resources []*interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	normalized := defaultSemanticUnderstandingRequest()
	if req != nil {
		*normalized = *req
		if req.ApplyMode == "" {
			normalized.ApplyMode = interfaces.SemanticUnderstandingApplyModeFillEmpty
		}
		if normalized.ConfidenceThreshold == nil {
			defaultThreshold := interfaces.DefaultSemanticUnderstandingConfidenceThreshold
			normalized.ConfidenceThreshold = &defaultThreshold
		}
	}
	if normalized.IncludeSampleRows || normalized.SamplePolicy != nil {
		return nil, fmt.Errorf("sample rows are only supported for resource semantic understanding task")
	}
	if err := validateSemanticUnderstandingRequest(normalized); err != nil {
		return nil, err
	}
	input, inputHash, err := buildCatalogSemanticUnderstandingInput(catalog, resources, normalized)
	if err != nil {
		return nil, err
	}
	return &interfaces.SemanticUnderstandingTask{
		Scope:               interfaces.SemanticUnderstandingTaskScopeCatalog,
		CatalogID:           catalog.ID,
		AgentID:             interfaces.SemanticUnderstandingCatalogAgentID,
		Input:               input,
		InputHash:           inputHash,
		ApplyMode:           normalized.ApplyMode,
		ConfidenceThreshold: *normalized.ConfidenceThreshold,
	}, nil
}

func validateSemanticUnderstandingRequest(req *interfaces.CreateSemanticUnderstandingTaskRequest) error {
	switch req.ApplyMode {
	case interfaces.SemanticUnderstandingApplyModeDryRun,
		interfaces.SemanticUnderstandingApplyModeFillEmpty,
		interfaces.SemanticUnderstandingApplyModeForce:
	default:
		return fmt.Errorf("invalid apply_mode")
	}
	if req.ConfidenceThreshold == nil || *req.ConfidenceThreshold < 0 || *req.ConfidenceThreshold > 1 {
		return fmt.Errorf("confidence_threshold must be between 0 and 1")
	}
	return nil
}

func defaultSemanticUnderstandingRequest() *interfaces.CreateSemanticUnderstandingTaskRequest {
	defaultThreshold := interfaces.DefaultSemanticUnderstandingConfidenceThreshold
	return &interfaces.CreateSemanticUnderstandingTaskRequest{
		ApplyMode:           interfaces.SemanticUnderstandingApplyModeFillEmpty,
		ConfidenceThreshold: &defaultThreshold,
	}
}

func buildResourceSemanticUnderstandingInput(resource *interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (string, string, error) {
	input := interfaces.SemanticUnderstandingResourceAgentInput{
		Resource:   buildResourceAgentInputResource(resource),
		SampleRows: []map[string]any{},
		Options: interfaces.SemanticUnderstandingResourceAgentInputOptions{
			Language:            interfaces.DefaultSemanticUnderstandingLanguage,
			ApplyMode:           req.ApplyMode,
			ConfidenceThreshold: *req.ConfidenceThreshold,
			IncludeSampleRows:   req.IncludeSampleRows,
			SamplePolicy:        req.SamplePolicy,
		},
	}
	return marshalSemanticUnderstandingInput(input)
}

func buildCatalogSemanticUnderstandingInput(catalog *interfaces.Catalog, resources []*interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (string, string, error) {
	sort.SliceStable(resources, func(i, j int) bool {
		return resources[i].ID < resources[j].ID
	})

	input := interfaces.SemanticUnderstandingCatalogAgentInput{
		Catalog: interfaces.SemanticUnderstandingCatalogAgentInputCatalog{
			ID:          catalog.ID,
			Name:        catalog.Name,
			Description: catalog.Description,
		},
		Resources:          []interfaces.SemanticUnderstandingCatalogAgentInputResource{},
		ExistingLogicViews: []interfaces.SemanticUnderstandingCatalogAgentInputExistingView{},
		Options: interfaces.SemanticUnderstandingCatalogAgentInputOptions{
			Language:            interfaces.DefaultSemanticUnderstandingLanguage,
			ApplyMode:           req.ApplyMode,
			ConfidenceThreshold: *req.ConfidenceThreshold,
		},
	}
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		if resource.Category == interfaces.ResourceCategoryLogicView {
			input.ExistingLogicViews = append(input.ExistingLogicViews, buildCatalogAgentInputExistingView(resource))
			continue
		}
		input.Resources = append(input.Resources, buildCatalogAgentInputResource(resource))
	}
	return marshalSemanticUnderstandingInput(input)
}

func buildResourceAgentInputResource(resource *interfaces.Resource) interfaces.SemanticUnderstandingResourceAgentInputResource {
	return interfaces.SemanticUnderstandingResourceAgentInputResource{
		ID:               resource.ID,
		Name:             resource.Name,
		Category:         resource.Category,
		Schema:           resource.Schema,
		SourceIdentifier: resource.SourceIdentifier,
		Description:      resource.Description,
		SchemaDefinition: buildResourceAgentInputProperties(resource.SchemaDefinition),
	}
}

func buildCatalogAgentInputResource(resource *interfaces.Resource) interfaces.SemanticUnderstandingCatalogAgentInputResource {
	return interfaces.SemanticUnderstandingCatalogAgentInputResource{
		ID:               resource.ID,
		Name:             resource.Name,
		Description:      resource.Description,
		Schema:           resource.Schema,
		SourceIdentifier: resource.SourceIdentifier,
		Keys:             buildCatalogAgentInputResourceKeys(resource.SourceMetadata),
		Fields:           buildCatalogAgentInputProperties(resource.SchemaDefinition),
	}
}

func buildCatalogAgentInputExistingView(resource *interfaces.Resource) interfaces.SemanticUnderstandingCatalogAgentInputExistingView {
	return interfaces.SemanticUnderstandingCatalogAgentInputExistingView{
		ID:               resource.ID,
		Name:             resource.Name,
		SourceIdentifier: resource.SourceIdentifier,
		Description:      resource.Description,
		Status:           resource.Status,
	}
}

func buildCatalogAgentInputResourceKeys(sourceMetadata map[string]any) *interfaces.SemanticUnderstandingCatalogAgentInputResourceKeys {
	if len(sourceMetadata) == 0 {
		return nil
	}

	keys := &interfaces.SemanticUnderstandingCatalogAgentInputResourceKeys{
		Primary: getCatalogAgentInputStringSlice(sourceMetadata["primary_keys"]),
	}
	for _, rawIndex := range getCatalogAgentInputMapSlice(sourceMetadata["indices"]) {
		unique, _ := rawIndex["unique"].(bool)
		primary, _ := rawIndex["primary"].(bool)
		if !unique || primary {
			continue
		}
		if columns := getCatalogAgentInputStringSlice(rawIndex["columns"]); len(columns) > 0 {
			keys.Unique = append(keys.Unique, columns)
		}
	}
	if len(keys.Primary) == 0 && len(keys.Unique) == 0 {
		return nil
	}
	return keys
}

func getCatalogAgentInputStringSlice(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if stringValue, ok := item.(string); ok && stringValue != "" {
				result = append(result, stringValue)
			}
		}
		return result
	default:
		return nil
	}
}

func getCatalogAgentInputMapSlice(value any) []map[string]any {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(values))
	for _, item := range values {
		if mapValue, ok := item.(map[string]any); ok {
			result = append(result, mapValue)
		}
	}
	return result
}

func buildCatalogAgentInputProperties(properties []*interfaces.Property) []interfaces.SemanticUnderstandingCatalogAgentInputProperty {
	result := make([]interfaces.SemanticUnderstandingCatalogAgentInputProperty, 0, len(properties))
	for _, property := range properties {
		if property == nil {
			continue
		}
		result = append(result, interfaces.SemanticUnderstandingCatalogAgentInputProperty{
			Name:        property.Name,
			DisplayName: property.DisplayName,
			Type:        property.Type,
			Description: property.Description,
		})
	}
	return result
}

func buildResourceAgentInputProperties(properties []*interfaces.Property) []interfaces.SemanticUnderstandingResourceAgentInputProperty {
	result := make([]interfaces.SemanticUnderstandingResourceAgentInputProperty, 0, len(properties))
	for _, property := range properties {
		if property == nil {
			continue
		}
		result = append(result, interfaces.SemanticUnderstandingResourceAgentInputProperty{
			Name:                property.Name,
			Type:                property.Type,
			OriginalName:        property.OriginalName,
			OriginalType:        property.OriginalType,
			OriginalDescription: property.OriginalDescription,
			DisplayName:         property.DisplayName,
			Description:         property.Description,
		})
	}
	return result
}

func marshalSemanticUnderstandingInput(input any) (string, string, error) {
	inputBytes, err := sonic.ConfigStd.Marshal(input)
	if err != nil {
		return "", "", err
	}
	inputJSON := string(inputBytes)
	sum := sha256.Sum256([]byte(inputJSON))
	return inputJSON, hex.EncodeToString(sum[:]), nil
}

func accountInfoFromContext(ctx context.Context) interfaces.AccountInfo {
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		if ai, ok := v.(interfaces.AccountInfo); ok {
			return ai
		}
	}
	return interfaces.AccountInfo{}
}

// filterTasksByParent keeps the tasks whose parent the caller may read, asking
// only about the parents on this page. Used when the visible set was too large
// to push into the query.
func (suts *semanticUnderstandingTaskService) filterTasksByParent(ctx context.Context,
	tasks []*interfaces.SemanticUnderstandingTaskSummary) ([]*interfaces.SemanticUnderstandingTaskSummary, error) {

	resourceIDs := make([]string, 0, len(tasks))
	catalogIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t.Scope == interfaces.SemanticUnderstandingTaskScopeResource && t.ResourceID != "" {
			resourceIDs = append(resourceIDs, t.ResourceID)
			continue
		}
		if t.CatalogID != "" {
			catalogIDs = append(catalogIDs, t.CatalogID)
		}
	}
	allowedResources, err := suts.rs.FilterAuthorizedResources(ctx, resourceIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	allowedCatalogs, err := suts.cs.FilterAuthorizedCatalogs(ctx, catalogIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	visible := make([]*interfaces.SemanticUnderstandingTaskSummary, 0, len(tasks))
	for _, t := range tasks {
		if t.Scope == interfaces.SemanticUnderstandingTaskScopeResource && t.ResourceID != "" {
			if allowedResources[t.ResourceID] {
				visible = append(visible, t)
			}
			continue
		}
		if t.CatalogID != "" && allowedCatalogs[t.CatalogID] {
			visible = append(visible, t)
		}
	}
	tasks = visible
	return visible, nil
}
