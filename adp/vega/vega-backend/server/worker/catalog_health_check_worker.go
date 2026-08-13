// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/logger"
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

var (
	chcWorkerOnce sync.Once
	chcWorker     *CatalogHealthCheckWorker
)

// CatalogHealthCheckWorker executes due physical Catalog health checks in-process.
type CatalogHealthCheckWorker struct {
	appSetting          *common.AppSetting
	defaultCronSchedule cron.Schedule
	cs                  interfaces.CatalogService
	chcsa               interfaces.CatalogHealthCheckScheduleAccess
}

func NewCatalogHealthCheckWorker(appSetting *common.AppSetting) *CatalogHealthCheckWorker {
	chcWorkerOnce.Do(func() {
		defaultCronExpr := catalogHealthCheckDefaultCronExpr
		if appSetting.CatalogHealthCheck.CronExpr != "" {
			defaultCronExpr = appSetting.CatalogHealthCheck.CronExpr
		}
		defaultCronSchedule, err := common.ParseHourlyCronExpr(defaultCronExpr)
		if err != nil {
			logger.Fatalf("Invalid global catalog health check cron expression: %v", err)
		}
		cs := catalog.NewCatalogService(appSetting)
		chcWorker = &CatalogHealthCheckWorker{
			appSetting:          appSetting,
			defaultCronSchedule: defaultCronSchedule,
			cs:                  cs,
			chcsa:               logics.CHCSA,
		}
	})
	return chcWorker
}

func (chcw *CatalogHealthCheckWorker) Start() error {
	if !chcw.appSetting.CatalogHealthCheck.WorkerEnabled {
		logger.Info("Catalog health check worker is disabled")
		return nil
	}

	now := time.Now()
	if err := chcw.chcsa.UpdateInheritedNextRun(
		context.Background(),
		now.UnixMilli(),
		chcw.defaultCronSchedule.Next(now).UnixMilli(),
	); err != nil {
		return err
	}

	go chcw.run()
	logger.Info("Catalog health check worker started")
	return nil
}

func (chcw *CatalogHealthCheckWorker) run() {
	chcw.runDue()

	ticker := time.NewTicker(catalogHealthCheckScanInterval)
	defer ticker.Stop()

	for range ticker.C {
		chcw.runDue()
	}
}

func (chcw *CatalogHealthCheckWorker) runDue() {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run due catalog health checks panicked: %v", recovered)
		}
	}()

	ctx := context.Background()
	schedules, err := chcw.chcsa.ListDue(ctx, time.Now().UnixMilli())
	if err != nil {
		logger.Errorf("List due catalog health check schedules failed: %v", err)
		return
	}
	for _, schedule := range schedules {
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

	// 创建任务前先推进数据库中的运行时间。任务创建失败时跳过本次触发，且服务停机期间
	// 错过的历史周期不会在恢复后逐次补跑。
	nextRun := cronSchedule.Next(now)
	if err := chcw.chcsa.UpdateRunMetadata(ctx, schedule.CatalogID,
		schedule.UpdateTime, now.UnixMilli(), nextRun.UnixMilli(),
	); err != nil {
		logger.Errorf("Update catalog health check run metadata failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
		return
	}

	if _, err := chcw.cs.InternalTestConnection(ctx, schedule.CatalogID); err != nil {
		logger.Errorf("Run catalog health check failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
		return
	}

	logger.Infof("Executed catalog health check schedule: id=%s", schedule.CatalogID)
}
