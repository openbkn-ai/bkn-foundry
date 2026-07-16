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
	"time"

	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/build_task"
	"vega-backend/logics/resource"
)

var (
	taskWorkerMangerOnce sync.Once
	taskWorkerManger     *TaskWorkerManger
)

// TaskWorkerManger provides unified task processing functionality.
type TaskWorkerManger struct {
	appSetting *common.AppSetting
	aqa        interfaces.AsynqAccess

	bbw  *batchBuildWorker
	bts  interfaces.BuildTaskService
	ebw  *embeddingWorker
	dtw  *DiscoverTaskWorker
	sbw  *streamingBuildWorker
	sutw *SemanticUnderstandingTaskWorker
}

// NewTaskWorkerManager creates or returns the singleton TaskWorkerManger.
func NewTaskWorkerManager(appSetting *common.AppSetting) *TaskWorkerManger {
	taskWorkerMangerOnce.Do(func() {
		rs := resource.NewResourceService(appSetting)
		bts := build_task.NewBuildTaskService(appSetting, rs)
		taskWorkerManger = &TaskWorkerManger{
			appSetting: appSetting,
			aqa:        logics.AQA,
			bbw:        NewBatchBuildWorker(appSetting),
			bts:        bts,
			ebw:        NewEmbeddingBuildWorker(appSetting),
			dtw:        NewDiscoverTaskWorker(appSetting),
			sbw:        NewStreamingBuildWorker(appSetting),
			sutw:       NewSemanticUnderstandingTaskWorker(appSetting),
		}
	})
	return taskWorkerManger
}

// Start starts the task worker.
func (twm *TaskWorkerManger) Start() {
	if common.GetDebugMode() {
		twm.startDebugSubscribers()
		return
	}

	// Start server in a goroutine
	go func() {
		for {
			if err := twm.Run(context.Background()); err != nil {
				logger.Errorf("Task worker failed: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
	}()

	// 自愈对账：入队消息丢失（pod 更替/入队失败）的任务会永远停在 init（界面"排队中"），
	// 周期对账把它们重新入队
	go newBuildTaskReconciler(twm.bts).run()

}

func (twm *TaskWorkerManger) startDebugSubscribers() {
	go func() {
		logger.Info("debug discover task channel subscriber started")
		for task := range twm.dtw.dts.DebugTaskQueue() {
			if err := twm.ProcessTask(context.Background(), task); err != nil {
				logger.Errorf("debug discover task failed: %v", err)
			}
		}
	}()
	go func() {
		logger.Info("debug semantic understanding task channel subscriber started")
		for task := range twm.sutw.suts.DebugTaskQueue() {
			if err := twm.ProcessTask(context.Background(), task); err != nil {
				logger.Errorf("debug semantic understanding task failed: %v", err)
			}
		}
	}()
	go func() {
		logger.Info("debug build task channel subscriber started")
		for task := range twm.bts.DebugTaskQueue() {
			if err := twm.ProcessTask(context.Background(), task); err != nil {
				logger.Errorf("debug build task failed: %v", err)
			}
		}
	}()
}

// Run runs the task worker.
func (twm *TaskWorkerManger) Run(ctx context.Context) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Task worker failed: %v", err)
		}
	}()

	srv := twm.aqa.CreateServer()

	// Register task workers
	mux := asynq.NewServeMux()
	mux.Handle(interfaces.DiscoverTaskType, twm)
	mux.Handle(interfaces.SemanticUnderstandingTaskType, twm)

	mux.Handle(interfaces.BuildTaskTypeBatch, twm)
	mux.Handle(interfaces.BuildTaskTypeEmbedding, twm)
	mux.Handle(interfaces.BuildTaskTypeStreaming, twm)

	logger.Infof("Task worker starting, listening for task types: %s, %s, %s, %s, %s", interfaces.DiscoverTaskType, interfaces.SemanticUnderstandingTaskType, interfaces.BuildTaskTypeBatch, interfaces.BuildTaskTypeEmbedding, interfaces.BuildTaskTypeStreaming)
	if err := srv.Run(mux); err != nil {
		logger.Errorf("Task worker failed: %v", err)
		return err
	}
	return nil
}

// ProcessTask processes a task from the queue.
func (twm *TaskWorkerManger) ProcessTask(ctx context.Context, task *asynq.Task) error {
	switch task.Type() {
	case interfaces.DiscoverTaskType:
		return twm.dtw.HandleTask(ctx, task)
	case interfaces.SemanticUnderstandingTaskType:
		return twm.sutw.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeBatch:
		return twm.bbw.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeEmbedding:
		return twm.ebw.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeStreaming:
		return twm.sbw.HandleTask(ctx, task)
	default:
		return fmt.Errorf("unknown task type: %s", task.Type())
	}
}
