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

	"github.com/openbkn-ai/bkn-comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
)

const (
	buildTaskPollInterval            = 30 * time.Second
	defaultBatchBuildWorkerCount     = 1
	defaultStreamingBuildWorkerCount = 1
)

// BuildTaskWorker schedules persisted build tasks and dispatches their mode-specific execution.
type BuildTaskWorker struct {
	bts interfaces.BuildTaskService
	bbw *batchBuildWorker
	sbw *streamingBuildWorker
	ebw *embeddingWorker

	batchWorkerCount     int
	streamingWorkerCount int
	batchQueue           chan string
	streamingQueue       chan string
	embeddingQueue       chan string
	mu                   sync.Mutex
	inFlight             map[string]struct{}
}

func newBuildTaskWorker(appSetting *common.AppSetting, bts interfaces.BuildTaskService,
	bbw *batchBuildWorker, sbw *streamingBuildWorker, ebw *embeddingWorker) *BuildTaskWorker {
	batchWorkerCount := defaultBatchBuildWorkerCount
	streamingWorkerCount := defaultStreamingBuildWorkerCount
	if appSetting != nil {
		if appSetting.TaskWorker.BatchWorkerCount > 0 {
			batchWorkerCount = appSetting.TaskWorker.BatchWorkerCount
		}
		if appSetting.TaskWorker.StreamingWorkerCount > 0 {
			streamingWorkerCount = appSetting.TaskWorker.StreamingWorkerCount
		}
	}
	embeddingWorkerCount := batchWorkerCount + streamingWorkerCount
	worker := &BuildTaskWorker{
		bts: bts,
		bbw: bbw,
		sbw: sbw,
		ebw: ebw,

		batchWorkerCount:     batchWorkerCount,
		streamingWorkerCount: streamingWorkerCount,
		batchQueue:           make(chan string, batchWorkerCount*taskQueueSizeMultiplier),
		streamingQueue:       make(chan string, streamingWorkerCount*taskQueueSizeMultiplier),
		embeddingQueue:       make(chan string, embeddingWorkerCount),
		inFlight:             make(map[string]struct{}),
	}
	bbw.embeddingQueue = worker.embeddingQueue
	sbw.embeddingQueue = worker.embeddingQueue
	return worker
}

// startLoops starts the local worker pools and database producer after startup recovery succeeds.
func (btw *BuildTaskWorker) startLoops(ctx context.Context) {
	for i := 0; i < btw.batchWorkerCount; i++ {
		go btw.runBatchTasks(ctx)
	}
	for i := 0; i < btw.streamingWorkerCount; i++ {
		go btw.runStreamingTasks(ctx)
	}
	for i := 0; i < btw.batchWorkerCount+btw.streamingWorkerCount; i++ {
		go btw.runEmbeddingTasks(ctx)
	}
	go btw.pollTasks(ctx)
}

func (btw *BuildTaskWorker) recoverInterruptedTasks(ctx context.Context) error {
	pageSize := cap(btw.batchQueue) + cap(btw.streamingQueue)
	for {
		tasks, _, err := btw.bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{
				Limit:     pageSize,
				Sort:      interfaces.BuildTaskSortCreateTime,
				Direction: interfaces.ASC_DIRECTION,
			},
			Statuses: []string{
				interfaces.BuildTaskStatusRunning,
				interfaces.BuildTaskStatusStopping,
			},
		})
		if err != nil {
			return fmt.Errorf("list interrupted build tasks: %w", err)
		}
		if len(tasks) == 0 {
			return nil
		}
		for _, task := range tasks {
			if task == nil {
				return fmt.Errorf("list interrupted build tasks returned a nil task")
			}
			update := interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusStopped)
			if task.Status == interfaces.BuildTaskStatusRunning {
				update = interfaces.NewBuildTaskUpdate().
					WithStatus(interfaces.BuildTaskStatusFailed).
					WithErrorMsg("build task interrupted by service restart")
			}
			changed, err := btw.bts.InternalUpdateStatus(ctx, nil, task.ID, update, task.Status)
			if err != nil {
				return fmt.Errorf("recover interrupted build task %s: %w", task.ID, err)
			}
			if !changed {
				return fmt.Errorf("interrupted build task %s was not recovered", task.ID)
			}
		}
	}
}

func (btw *BuildTaskWorker) pollTasks(ctx context.Context) {
	btw.fillBatchQueue(ctx)
	btw.fillStreamingQueue(ctx)
	ticker := time.NewTicker(buildTaskPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-btw.bts.DispatchSignal():
		}
		btw.fillBatchQueue(ctx)
		btw.fillStreamingQueue(ctx)
	}
}

func (btw *BuildTaskWorker) fillBatchQueue(ctx context.Context) {
	if len(btw.batchQueue) != 0 {
		return
	}
	tasks, _, err := btw.bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{
			Limit:     cap(btw.batchQueue),
			Sort:      interfaces.BuildTaskSortCreateTime,
			Direction: interfaces.ASC_DIRECTION,
		},
		Statuses: []string{interfaces.BuildTaskStatusPending},
		Mode:     interfaces.BuildTaskModeBatch,
	})
	if err != nil {
		logger.Errorf("List pending batch build tasks failed: %v", err)
		return
	}
	for _, task := range tasks {
		if task == nil || !btw.addInFlight(task.ID) {
			continue
		}
		select {
		case btw.batchQueue <- task.ID:
		case <-ctx.Done():
			btw.removeInFlight(task.ID)
			return
		}
	}
}

