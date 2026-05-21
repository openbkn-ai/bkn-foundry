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
	"github.com/kweaver-ai/kweaver-go-lib/logger"

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
	appSetting       *common.AppSetting
	aqa              interfaces.AsynqAccess
	discoverHandler  *DiscoverHandler
	btBuildHandler   *batchBuildHandler
	stBuildHandler   *streamingBuildHandler
	embeddingHandler *embeddingHandler
}

// NewTaskWorkerManager creates or returns the singleton TaskWorkerManger.
func NewTaskWorkerManager(appSetting *common.AppSetting) *TaskWorkerManger {
	taskWorkerOnce.Do(func() {
		taskWorkerMgr = &TaskWorkerManger{
			appSetting:       appSetting,
			aqa:              logics.AQA,
			discoverHandler:  NewDiscoverHandler(appSetting),
			btBuildHandler:   NewBatchBuildHandler(appSetting),
			stBuildHandler:   NewStreamingBuildHandler(appSetting),
			embeddingHandler: NewEmbeddingBuildHandler(appSetting),
		}
	})
	return taskWorkerMgr
}

// Start starts the task worker.
func (tw *TaskWorkerManger) Start() {
	// Start server in a goroutine
	go func() {
		for {
			if err := tw.Run(context.Background()); err != nil {
				logger.Errorf("Task worker failed: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
	}()

	if common.GetDebugMode() {
		go func() {
			logger.Info("debug task channel subscriber started")
			for task := range tw.discoverHandler.dts.DebugTaskQueue() {
				if err := tw.ProcessTask(context.Background(), task); err != nil {
					logger.Errorf("debug task failed: %v", err)
				}
			}
		}()
	}
}

// Run runs the task worker.
func (tw *TaskWorkerManger) Run(ctx context.Context) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Task worker failed: %v", err)
		}
	}()

	srv := tw.aqa.CreateServer()

	// Register task handlers
	mux := asynq.NewServeMux()
	mux.Handle(interfaces.DiscoverTaskType, tw)
	mux.Handle(interfaces.BuildTaskTypeBatch, tw)
	mux.Handle(interfaces.BuildTaskTypeEmbedding, tw)
	mux.Handle(interfaces.BuildTaskTypeStreaming, tw)

	logger.Infof("Task worker starting, listening for task types: %s, %s, %s, %s", interfaces.DiscoverTaskType, interfaces.BuildTaskTypeBatch, interfaces.BuildTaskTypeEmbedding, interfaces.BuildTaskTypeStreaming)
	if err := srv.Run(mux); err != nil {
		logger.Errorf("Task worker failed: %v", err)
		return err
	}
	return nil
}

// ProcessTask processes a task from the queue.
func (tw *TaskWorkerManger) ProcessTask(ctx context.Context, task *asynq.Task) error {
	switch task.Type() {
	case interfaces.DiscoverTaskType:
		return tw.discoverHandler.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeBatch:
		return tw.btBuildHandler.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeEmbedding:
		return tw.embeddingHandler.HandleTask(ctx, task)
	case interfaces.BuildTaskTypeStreaming:
		return tw.stBuildHandler.HandleTask(ctx, task)
	default:
		return fmt.Errorf("unknown task type: %s", task.Type())
	}
}
