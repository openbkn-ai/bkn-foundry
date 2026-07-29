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
	appSetting *common.AppSetting
	cs         interfaces.CatalogService
	chcsa      interfaces.CatalogHealthCheckScheduleAccess
}

func NewCatalogHealthCheckWorker(appSetting *common.AppSetting) *CatalogHealthCheckWorker {
	chcWorkerOnce.Do(func() {
		cs := catalog.NewCatalogService(appSetting)
		chcWorker = &CatalogHealthCheckWorker{
			appSetting: appSetting,
			cs:         cs,
			chcsa:      logics.CHCSA,
		}
	})
	return chcWorker
}

func (w *CatalogHealthCheckWorker) defaultCronExpr() string {
	if w.appSetting != nil && w.appSetting.CatalogHealthCheck.CronExpr != "" {
		return w.appSetting.CatalogHealthCheck.CronExpr
	}
	return catalogHealthCheckDefaultCronExpr
}

func (w *CatalogHealthCheckWorker) Start() error {
	if w.appSetting == nil || !w.appSetting.CatalogHealthCheck.WorkerEnabled {
		logger.Info("Catalog health check worker is disabled")
		return nil
	}
	if _, err := cron.ParseStandard(w.defaultCronExpr()); err != nil {
		return err
	}

	go w.run()
	logger.Info("Catalog health check worker started")
	return nil
}

func (w *CatalogHealthCheckWorker) run() {
	w.runDue()

	ticker := time.NewTicker(catalogHealthCheckScanInterval)
	defer ticker.Stop()

	for range ticker.C {
		w.runDue()
	}
}

func (w *CatalogHealthCheckWorker) runDue() {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run due catalog health checks panicked: %v", recovered)
		}
	}()

	ctx := context.Background()
	schedules, err := w.chcsa.ListDue(ctx, time.Now().UnixMilli())
	if err != nil {
		logger.Errorf("List due catalog health check schedules failed: %v", err)
		return
	}
	for _, schedule := range schedules {
		w.runCatalogHealthCheck(ctx, schedule)
	}
}

func (w *CatalogHealthCheckWorker) runCatalogHealthCheck(ctx context.Context, schedule *interfaces.CatalogHealthCheckSchedule) {

	if schedule.Mode == interfaces.CatalogHealthCheckScheduleModeDisabled || schedule.NextRun > time.Now().UnixMilli() {
		return
	}

	cronExpr := schedule.CronExpr
	if schedule.Mode == interfaces.CatalogHealthCheckScheduleModeInherit {
		cronExpr = w.defaultCronExpr()
	}
	cronSchedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		logger.Errorf("Parse catalog health check cron expression failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
		return
	}

	if _, err := w.cs.TestConnection(ctx, &interfaces.Catalog{ID: schedule.CatalogID, ConnectorType: "scheduled"}); err != nil {
		logger.Errorf("Run catalog health check failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
		return
	}

	lastRun := time.Now()
	if err := w.chcsa.UpdateRunMetadata(ctx, schedule.CatalogID, lastRun.UnixMilli(), cronSchedule.Next(lastRun).UnixMilli()); err != nil {
		logger.Errorf("Update catalog health check run metadata failed: catalog_id=%s, error=%v", schedule.CatalogID, err)
	}
}