func (btw *BuildTaskWorker) fillStreamingQueue(ctx context.Context) {
	if len(btw.streamingQueue) != 0 {
		return
	}
	tasks, _, err := btw.bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{
			Limit:     cap(btw.streamingQueue),
			Sort:      interfaces.BuildTaskSortCreateTime,
			Direction: interfaces.ASC_DIRECTION,
		},
		Statuses: []string{interfaces.BuildTaskStatusPending},
		Mode:     interfaces.BuildTaskModeStreaming,
	})
	if err != nil {
		logger.Errorf("List pending streaming build tasks failed: %v", err)
		return
	}
	for _, task := range tasks {
		if task == nil || !btw.addInFlight(task.ID) {
			continue
		}
		select {
		case btw.streamingQueue <- task.ID:
		case <-ctx.Done():
			btw.removeInFlight(task.ID)
			return
		}
	}
}

func (btw *BuildTaskWorker) runBatchTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case taskID := <-btw.batchQueue:
			btw.runBatchSafely(ctx, taskID)
			btw.removeInFlight(taskID)
			btw.bts.RequestDispatch()
		}
	}
}

func (btw *BuildTaskWorker) runStreamingTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case taskID := <-btw.streamingQueue:
			btw.runStreamingSafely(ctx, taskID)
			btw.removeInFlight(taskID)
			btw.bts.RequestDispatch()
		}
	}
}

func (btw *BuildTaskWorker) runBatchSafely(ctx context.Context, taskID string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run batch build task panicked: id=%s, error=%v", taskID, recovered)
			btw.failTask(ctx, taskID, fmt.Sprintf("batch build task panicked: %v", recovered))
		}
	}()
	if err := btw.runBatchTask(ctx, taskID); err != nil {
		logger.Errorf("Run batch build task failed: id=%s, error=%v", taskID, err)
	}
}

func (btw *BuildTaskWorker) runStreamingSafely(ctx context.Context, taskID string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run streaming build task panicked: id=%s, error=%v", taskID, recovered)
			btw.failTask(ctx, taskID, fmt.Sprintf("streaming build task panicked: %v", recovered))
		}
	}()
	if err := btw.runStreamingTask(ctx, taskID); err != nil {
		logger.Errorf("Run streaming build task failed: id=%s, error=%v", taskID, err)
	}
}

func (btw *BuildTaskWorker) runEmbeddingTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case taskID := <-btw.embeddingQueue:
			btw.runEmbeddingSafely(ctx, taskID)
		}
	}
}

func (btw *BuildTaskWorker) runEmbeddingSafely(ctx context.Context, taskID string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("Run build embedding panicked: id=%s, error=%v", taskID, recovered)
			btw.failTask(ctx, taskID, fmt.Sprintf("embedding panicked: %v", recovered))
		}
	}()
	if err := btw.ebw.Run(ctx, taskID); err != nil {
		logger.Errorf("Run build embedding failed: id=%s, error=%v", taskID, err)
	}
}

func (btw *BuildTaskWorker) runBatchTask(ctx context.Context, taskID string) error {
	task, err := btw.bts.InternalGetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil || task.Status != interfaces.BuildTaskStatusPending {
		return nil
	}
	if task.Mode != interfaces.BuildTaskModeBatch {
		err = fmt.Errorf("batch build task has mode %q", task.Mode)
		btw.failTask(ctx, taskID, err.Error())
		return err
	}
	if err := btw.bbw.Run(ctx, task); err != nil {
		btw.failTask(ctx, taskID, err.Error())
		return err
	}
	if !buildTaskHasEmbedding(task) {
		return nil
	}
	return btw.waitForTerminalStatus(ctx, taskID)
}

func (btw *BuildTaskWorker) runStreamingTask(ctx context.Context, taskID string) error {
	task, err := btw.bts.InternalGetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil || task.Status != interfaces.BuildTaskStatusPending {
		return nil
	}
	if task.Mode != interfaces.BuildTaskModeStreaming {
		err = fmt.Errorf("streaming build task has mode %q", task.Mode)
		btw.failTask(ctx, taskID, err.Error())
		return err
	}
	if err := btw.sbw.Run(ctx, task); err != nil {
		btw.failTask(ctx, taskID, err.Error())
		return err
	}
	if !buildTaskHasEmbedding(task) {
		return nil
	}
	return btw.waitForTerminalStatus(ctx, taskID)
}

func (btw *BuildTaskWorker) waitForTerminalStatus(ctx context.Context, taskID string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status, err := btw.bts.InternalGetStatus(ctx, taskID)
		if err != nil {
			logger.Errorf("Get running build task status failed: id=%s, error=%v", taskID, err)
		} else if isBuildTaskTerminal(status) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (btw *BuildTaskWorker) failTask(ctx context.Context, taskID, detail string) {
	update := interfaces.NewBuildTaskUpdate().
		WithStatus(interfaces.BuildTaskStatusFailed).
		WithErrorMsg(detail)
	if _, err := btw.bts.InternalUpdateStatus(ctx, nil, taskID, update,
		interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusRunning); err != nil {
		logger.Errorf("Mark build task failed: id=%s, error=%v", taskID, err)
	}
}

func (btw *BuildTaskWorker) addInFlight(id string) bool {
	btw.mu.Lock()
	defer btw.mu.Unlock()
	if _, exists := btw.inFlight[id]; exists {
		return false
	}
	btw.inFlight[id] = struct{}{}
	return true
}

func (btw *BuildTaskWorker) removeInFlight(id string) {
	btw.mu.Lock()
	defer btw.mu.Unlock()
	delete(btw.inFlight, id)
}
