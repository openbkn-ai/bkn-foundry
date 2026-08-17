// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_schedule

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
	dsServiceOnce sync.Once
	dsService     interfaces.DiscoverScheduleService
)

type discoverScheduleService struct {
	appSetting *common.AppSetting
	cs         interfaces.CatalogService
	dsa        interfaces.DiscoverScheduleAccess
	dts        interfaces.DiscoverTaskService
	ums        interfaces.UserMgmtService
}

func (dss *discoverScheduleService) UpdateRunMetadata(
	ctx context.Context, id string, scheduleUpdateTime, scheduleNextRun, lastRun, nextRun int64,
) error {
	return dss.dsa.UpdateRunMetadata(ctx, id, scheduleUpdateTime, scheduleNextRun, lastRun, nextRun)
}

// NewDiscoverScheduleService creates a new DiscoverScheduleService.
func NewDiscoverScheduleService(appSetting *common.AppSetting, dts interfaces.DiscoverTaskService) interfaces.DiscoverScheduleService {
	dsServiceOnce.Do(func() {
		dsService = &discoverScheduleService{
			appSetting: appSetting,
			cs:         catalog.NewCatalogService(appSetting),
			dsa:        logics.DSA,
			dts:        dts,
			ums:        user_mgmt.NewUserMgmtService(appSetting),
		}
	})
	return dsService
}

/**
 * Create a timed discovery task service
 * @param ctx Context.context Context information is used to pass the data of the request scope and the cancellation signal
 * @ param the req * interfaces in DiscoverSchedule regularly find task request structure
 * @return string returns the created task ID
 * @return error returns errors that may occur during the operation process
 */
func (dss *discoverScheduleService) Create(ctx context.Context, req *interfaces.DiscoverScheduleRequest) (string, error) {
	// 使用OpenTelemetry追踪请求执行过程
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.Create")
	defer span.End()

	// Validate cron expression
	if req.CronExpr == "" {
		otellog.LogError(ctx, "Cron expression is required", nil)
		return "", fmt.Errorf("cron_expr is required")
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}

	nowTime := time.Now()
	nextRun, err := calculateScheduleNextRun(req.CronExpr, nowTime, req.StartTime)
	if err != nil {
		return "", err
	}
	now := nowTime.UnixMilli()
	schedule := &interfaces.DiscoverSchedule{
		ID:        xid.New().String(),
		Name:      req.Name,
		CatalogID: req.CatalogID,
		CronExpr:  req.CronExpr,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Enabled:   req.Enabled,
		Strategy:  req.Strategy,
		NextRun:   nextRun.UnixMilli(),

		Creator:    accountInfo,
		CreateTime: now,
		Updater:    accountInfo,
		UpdateTime: now,
	}

	// Create schedule
	if err := dss.dsa.Create(ctx, schedule); err != nil {
		otellog.LogError(ctx, "Failed to create discover schedule", err)
		return "", err
	}

	logger.Infof("Created discover schedule: id=%s, catalog_id=%s, cron=%s",
		schedule.ID, schedule.CatalogID, schedule.CronExpr)
	return schedule.ID, nil
}

// GetByID retrieves a discover schedule by ID.
func (dss *discoverScheduleService) GetByID(ctx context.Context, id string) (*interfaces.DiscoverSchedule, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.GetByID")
	defer span.End()

	schedule, err := dss.dsa.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get discover schedule failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverSchedule_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if schedule != nil {
		if err := dss.populateDiscoverScheduleReferences(ctx, []*interfaces.DiscoverSchedule{schedule}); err != nil {
			span.RecordError(err)
			logger.Warnf("Failed to populate discover schedule references: %v", err)
		}
		if err := dss.ums.GetAccountNames(ctx, []*interfaces.AccountInfo{&schedule.Creator, &schedule.Updater}); err != nil {
			span.RecordError(err)
			logger.Warnf("Failed to populate discover schedule account names: %v", err)
		}
	}
	return schedule, nil
}

// List lists discover schedules.
func (dss *discoverScheduleService) List(ctx context.Context, params interfaces.DiscoverScheduleQueryParams) ([]*interfaces.DiscoverSchedule, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.List")
	defer span.End()

	schedules, total, err := dss.dsa.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List discover schedules failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_DiscoverSchedule_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if err := dss.populateDiscoverScheduleReferences(ctx, schedules); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate discover schedule references: %v", err)
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(schedules)*2)
	for _, s := range schedules {
		accountInfos = append(accountInfos, &s.Creator, &s.Updater)
	}
	if err := dss.ums.GetAccountNames(ctx, accountInfos); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate discover schedule account names: %v", err)
	}
	return schedules, total, nil
}

// PopulateDiscoverScheduleReferences batch scheduling, filling the page link directory name.
func (dss *discoverScheduleService) populateDiscoverScheduleReferences(ctx context.Context, schedules []*interfaces.DiscoverSchedule) error {
	catalogIDs := make([]string, 0, len(schedules))
	seen := make(map[string]struct{}, len(schedules))
	for _, schedule := range schedules {
		if schedule.CatalogID == "" {
			continue
		}
		if _, exists := seen[schedule.CatalogID]; !exists {
			seen[schedule.CatalogID] = struct{}{}
			catalogIDs = append(catalogIDs, schedule.CatalogID)
		}
	}
	if len(catalogIDs) == 0 {
		return nil
	}

	catalogs, err := dss.cs.InternalGetByIDs(ctx, catalogIDs)
	if err != nil {
		return err
	}
	catalogsByID := make(map[string]*interfaces.Catalog, len(catalogs))
	for _, catalog := range catalogs {
		catalogsByID[catalog.ID] = catalog
	}
	for _, schedule := range schedules {
		if catalog := catalogsByID[schedule.CatalogID]; catalog != nil {
			schedule.CatalogName = catalog.Name
		}
	}
	return nil
}

