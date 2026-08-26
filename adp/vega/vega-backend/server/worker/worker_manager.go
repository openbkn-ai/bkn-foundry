// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"vega-backend/common"
	"vega-backend/logics/build_task"
	logicsDiscoverSchedule "vega-backend/logics/discover_schedule"
	logicsDiscoverTask "vega-backend/logics/discover_task"
	"vega-backend/logics/resource"
)

const taskQueueSizeMultiplier = 4

var ErrWorkerManagerStopping = errors.New("worker manager is stopping")

var (
	workerManagerOnce sync.Once
	workerManager     *WorkerManager
)

// WorkerManager owns every long-running VEGA background worker.
type WorkerManager struct {
	btw  *BuildTaskWorker
	dtw  *DiscoverTaskWorker
	sutw *SemanticUnderstandingTaskWorker
	dsw  *DiscoverScheduleWorker
	chcw *CatalogHealthCheckWorker
	icw  *IndexCleanupWorker
}

// NewWorkerManager creates or returns the singleton WorkerManager.
func NewWorkerManager(appSetting *common.AppSetting) *WorkerManager {
	workerManagerOnce.Do(func() {
		rs := resource.NewResourceService(appSetting)
		bts := build_task.NewBuildTaskService(appSetting, rs)
		btw := NewBuildTaskWorker(appSetting, bts)
		dtw := NewDiscoverTaskWorker(appSetting)
		sutw := NewSemanticUnderstandingTaskWorker(appSetting)
		dts := logicsDiscoverTask.NewDiscoverTaskService(appSetting)
		dss := logicsDiscoverSchedule.NewDiscoverScheduleService(appSetting, dts)
		dsw := NewDiscoverScheduleWorker(appSetting, dss)
		chcw := NewCatalogHealthCheckWorker(appSetting)
		icw := NewIndexCleanupWorker(appSetting)
		workerManager = &WorkerManager{
			btw:  btw,
			dtw:  dtw,
			sutw: sutw,
			dsw:  dsw,
			chcw: chcw,
			icw:  icw,
		}
	})
	return workerManager
}

// Start recovers task state before launching all task and schedule workers.
func (wm *WorkerManager) Start(ctx context.Context) error {
	if err := wm.sutw.recoverInterruptedTasks(ctx); err != nil {
		return fmt.Errorf("recover semantic understanding tasks: %w", err)
	}
	if err := wm.dtw.recoverInterruptedTasks(ctx); err != nil {
		return fmt.Errorf("recover discover tasks: %w", err)
	}
	if err := wm.btw.recoverInterruptedTasks(ctx); err != nil {
		return fmt.Errorf("recover build tasks: %w", err)
	}

	wm.sutw.Start()
	wm.dtw.Start()
	wm.btw.Start()
	wm.dsw.Start()
	wm.chcw.Start()
	wm.icw.Start()
	return nil
}

// Stop stops schedule workers followed by task workers that were already
// running, which finish naturally.
func (wm *WorkerManager) Stop() {
	wm.dsw.Stop()
	wm.chcw.Stop()
	wm.icw.Stop()
	wm.sutw.Stop()
	wm.dtw.Stop()
	wm.btw.Stop()
}
