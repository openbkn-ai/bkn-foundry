// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"fmt"
	"sync"

	"vega-backend/common"
	"vega-backend/logics/build_task"
	"vega-backend/logics/resource"
)

const taskQueueSizeMultiplier = 4

var (
	taskWorkerMangerOnce sync.Once
	taskWorkerManger     *TaskWorkerManger
)

// TaskWorkerManger provides unified task processing functionality.
type TaskWorkerManger struct {
	btw  *BuildTaskWorker
	dtw  *DiscoverTaskWorker
	sutw *SemanticUnderstandingTaskWorker
}

// NewTaskWorkerManager creates or returns the singleton TaskWorkerManger.
func NewTaskWorkerManager(appSetting *common.AppSetting) *TaskWorkerManger {
	taskWorkerMangerOnce.Do(func() {
		rs := resource.NewResourceService(appSetting)
		bts := build_task.NewBuildTaskService(appSetting, rs)
		sutw := NewSemanticUnderstandingTaskWorker(appSetting)
		bbw := NewBatchBuildWorker(appSetting)
		sbw := NewStreamingBuildWorker(appSetting)
		ebw := NewEmbeddingBuildWorker(appSetting)
		taskWorkerManger = &TaskWorkerManger{
			btw:  newBuildTaskWorker(appSetting, bts, bbw, sbw, ebw),
			dtw:  NewDiscoverTaskWorker(appSetting),
			sutw: sutw,
		}
	})
	return taskWorkerManger
}

// Start recovers all interrupted tasks before starting any task loop.
func (twm *TaskWorkerManger) Start(ctx context.Context) error {
	if err := twm.sutw.recoverInterruptedTasks(ctx); err != nil {
		return fmt.Errorf("recover semantic understanding tasks: %w", err)
	}
	if err := twm.dtw.recoverInterruptedTasks(ctx); err != nil {
		return fmt.Errorf("recover discover tasks: %w", err)
	}
	if err := twm.btw.recoverInterruptedTasks(ctx); err != nil {
		return fmt.Errorf("recover build tasks: %w", err)
	}

	twm.sutw.startLoops(ctx)
	twm.dtw.startLoops(ctx)
	twm.btw.startLoops(ctx)
	return nil
}
