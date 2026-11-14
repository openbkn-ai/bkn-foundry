// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package build_schedule

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/catalog"
)

var serviceOnce sync.Once
var service interfaces.BuildScheduleService

type buildScheduleService struct {
	sa  interfaces.BuildScheduleAccess
	cs  interfaces.CatalogService
	rs  interfaces.ResourceService
	bts interfaces.BuildTaskService
}

func NewBuildScheduleService(settings *common.AppSetting, bts interfaces.BuildTaskService, rs interfaces.ResourceService) interfaces.BuildScheduleService {
	serviceOnce.Do(func() {
		service = &buildScheduleService{
			sa:  logics.BSA,
			cs:  catalog.NewCatalogService(settings),
			rs:  rs,
			bts: bts,
		}
	})
	return service
}

func validateRequest(ctx context.Context, req *interfaces.BuildScheduleRequest) error {
	if req == nil || strings.TrimSpace(req.Name) == "" || utf8.RuneCountInString(req.Name) > interfaces.NAME_MAX_LENGTH || req.ResourceID == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidParameter)
	}
	if req.ExecuteType != interfaces.BuildTaskExecuteTypeFull && req.ExecuteType != interfaces.BuildTaskExecuteTypeIncremental {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidExecuteType)
	}
	if _, err := common.ParseHourlyCronExpr(req.CronExpr); err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidCronExpr)
	}
	if req.StartTime < 0 || req.EndTime < 0 || (req.EndTime > 0 && req.StartTime > req.EndTime) {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidTimeRange)
	}
	return nil
}

func nextRun(ctx context.Context, expr string, now time.Time, start int64) (int64, error) {
	schedule, err := common.ParseHourlyCronExpr(expr)
	if err != nil {
		return 0, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidCronExpr)
	}
	from := now
	if start > now.UnixMilli() {
		from = time.UnixMilli(start).In(now.Location()).Add(-time.Nanosecond)
	}
	next := schedule.Next(from)
	if next.IsZero() {
		return 0, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidCronExpr)
	}
	return next.UnixMilli(), nil
}

func (s *buildScheduleService) requireTaskManage(ctx context.Context, id string) (*interfaces.Catalog, error) {
	allowed, cat, err := s.cs.CheckCatalogPermission(ctx, id, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}
	if cat == nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	return cat, nil
}

func (s *buildScheduleService) Create(ctx context.Context, req *interfaces.BuildScheduleRequest) (string, error) {
	if err := validateRequest(ctx, req); err != nil {
		return "", err
	}
	resource, err := s.rs.GetByID(ctx, req.ResourceID)
	if err != nil {
		return "", err
	}
	if resource == nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	if _, err = s.requireTaskManage(ctx, resource.CatalogID); err != nil {
		return "", err
	}
	if resource.Category != interfaces.ResourceCategoryTable {
		return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidParameter)
	}
	creator, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if creator.ID == "" || creator.Type == "" {
		return "", httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}
	now := time.Now()
	next, err := nextRun(ctx, req.CronExpr, now, req.StartTime)
	if err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildSchedule_InternalError_CreateFailed)
	}
	schedule := &interfaces.BuildSchedule{
		ID:          id.String(),
		Name:        req.Name,
		ResourceID:  resource.ID,
		CatalogID:   resource.CatalogID,
		CronExpr:    req.CronExpr,
		ExecuteType: req.ExecuteType,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Enabled:     req.Enabled,
		NextRun:     next,
		LastStatus:  interfaces.ScheduleStatusNever,
		Creator:     creator,
		Updater:     creator,
		CreateTime:  now.UnixMilli(),
		UpdateTime:  now.UnixMilli(),
	}
	if err = s.sa.Create(ctx, schedule); err != nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildSchedule_InternalError_CreateFailed)
	}
	return schedule.ID, nil
}

func (s *buildScheduleService) GetByID(ctx context.Context, id string) (*interfaces.BuildSchedule, error) {
	schedule, err := s.sa.GetByID(ctx, id)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
	}
	if schedule == nil {
		return nil, nil //nolint:nilnil // HTTP 层统一映射不存在结果。
	}
	if _, err = s.requireTaskManage(ctx, schedule.CatalogID); err != nil {
		return nil, err
	}
	s.populateReferences(ctx, []*interfaces.BuildSchedule{schedule})
	return schedule, nil
}

