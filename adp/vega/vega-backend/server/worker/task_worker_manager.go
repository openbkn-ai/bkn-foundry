// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
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
)

var (
	taskWorkerOnce sync.Once
	taskWorkerMgr  *TaskWorkerManger
)

// TaskWorkerManger provides unified task processing functionality.
type TaskWorkerManger struct {
	appSetting *common.AppSetting
	aqa        interfaces.AsynqAccess

	discoverTaskWorker *DiscoverTaskWorker
	sutWorker          *SemanticUnderstandingTaskWorker
	btBuildWorker      *batchBuildWorker
	stBuildWorker      *streamingBuildWorker
	embeddingWorker    *embeddingWorker
}

// NewTaskWorkerManager creates or returns the singleton TaskWorkerManger.
func NewTaskWorkerManager(appSetting *common.AppSetting) *TaskWorkerManger {
	taskWorkerOnce.Do(func() {
		taskWorkerMgr = &TaskWorkerManger{
			appSetting:         appSetting,
			aqa:                logics.AQA,
			discoverTaskWorker: NewDiscoverTaskWorker(appSetting),
			sutWorker:          NewSemanticUnderstandingTaskWorker(appSetting),
			btBuildWorker:      NewBatchBuildWorker(appSetting),
			stBuildWorker:      NewStreamingBuildWorker(appSetting),
			embeddingWorker:    NewEmbeddingBuildWorker(appSetting),
		}
	})
	return taskWorkerMgr
}

// Start starts the task worker.
func (twm *TaskWorkerManger) Start() {
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
	go newBuildTaskReconciler().run()

	if common.GetDebugMode() {
		go func() {
			logger.Info("debug task channel subscriber started")
			for task := range twm.discoverTaskWorker.dts.DebugTaskQueue() {
				if err := twm.ProcessTask(context.Background(), task); err != nil {
					logger.Errorf("debug task failed: %v", err)
				}
			}
		}()
		go func() {
			logger.Info("debug semantic understanding task channel subscriber started")
			for task := range twm.sutWorker.suts.DebugTaskQueue() {
				if err := twm.ProcessTask(context.Background(), task); err != nil {
					logger.Errorf("debug semantic understanding task failed: %v", err)
				}
			}
		}()
	}
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
		return twm.discoverTaskWorker.HandleTask(ctx, task)
	case interfaces.SemanticUnderstandingTaskType:
		return twm.sutWorker.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeBatch:
		return twm.btBuildWorker.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeEmbedding:
		return twm.embeddingWorker.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeStreaming:
		return twm.stBuildWorker.HandleTask(ctx, task)
	default:
		return fmt.Errorf("unknown task type: %s", task.Type())
	}
}
