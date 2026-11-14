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

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
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
	dsw.runScheduleAt(ctx, schedule, time.Now())
}

func (dsw *DiscoverScheduleWorker) runScheduleAt(ctx context.Context, schedule *interfaces.DiscoverSchedule, now time.Time) {
	catalogID := schedule.CatalogID
	defer func() {
		if recovered := recover(); recovered != nil {
			// 触发中断保留 triggering，不伪造已知结果或重试本次。
			logger.Errorf("Run discover schedule panicked: catalog_id=%s, schedule_id=%s, error=%v",
				catalogID, schedule.ID, recovered)
		}
	}()

	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, schedule.Creator)
	if !schedule.Enabled || schedule.NextRun > now.UnixMilli() {
		return
	}

	if schedule.EndTime > 0 && now.UnixMilli() > schedule.EndTime {
		if err := dsw.dss.InternalUpdateEnabled(ctx, schedule, false); err != nil {
			logger.Errorf("Disable expired discover schedule failed: schedule_id=%s, error=%v", schedule.ID, err)
		}
		return
	}

	cronSchedule, err := common.ParseHourlyCronExpr(schedule.CronExpr)
	if err != nil {
		if _, disableErr := dsw.dsa.DisableInvalid(ctx, schedule.ID, schedule.UpdateTime,
			schedule.NextRun, "调度表达式无效或执行间隔不足一小时，请修正后启用"); disableErr != nil {
			logger.Errorf("Disable invalid discover schedule failed: schedule_id=%s, error=%v", schedule.ID, disableErr)
		}
		return
	}

	from := now
	if schedule.StartTime > now.UnixMilli() {
		from = time.UnixMilli(schedule.StartTime).In(now.Location()).Add(-time.Nanosecond)
	}
	nextRun := cronSchedule.Next(from)
	if nextRun.IsZero() {
		if _, disableErr := dsw.dsa.DisableInvalid(ctx, schedule.ID, schedule.UpdateTime,
			schedule.NextRun, "调度表达式没有有效的未来触发时间，请修正后启用"); disableErr != nil {
			logger.Errorf("Disable unsatisfiable discover schedule failed: schedule_id=%s, error=%v", schedule.ID, disableErr)
		}
		return
	}
	if schedule.NextRun == 0 || schedule.StartTime > now.UnixMilli() {
		if _, err := dsw.dsa.InitializeNextRun(ctx, schedule.ID,
			schedule.UpdateTime, schedule.NextRun, nextRun.UnixMilli()); err != nil {
			logger.Errorf("Initialize discover schedule next run failed: schedule_id=%s, error=%v", schedule.ID, err)
		}
		return
	}
	runID, err := uuid.NewV7()
	if err != nil {
		logger.Errorf("Generate discover schedule run ID failed: %v", err)
		return
	}

	rowsAffected, err := dsw.dsa.ClaimRun(ctx, schedule.ID, schedule.UpdateTime,
		schedule.NextRun, now.UnixMilli(), nextRun.UnixMilli(), runID.String())
	if err != nil {
		logger.Errorf("Claim discover schedule failed: schedule_id=%s, error=%v", schedule.ID, err)
		return
	}
	if rowsAffected == 0 {
		return
	}

	result, err := dsw.dss.ExecuteSchedule(ctx, schedule)
	if err != nil {
		logger.Errorf("Execute discover schedule failed: schedule_id=%s, error=%v", schedule.ID, err)
		if result.Status == "" {
			result = interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed,
				Message: "无法创建探查任务，请检查目录配置与权限"}
		}
	}
	if _, err := dsw.dsa.CompleteRun(ctx, schedule.ID, runID.String(), result); err != nil {
		logger.Errorf("Save discover schedule result failed: schedule_id=%s, error=%v", schedule.ID, err)
	}
}
