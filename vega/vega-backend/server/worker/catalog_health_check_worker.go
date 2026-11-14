// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/robfig/cron/v3"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/catalog"
)

const (
	catalogHealthCheckDefaultCronExpr = "0 * * * *"
	catalogHealthCheckScanInterval    = time.Minute
)

// CatalogHealthCheckWorker executes due physical Catalog health checks in-process.
type CatalogHealthCheckWorker struct {
	appSetting          *common.AppSetting
	defaultCronSchedule cron.Schedule
	cs                  interfaces.CatalogService
	chcsa               interfaces.CatalogHealthCheckScheduleAccess

	wg      sync.WaitGroup
	stopCh  chan struct{}
	stopped atomic.Bool
}

func NewCatalogHealthCheckWorker(appSetting *common.AppSetting) *CatalogHealthCheckWorker {
	defaultCronExpr := catalogHealthCheckDefaultCronExpr
	if appSetting.CatalogHealthCheck.CronExpr != "" {
		defaultCronExpr = appSetting.CatalogHealthCheck.CronExpr
	}
	defaultCronSchedule, err := common.ParseHourlyCronExpr(defaultCronExpr)
	if err != nil {
		logger.Fatalf("Invalid global catalog health check cron expression: %v", err)
	}
	worker := &CatalogHealthCheckWorker{
		appSetting:          appSetting,
		defaultCronSchedule: defaultCronSchedule,
		cs:                  catalog.NewCatalogService(appSetting),
		chcsa:               logics.CHCSA,
		stopCh:              make(chan struct{}),
	}
	worker.stopped.Store(false)
	return worker
}

func (chcw *CatalogHealthCheckWorker) Start() {
	if !chcw.appSetting.CatalogHealthCheck.WorkerEnabled {
		logger.Info("Catalog health check worker is disabled")
		return
	}

	now := time.Now()
	if err := chcw.chcsa.UpdateInheritedNextRun(context.Background(),
		now.UnixMilli(), chcw.defaultCronSchedule.Next(now).UnixMilli()); err != nil {
		logger.Errorf("Initialize catalog health check schedules failed; continuing with the polling loop: %v", err)
	}

	chcw.wg.Add(1)
	go func() {
		defer chcw.wg.Done()
		chcw.run(context.Background())
	}()
	logger.Info("Catalog health check worker started")
}

func (chcw *CatalogHealthCheckWorker) Stop() {
	chcw.stopped.Store(true)
	close(chcw.stopCh)
	chcw.wg.Wait()
}

func (chcw *CatalogHealthCheckWorker) run(ctx context.Context) {
	chcw.runDue(ctx)

	ticker := time.NewTicker(catalogHealthCheckScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-chcw.stopCh:
			return
		case <-ticker.C:
			chcw.runDue(ctx)
		}
	}
}

func (chcw *CatalogHealthCheckWorker) runDue(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run due catalog health checks panicked: %v", recovered)
		}
	}()

	schedules, err := chcw.chcsa.ListDue(ctx, time.Now().UnixMilli())
	if err != nil {
		logger.Errorf("List due catalog health check schedules failed: %v", err)
		return
	}
	for _, schedule := range schedules {
		if chcw.stopped.Load() {
			return
		}
		chcw.runSchedule(ctx, schedule)
	}
}

func (chcw *CatalogHealthCheckWorker) runSchedule(ctx context.Context, schedule *interfaces.CatalogHealthCheckSchedule) {
	chcw.runScheduleAt(ctx, schedule, time.Now())
}

func (chcw *CatalogHealthCheckWorker) runScheduleAt(ctx context.Context, schedule *interfaces.CatalogHealthCheckSchedule, now time.Time) {
	catalogID := schedule.CatalogID
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run catalog health check panicked: catalog_id=%s, error=%v", catalogID, recovered)
		}
	}()

	if schedule.Mode == interfaces.CatalogHealthCheckScheduleModeDisabled || schedule.NextRun > now.UnixMilli() {
		return
	}

	var cronSchedule cron.Schedule
	if schedule.Mode == interfaces.CatalogHealthCheckScheduleModeInherit {
		cronSchedule = chcw.defaultCronSchedule
	} else {
		var err error
		cronSchedule, err = common.ParseHourlyCronExpr(schedule.CronExpr)
		if err != nil {
			message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "VegaBackend.Schedule.Result.CronInvalid", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			if _, disableErr := chcw.chcsa.DisableInvalid(ctx, catalogID, schedule.UpdateTime,
				schedule.NextRun, message); disableErr != nil {
				logger.Errorf("Disable invalid catalog health check schedule failed: catalog_id=%s, error=%v", catalogID, disableErr)
			}
			return
		}
	}

	nextRun := cronSchedule.Next(now)
	if nextRun.IsZero() {
		message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "VegaBackend.Schedule.Result.CronNoFuture", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		if _, err := chcw.chcsa.DisableInvalid(ctx, catalogID, schedule.UpdateTime,
			schedule.NextRun, message); err != nil {
			logger.Errorf("Disable unsatisfiable catalog health check schedule failed: catalog_id=%s, error=%v", catalogID, err)
		}
		return
	}
	if schedule.NextRun == 0 {
		if _, err := chcw.chcsa.InitializeNextRun(ctx, catalogID,
			schedule.UpdateTime, schedule.NextRun, nextRun.UnixMilli()); err != nil {
			logger.Errorf("Initialize catalog health check schedule failed: catalog_id=%s, error=%v", catalogID, err)
		}
		return
	}
	runID, err := uuid.NewV7()
	if err != nil {
		logger.Errorf("Generate catalog health check run ID failed: %v", err)
		return
	}
	rowsAffected, err := chcw.chcsa.ClaimRun(ctx, catalogID, schedule.UpdateTime,
		schedule.NextRun, now.UnixMilli(), nextRun.UnixMilli(), runID.String())
	if err != nil {
		logger.Errorf("Claim catalog health check schedule failed: catalog_id=%s, error=%v", catalogID, err)
		return
	}
	if rowsAffected == 0 {
		return
	}
	result := interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}
	if _, err := chcw.cs.InternalTestConnection(ctx, catalogID); err != nil {
		logger.Errorf("Run catalog health check failed: catalog_id=%s, error=%v", catalogID, err)
		status := interfaces.ScheduleStatusFailed
		messageID := "VegaBackend.Schedule.Result.HealthCheckFailed"
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.ErrorCode {
			case verrors.VegaBackend_Catalog_IsDisabled:
				status = interfaces.ScheduleStatusSkipped
				messageID = "VegaBackend.Schedule.Result.HealthCatalogDisabled"
			case verrors.VegaBackend_Catalog_NotFound:
				status = interfaces.ScheduleStatusSkipped
				messageID = "VegaBackend.Schedule.Result.HealthCatalogMissing"
			}
		}
		message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), messageID, nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		result = interfaces.ScheduleRunResult{Status: status, Message: message}
	}
	if _, err := chcw.chcsa.CompleteRun(ctx, catalogID, runID.String(), result); err != nil {
		logger.Errorf("Save catalog health check result failed: catalog_id=%s, error=%v", catalogID, err)
	}
}
