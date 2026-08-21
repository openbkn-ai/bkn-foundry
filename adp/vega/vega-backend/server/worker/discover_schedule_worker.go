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

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
)

const discoverScheduleScanInterval = time.Minute

// The DiscoverScheduleWorker polls the next_run in the database and executes the expired DiscoverSchedule.
// The database is the sole source of facts for the scheduling status.
type DiscoverScheduleWorker struct {
	appSetting *common.AppSetting
	dsa        interfaces.DiscoverScheduleAccess
	dss        interfaces.DiscoverScheduleService

	wg      sync.WaitGroup
	stopCh  chan struct{}
	stopped atomic.Bool
}

// NewDiscoverScheduleWorker creates a Discover schedule worker.
func NewDiscoverScheduleWorker(appSetting *common.AppSetting, dss interfaces.DiscoverScheduleService) *DiscoverScheduleWorker {
	worker := &DiscoverScheduleWorker{
		appSetting: appSetting,
		dsa:        logics.DSA,
		dss:        dss,
		stopCh:     make(chan struct{}),
	}
	worker.stopped.Store(false)
	return worker
}

// Start the polling loop.
func (dsw *DiscoverScheduleWorker) Start() {
	dsw.wg.Add(1)
	go func() {
		defer dsw.wg.Done()
		dsw.run(context.Background())
	}()
	logger.Info("Discover schedule worker started")
}

func (dsw *DiscoverScheduleWorker) Stop() {
	dsw.stopped.Store(true)
	close(dsw.stopCh)
	dsw.wg.Wait()
}

func (dsw *DiscoverScheduleWorker) run(ctx context.Context) {
	dsw.runDue(ctx)

	ticker := time.NewTicker(discoverScheduleScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-dsw.stopCh:
			return
		case <-ticker.C:
			dsw.runDue(ctx)
		}
	}
}

func (dsw *DiscoverScheduleWorker) runDue(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run due discover schedules panicked: %v", recovered)
		}
	}()

	schedules, err := dsw.dsa.ListDue(ctx, time.Now().UnixMilli())
	if err != nil {
		logger.Errorf("List due discover schedules failed: %v", err)
		return
	}
	for _, schedule := range schedules {
		if dsw.stopped.Load() {
			return
		}
		dsw.runSchedule(ctx, schedule)
	}
}

func (dsw *DiscoverScheduleWorker) runSchedule(ctx context.Context, schedule *interfaces.DiscoverSchedule) {
	catalogID := schedule.CatalogID
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run discover schedule panicked: catalog_id=%s, schedule_id=%s, error=%v", catalogID, schedule.ID, recovered)
		}
	}()
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, schedule.Creator)

	now := time.Now()
	if !schedule.Enabled || schedule.NextRun > now.UnixMilli() {
		return
	}

	if schedule.EndTime > 0 && now.UnixMilli() > schedule.EndTime {
		if err := dsw.dss.UpdateEnabled(ctx, schedule, false); err != nil {
			logger.Errorf("Disable expired discover schedule failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		}
		return
	}

	cronSchedule, err := common.ParseHourlyCronExpr(schedule.CronExpr)
	if err != nil {
		logger.Errorf("Parse discover schedule cron expression failed; disabling schedule: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		if disableErr := dsw.dss.UpdateEnabled(ctx, schedule, false); disableErr != nil {
			logger.Errorf("Disable discover schedule with invalid cron expression failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, disableErr)
		}
		return
	}

	// The memory cron before the upgrade does not maintain next_run. The first scan only initializes the time for the next run.
	// Avoid concentrating the execution of all existing plans in the first tick after the upgrade.
	if schedule.NextRun == 0 || schedule.StartTime > now.UnixMilli() {
		from := now
		if schedule.StartTime > now.UnixMilli() {
			from = time.UnixMilli(schedule.StartTime).In(now.Location()).Add(-time.Nanosecond)
		}
		nextRun := cronSchedule.Next(from)
		if _, err := dsw.dss.UpdateRunMetadata(ctx, schedule.ID,
			schedule.UpdateTime, schedule.NextRun, schedule.LastRun, nextRun.UnixMilli()); err != nil {
			logger.Errorf("Initialize discover schedule next run failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		}
		return
	}

	// Advance the running time in the database before creating a task. Skip this trigger when the task creation fails and during the service downtime
	// Missed historical cycles will not be made up for one by one after recovery.
	nextRun := cronSchedule.Next(now)
	rowsAffected, err := dsw.dss.UpdateRunMetadata(ctx, schedule.ID,
		schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), nextRun.UnixMilli(),
	)
	if err != nil {
		logger.Errorf("Advance discover schedule failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		return
	}
	if rowsAffected == 0 {
		return
	}

	if err := dsw.dss.ExecuteSchedule(ctx, schedule); err != nil {
		logger.Errorf("Execute discover schedule failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		return
	}

	logger.Infof("Executed discover schedule: id=%s", schedule.ID)
}
