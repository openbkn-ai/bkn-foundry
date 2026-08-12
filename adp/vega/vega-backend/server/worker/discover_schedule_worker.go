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

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
)

const discoverScheduleScanInterval = time.Minute

var (
	dsWorkerOnce sync.Once
	dsWorker     *DiscoverScheduleWorker
)

// DiscoverScheduleWorker 轮询数据库中的 next_run 并执行到期的 Discover Schedule。
// 数据库是调度状态的唯一事实源。
type DiscoverScheduleWorker struct {
	appSetting *common.AppSetting
	dsa        interfaces.DiscoverScheduleAccess
	dss        interfaces.DiscoverScheduleService
}

// NewDiscoverScheduleWorker 创建或返回 Discover Schedule worker 单例。
func NewDiscoverScheduleWorker(appSetting *common.AppSetting, dss interfaces.DiscoverScheduleService) *DiscoverScheduleWorker {
	dsWorkerOnce.Do(func() {
		dsWorker = &DiscoverScheduleWorker{
			appSetting: appSetting,
			dsa:        logics.DSA,
			dss:        dss,
		}
	})
	return dsWorker
}

// Start 启动轮询循环。
func (dsw *DiscoverScheduleWorker) Start() error {
	go dsw.run()
	logger.Info("Discover schedule worker started")
	return nil
}

func (dsw *DiscoverScheduleWorker) run() {
	dsw.runDue()

	ticker := time.NewTicker(discoverScheduleScanInterval)
	defer ticker.Stop()

	for range ticker.C {
		dsw.runDue()
	}
}

func (dsw *DiscoverScheduleWorker) runDue() {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run due discover schedules panicked: %v", recovered)
		}
	}()

	ctx := context.Background()
	schedules, err := dsw.dsa.ListDue(ctx, time.Now().UnixMilli())
	if err != nil {
		logger.Errorf("List due discover schedules failed: %v", err)
		return
	}
	for _, schedule := range schedules {
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

	now := time.Now()
	if !schedule.Enabled || schedule.NextRun > now.UnixMilli() {
		return
	}

	if schedule.StartTime > 0 && now.UnixMilli() < schedule.StartTime {
		return
	}
	if schedule.EndTime > 0 && now.UnixMilli() > schedule.EndTime {
		if err := dsw.dss.Disable(ctx, schedule.ID); err != nil {
			logger.Errorf("Disable expired discover schedule failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		}
		return
	}

	cronSchedule, err := common.ParseHourlyCronExpr(schedule.CronExpr)
	if err != nil {
		logger.Errorf("Parse discover schedule cron expression failed; disabling schedule: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		if disableErr := dsw.dss.Disable(ctx, schedule.ID); disableErr != nil {
			logger.Errorf("Disable discover schedule with invalid cron expression failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, disableErr)
		}
		return
	}

	// 创建任务前先推进数据库中的运行时间。任务创建失败时跳过本次触发，且服务停机期间
	// 错过的历史周期不会在恢复后逐次补跑。
	nextRun := cronSchedule.Next(now)
	if err := dsw.dss.UpdateRunMetadata(ctx, schedule.ID,
		schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), nextRun.UnixMilli(),
	); err != nil {
		logger.Errorf("Advance discover schedule failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		return
	}

	if err := dsw.dss.ExecuteSchedule(ctx, schedule); err != nil {
		logger.Errorf("Execute discover schedule failed: schedule_id=%s, catalog_id=%s, error=%v", schedule.ID, catalogID, err)
		return
	}

	logger.Infof("Executed discover schedule: id=%s", schedule.ID)
}
