// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
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

// Start starts the task worker.
func (twm *TaskWorkerManger) Start() {
	twm.sutw.Start(context.Background())
	twm.dtw.Start(context.Background())
	twm.btw.Start(context.Background())
}