func (s *buildScheduleService) List(ctx context.Context, params interfaces.BuildScheduleQueryParams) ([]*interfaces.BuildSchedule, int64, error) {
	if params.CatalogID != "" {
		allowed, _, err := s.cs.CheckCatalogPermission(ctx, params.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true)
		if err != nil {
			if interfaces.IsPermissionRefusal(err) {
				return []*interfaces.BuildSchedule{}, 0, nil
			}
			return nil, 0, err
		}
		if !allowed {
			return []*interfaces.BuildSchedule{}, 0, nil
		}
	} else {
		visible, err := s.cs.ListPermittedCatalogIDs(ctx, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, interfaces.VISIBILITY_MATCH_ALL, interfaces.CatalogsQueryParams{})
		if err != nil {
			if interfaces.IsPermissionRefusal(err) {
				return []*interfaces.BuildSchedule{}, 0, nil
			}
			return nil, 0, err
		}
		if len(visible) == 0 {
			return []*interfaces.BuildSchedule{}, 0, nil
		}
		params.CatalogIDs = visible
	}
	schedules, total, err := s.sa.List(ctx, params)
	if err != nil {
		return nil, 0, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildSchedule_InternalError_GetFailed)
	}
	s.populateReferences(ctx, schedules)
	return schedules, total, nil
}

func (s *buildScheduleService) populateReferences(ctx context.Context, schedules []*interfaces.BuildSchedule) {
	resourceIDs := make([]string, 0, len(schedules))
	catalogIDs := make([]string, 0, len(schedules))
	resourcesSeen := map[string]bool{}
	catalogsSeen := map[string]bool{}
	for _, schedule := range schedules {
		if !resourcesSeen[schedule.ResourceID] {
			resourceIDs = append(resourceIDs, schedule.ResourceID)
			resourcesSeen[schedule.ResourceID] = true
		}
		if !catalogsSeen[schedule.CatalogID] {
			catalogIDs = append(catalogIDs, schedule.CatalogID)
			catalogsSeen[schedule.CatalogID] = true
		}
	}
	if len(resourceIDs) == 0 {
		return
	}
	resources, err := s.rs.InternalGetByIDs(ctx, resourceIDs)
	if err != nil {
		logger.Warn("Failed to populate build schedule resource names")
	}
	catalogs, err := s.cs.InternalGetByIDs(ctx, catalogIDs)
	if err != nil {
		logger.Warn("Failed to populate build schedule catalog names")
	}
	for _, schedule := range schedules {
		if res := resources[schedule.ResourceID]; res != nil {
			schedule.ResourceName = res.Name
		}
		if cat := catalogs[schedule.CatalogID]; cat != nil {
			schedule.CatalogName = cat.Name
		}
	}
}

func (s *buildScheduleService) Update(ctx context.Context, current *interfaces.BuildSchedule, req *interfaces.BuildScheduleRequest) error {
	if current == nil {
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildSchedule_NotFound)
	}
	if _, err := s.requireTaskManage(ctx, current.CatalogID); err != nil {
		return err
	}
	if err := validateRequest(ctx, req); err != nil {
		return err
	}
	if req.ExpectedUpdateTime <= 0 {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildSchedule_InvalidParameter)
	}
	if req.ResourceID != current.ResourceID {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildSchedule_ResourceMismatch)
	}
	if req.Enabled != current.Enabled {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildSchedule_EnabledFieldNotAllowed)
	}
	now := time.Now()
	next, err := nextRun(ctx, req.CronExpr, now, req.StartTime)
	if err != nil {
		return err
	}
	updater, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	updated := *current
	updated.Name = req.Name
	updated.CronExpr = req.CronExpr
	updated.ExecuteType = req.ExecuteType
	updated.StartTime = req.StartTime
	updated.EndTime = req.EndTime
	updated.NextRun = next
	updated.Updater = updater
	updated.UpdateTime = max(now.UnixMilli(), current.UpdateTime+1)
	affected, err := s.sa.Update(ctx, &updated, req.ExpectedUpdateTime)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildSchedule_InternalError_UpdateFailed)
	}
	if affected == 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildSchedule_UpdateConflict)
	}
	return nil
}