// Update updates a discover schedule.
func (dss *discoverScheduleService) Update(ctx context.Context, schedule *interfaces.DiscoverSchedule, req *interfaces.DiscoverScheduleRequest) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.Update")
	defer span.End()

	if schedule == nil {
		return fmt.Errorf("discover schedule not found")
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}

	schedule.CronExpr = req.CronExpr
	schedule.Name = req.Name
	schedule.StartTime = req.StartTime
	schedule.EndTime = req.EndTime
	schedule.Strategy = req.Strategy
	schedule.Updater = accountInfo
	now := time.Now()
	nextRun, err := calculateScheduleNextRun(schedule.CronExpr, now, schedule.StartTime)
	if err != nil {
		return err
	}
	schedule.NextRun = nextRun.UnixMilli()
	schedule.UpdateTime = now.UnixMilli()

	// Update schedule
	if err := dss.dsa.Update(ctx, schedule); err != nil {
		otellog.LogError(ctx, "Failed to update discover schedule", err)
		return err
	}
	logger.Infof("Updated discover schedule: id=%s", schedule.ID)
	return nil
}

// Delete deletes a discover schedule by ID.
func (dss *discoverScheduleService) Delete(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.Delete")
	defer span.End()

	// Delete schedule
	if err := dss.dsa.Delete(ctx, id); err != nil {
		otellog.LogError(ctx, "Failed to delete discover schedule", err)
		return err
	}

	logger.Infof("Deleted discover schedule: id=%s", id)
	return nil
}

// Enable enables a discover schedule.
func (dss *discoverScheduleService) Enable(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.Enable")
	defer span.End()

	schedule, err := dss.dsa.GetByID(ctx, id)
	if err != nil {
		otellog.LogError(ctx, "Failed to get discover schedule", err)
		return err
	}
	if schedule == nil {
		return fmt.Errorf("discover schedule not found")
	}

	nextRun, err := calculateScheduleNextRun(schedule.CronExpr, time.Now(), schedule.StartTime)
	if err != nil {
		return err
	}
	if err := dss.dsa.Enable(ctx, id, nextRun.UnixMilli()); err != nil {
		otellog.LogError(ctx, "Failed to enable discover schedule", err)
		return err
	}

	return nil
}

// calculateScheduleNextRun calculates the next cron trigger time that meets the future startTime limit.
// Offset forward by one nanosecond to ensure that the cron time point exactly at startTime can still be selected.
func calculateScheduleNextRun(cronExpr string, now time.Time, startTime int64) (time.Time, error) {
	cronSchedule, err := common.ParseHourlyCronExpr(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: %w", err)
	}

	from := now
	if startTime > now.UnixMilli() {
		from = time.UnixMilli(startTime).In(now.Location()).Add(-time.Nanosecond)
	}
	return cronSchedule.Next(from), nil
}

// Disable disables a discover schedule.
func (dss *discoverScheduleService) Disable(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.Disable")
	defer span.End()

	if err := dss.dsa.Disable(ctx, id); err != nil {
		otellog.LogError(ctx, "Failed to disable discover schedule", err)
		return err
	}

	return nil
}

// ExecuteSchedule is a method for executing plan discovery tasks
// It takes a context and a planned discovery task as parameters and returns an error
func (dss *discoverScheduleService) ExecuteSchedule(ctx context.Context, schedule *interfaces.DiscoverSchedule) error {
	// 使用追踪器创建一个新的span，用于监控和追踪请求的执行过程
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DiscoverScheduleService.ExecuteSchedule")
	defer span.End() // 确保在函数返回时结束span

	// Check whether the DiscoverTaskService has been set up
	if dss.dts == nil {
		otellog.LogError(ctx, "DiscoverTaskService not set", nil)
		return fmt.Errorf("DiscoverTaskService not set")
	}

	catalogInfo, err := dss.cs.InternalGetByID(ctx, schedule.CatalogID, false)
	if err != nil {
		otellog.LogError(ctx, "Failed to get catalog before executing discover schedule", err)
		return err
	}
	if catalogInfo == nil || !catalogInfo.Enabled {
		logger.Infof("Skip discover schedule for missing or disabled catalog: id=%s, catalog_id=%s", schedule.ID, schedule.CatalogID)
		return nil
	}

	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, schedule.Creator)

	// Check if there are any identical tasks currently being executed
	_, tasks, err := dss.dts.List(ctx, interfaces.DiscoverTaskQueryParams{
		CatalogID:   schedule.CatalogID,
		Statuses:    []string{interfaces.DiscoverTaskStatusRunning},
		TriggerType: interfaces.DiscoverTaskTriggerScheduled,
	})
	if err != nil {
		otellog.LogError(ctx, "Failed to check running tasks", err)
		return err
	}
	if tasks > 0 {
		logger.Warnf("There is already a running discover task for catalog %s, skipping execution", schedule.CatalogID)
		return nil
	}

	// Create a pending discover task; the local database-backed worker will pick it up.
	_, err = dss.dts.Create(ctx, &interfaces.CreateDiscoverTaskRequest{
		CatalogID:   schedule.CatalogID,
		TriggerType: interfaces.DiscoverTaskTriggerScheduled,
		ScheduleID:  schedule.ID,
		Strategy:    schedule.Strategy,
	})
	if err != nil {
		otellog.LogError(ctx, "Failed to create discover task", err)
		return err
	}

	logger.Infof("Executed discover schedule: id=%s, catalog_id=%s", schedule.ID, schedule.CatalogID)
	return nil
}
