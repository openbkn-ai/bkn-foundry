// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/robfig/cron/v3"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
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
	catalogID := schedule.CatalogID
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run catalog health check panicked: catalog_id=%s, error=%v", catalogID, recovered)
		}
	}()

	now := time.Now()
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
			logger.Errorf("Parse catalog health check cron expression failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
			return
		}
	}

	// Advance the running time in the database before creating a task. Skip this trigger when the task creation fails and during the service downtime
	// Missed historical cycles will not be made up for one by one after recovery.
	nextRun := cronSchedule.Next(now)
	rowsAffected, err := chcw.chcsa.UpdateRunMetadata(ctx, schedule.CatalogID,
		schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), nextRun.UnixMilli())
	if err != nil {
		logger.Errorf("Update catalog health check run metadata failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
		return
	}
	if rowsAffected == 0 {
		return
	}

	if _, err := chcw.cs.InternalTestConnection(ctx, schedule.CatalogID); err != nil {
		logger.Errorf("Run catalog health check failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
		return
	}

	logger.Infof("Executed catalog health check schedule: id=%s", schedule.CatalogID)
}
