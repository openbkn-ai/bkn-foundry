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
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

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

	batchWorkerCount     int
	streamingWorkerCount int
	batchQueue           chan string
	streamingQueue       chan string
	mu                   sync.Mutex
	inFlight             map[string]struct{}

	wg      sync.WaitGroup
	stopCh  chan struct{}
	stopped atomic.Bool
}

func NewBuildTaskWorker(appSetting *common.AppSetting, bts interfaces.BuildTaskService) *BuildTaskWorker {
	batchWorkerCount, streamingWorkerCount := calculateTaskWorkerCounts(appSetting)

	bbw := NewBatchBuildWorker(appSetting)
	sbw := NewStreamingBuildWorker(appSetting)
	worker := &BuildTaskWorker{
		bts: bts,
		bbw: bbw,
		sbw: sbw,

		batchWorkerCount:     batchWorkerCount,
		streamingWorkerCount: streamingWorkerCount,
		batchQueue:           make(chan string, batchWorkerCount*taskQueueSizeMultiplier),
		streamingQueue:       make(chan string, streamingWorkerCount*taskQueueSizeMultiplier),
		inFlight:             make(map[string]struct{}),
		stopCh:               make(chan struct{}),
	}
	worker.stopped.Store(false)
	bbw.stopped = &worker.stopped
	sbw.stopped = &worker.stopped
	return worker
}

func calculateTaskWorkerCounts(appSetting *common.AppSetting) (int, int) {
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
	return batchWorkerCount, streamingWorkerCount
}

// Start starts the local worker pools and database producer after startup recovery succeeds.
func (btw *BuildTaskWorker) Start() {
	btw.wg.Add(btw.batchWorkerCount + btw.streamingWorkerCount + 1)
	for i := 0; i < btw.batchWorkerCount; i++ {
		go func() {
			defer btw.wg.Done()
			btw.runBatchTasks(context.Background())
		}()
	}
	for i := 0; i < btw.streamingWorkerCount; i++ {
		go func() {
			defer btw.wg.Done()
			btw.runStreamingTasks(context.Background())
		}()
	}
	go func() {
		defer btw.wg.Done()
		btw.pollTasks(context.Background())
	}()
}

func (btw *BuildTaskWorker) Stop() {
	btw.stopped.Store(true)
	close(btw.stopCh)
	btw.wg.Wait()
}

func (btw *BuildTaskWorker) recoverInterruptedTasks(ctx context.Context) error {
	pageSize := cap(btw.batchQueue) + cap(btw.streamingQueue)
	for {
		tasks, err := btw.bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
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
			var changed bool
			if task.Status == interfaces.BuildTaskStatusRunning {
				changed, err = btw.bts.InternalMarkFailed(ctx, nil, task.ID,
					"build task interrupted by service restart")
			} else {
				changed, err = btw.bts.InternalMarkStopped(ctx, task.ID)
			}
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
		case <-btw.stopCh:
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
	tasks, err := btw.bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
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
		if btw.stopped.Load() {
			return
		}
		if task == nil || !btw.addInFlight(task.ID) {
			continue
		}
		select {
		case btw.batchQueue <- task.ID:
		case <-btw.stopCh:
			btw.removeInFlight(task.ID)
			return
		}
	}
}

func (btw *BuildTaskWorker) fillStreamingQueue(ctx context.Context) {
	if len(btw.streamingQueue) != 0 {
		return
	}
	tasks, err := btw.bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
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
		if btw.stopped.Load() {
			return
		}
		if task == nil || !btw.addInFlight(task.ID) {
			continue
		}
		select {
		case btw.streamingQueue <- task.ID:
		case <-btw.stopCh:
			btw.removeInFlight(task.ID)
			return
		}
	}
}

func (btw *BuildTaskWorker) runBatchTasks(ctx context.Context) {
	for {
		select {
		case <-btw.stopCh:
			return
		case taskID := <-btw.batchQueue:
			if btw.stopped.Load() {
				return
			}
			btw.runBatchSafely(ctx, taskID)
			btw.removeInFlight(taskID)
			btw.bts.RequestDispatch()
		}
	}
}

func (btw *BuildTaskWorker) runStreamingTasks(ctx context.Context) {
	for {
		select {
		case <-btw.stopCh:
			return
		case taskID := <-btw.streamingQueue:
			if btw.stopped.Load() {
				return
			}
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
	// Asynchronous tasks have no original request context. Resolve execution
	// dependencies using the task creator's permissions.
	taskCtx := context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, task.Creator)
	resource, err := btw.bbw.rs.InternalGetByID(taskCtx, nil, task.ResourceID)
	if err != nil {
		return fmt.Errorf("get resource before claiming batch build task: %w", err)
	}
	if resource == nil {
		if err := cancelBuildTaskForDeletedParent(taskCtx, btw.bts, taskID, "resource deleted"); err != nil {
			return fmt.Errorf("cancel batch build task with deleted resource: %w", err)
		}
		return nil
	}
	if err := validateBuildTaskResourceFingerprint(resource, task); err != nil {
		btw.failTask(taskCtx, taskID, err.Error())
		return err
	}
	catalog, err := btw.bbw.cs.InternalGetByID(taskCtx, resource.CatalogID, true)
	if err != nil {
		if isNotFoundError(err) {
			if updateErr := cancelBuildTaskForDeletedParent(taskCtx, btw.bts, taskID, "catalog deleted"); updateErr != nil {
				return fmt.Errorf("cancel batch build task with deleted catalog: %w", updateErr)
			}
			return nil
		}
		err = fmt.Errorf("get catalog before claiming batch build task: %w", err)
		btw.failTask(taskCtx, taskID, err.Error())
		return err
	}
	if catalog == nil {
		if err := cancelBuildTaskForDeletedParent(taskCtx, btw.bts, taskID, "catalog deleted"); err != nil {
			return fmt.Errorf("cancel batch build task with deleted catalog: %w", err)
		}
		return nil
	}
	if !catalog.Enabled {
		err = fmt.Errorf("catalog is disabled")
		btw.failTask(taskCtx, taskID, err.Error())
		return err
	}
	claimed, err := btw.bts.InternalMarkRunning(taskCtx, nil, taskID)
	if err != nil {
		return fmt.Errorf("claim batch build task execution: %w", err)
	}
	if !claimed {
		logger.Infof("Batch build task was not claimed for running: id=%s", taskID)
		return nil
	}
	if err := btw.bbw.Run(taskCtx, task, resource, catalog); err != nil {
		btw.failTask(taskCtx, taskID, err.Error())
		return err
	}
	return nil
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
	claimed, err := btw.bts.InternalMarkRunning(ctx, nil, taskID)
	if err != nil {
		return fmt.Errorf("claim streaming build task execution: %w", err)
	}
	if !claimed {
		logger.Infof("Streaming build task was not claimed for running: id=%s", taskID)
		return nil
	}
	if err := btw.sbw.Run(ctx, task); err != nil {
		btw.failTask(ctx, taskID, err.Error())
		return err
	}
	return nil
}

func (btw *BuildTaskWorker) failTask(ctx context.Context, taskID, detail string) {
	if _, err := btw.bts.InternalMarkFailed(ctx, nil, taskID, detail); err != nil {
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