func (s *buildScheduleService) Delete(ctx context.Context, current *interfaces.BuildSchedule) error {
	if current == nil {
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildSchedule_NotFound)
	}
	if _, err := s.requireTaskManage(ctx, current.CatalogID); err != nil {
		return err
	}
	if err := s.sa.Delete(ctx, current.ID); err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildSchedule_InternalError_DeleteFailed)
	}
	return nil
}

func (s *buildScheduleService) UpdateEnabled(ctx context.Context, current *interfaces.BuildSchedule, enabled bool) error {
	return s.setEnabled(ctx, current, enabled, true)
}

func (s *buildScheduleService) InternalUpdateEnabled(ctx context.Context, current *interfaces.BuildSchedule, enabled bool) error {
	return s.setEnabled(ctx, current, enabled, false)
}

func (s *buildScheduleService) setEnabled(ctx context.Context, current *interfaces.BuildSchedule, enabled, checkPermission bool) error {
	if current == nil {
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildSchedule_NotFound)
	}
	if checkPermission {
		if _, err := s.requireTaskManage(ctx, current.CatalogID); err != nil {
			return err
		}
	}
	var next *int64
	now := time.Now()
	if enabled {
		value, err := nextRun(ctx, current.CronExpr, now, current.StartTime)
		if err != nil {
			return err
		}
		next = &value
	}
	updater, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	affected, err := s.sa.UpdateEnabled(ctx, current.ID, enabled, next, current.UpdateTime, max(now.UnixMilli(), current.UpdateTime+1), updater)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildSchedule_InternalError_UpdateFailed)
	}
	if affected == 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildSchedule_UpdateConflict)
	}
	return nil
}

func (s *buildScheduleService) ExecuteSchedule(ctx context.Context, schedule *interfaces.BuildSchedule) (interfaces.ScheduleRunResult, error) {
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, schedule.Creator)
	if schedule.Creator.ID == "" || schedule.Creator.Type == "" {
		return interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: "调度执行身份无效，请重新创建调度"}, nil
	}
	cat, err := s.requireTaskManage(ctx, schedule.CatalogID)
	if err != nil {
		return failedBuildResult(err), err
	}
	resource, err := s.rs.InternalGetByID(ctx, nil, schedule.ResourceID)
	if err != nil {
		return failedBuildResult(err), err
	}
	if resource == nil || resource.CatalogID != schedule.CatalogID {
		return interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSkipped, Message: "资源不存在或已不属于调度目录"}, nil
	}
	if !cat.Enabled || !resource.Enabled {
		return interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSkipped, Message: "资源或目录已停用"}, nil
	}
	_, err = s.bts.Create(ctx, &interfaces.CreateBuildTaskRequest{ResourceID: schedule.ResourceID, Mode: interfaces.BuildTaskModeBatch, ExecuteType: schedule.ExecuteType, ScheduleID: schedule.ID, TriggerType: interfaces.BuildTaskTriggerScheduled})
	if err != nil {
		return failedBuildResult(err), err
	}
	return interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}, nil
}

func failedBuildResult(err error) interfaces.ScheduleRunResult {
	result := interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: "无法创建构建任务，请检查资源配置或联系管理员"}
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return result
	}
	switch httpErr.ErrorCode {
	case verrors.VegaBackend_BuildTask_Exist:
		result.Status = interfaces.ScheduleStatusSkipped
		result.Message = "资源已有待执行、运行中或停止中的构建任务"
	case verrors.VegaBackend_Catalog_IsDisabled:
		result.Status = interfaces.ScheduleStatusSkipped
		result.Message = "目录已停用"
	case verrors.VegaBackend_BuildTask_InvalidParameter_PrimaryKeyFields:
		result.Message = "资源主键配置无效，无法创建构建任务"
	case verrors.VegaBackend_BuildTask_InvalidParameter_IncrementalFields:
		result.Message = "资源增量字段配置无效，无法创建构建任务"
	case verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable:
		result.Message = "增量构建缺少可用索引或有效基线"
	default:
		if httpErr.HTTPCode == http.StatusForbidden || httpErr.HTTPCode == http.StatusUnauthorized {
			result.Message = "调度执行者没有当前目录或资源的构建权限"
		}
	}
	return result
}
