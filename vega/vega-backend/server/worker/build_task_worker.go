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
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/sync_checkpoint"
)

const (
	buildTaskPollInterval            = 30 * time.Second
	defaultBatchBuildWorkerCount     = 1
	defaultStreamingBuildWorkerCount = 1
)

var errIncrementalBatchResourceInvalid = errors.New("incremental batch resource is invalid")

var runningOrStoppingStatuses = []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping}

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
	batchResourceByTask  map[string]string

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
					"build task interrupted by service restart", nil)
			} else {
				changed, err = btw.bts.InternalMarkStopped(ctx, nil, task.ID, nil)
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
		Statuses:           []string{interfaces.BuildTaskStatusPending},
		Mode:               interfaces.BuildTaskModeBatch,
		ExcludeResourceIDs: btw.reservedBatchResources(),
	})
	if err != nil {
		logger.Errorf("List pending batch build tasks failed: %v", err)
		return
	}
	for _, task := range tasks {
		if btw.stopped.Load() {
			return
		}
		if task == nil || !btw.reserveBatchTask(task) {
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
			if err := btw.runBatchTask(ctx, taskID); err != nil {
				logger.Errorf("Run batch build task failed: id=%s, error=%v", taskID, err)
			}
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

func (btw *BuildTaskWorker) runBatchTask(ctx context.Context, taskID string) (err error) {
	var claimed bool
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		failure := fmt.Errorf("batch build task panicked: %v", recovered)
		var statusErr error
		if claimed {
			_, statusErr = markBatchBuildFailed(ctx, btw.bts, taskID, failure.Error())
		} else {
			_, statusErr = btw.bts.InternalMarkFailed(ctx, nil, taskID, failure.Error(), []string{interfaces.BuildTaskStatusPending})
		}
		err = errors.Join(failure, statusErr)
	}()

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
	if resource.IndexConfig == nil || len(resource.IndexConfig.PrimaryKeyFields) == 0 {
		err = errors.New("batch build resource requires primary_key_fields")
		btw.failTask(taskCtx, taskID, err.Error())
		return err
	}
	if task.IndexConfig == nil || len(task.IndexConfig.PrimaryKeyFields) == 0 {
		err = errors.New("batch build task snapshot requires primary_key_fields")
		btw.failTask(taskCtx, taskID, err.Error())
		return err
	}
	if task.ExecuteType == interfaces.BuildTaskExecuteTypeIncremental {
		err = validateIncrementalBatchResource(resource, task)
	} else {
		err = validateBuildTaskResourceFingerprint(resource, task)
	}
	if err != nil {
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
	var indexName *string
	switch task.ExecuteType {
	case interfaces.BuildTaskExecuteTypeIncremental:
		claimed, resource, err = btw.claimIncrementalBatchTask(taskCtx, task, resource)
	case interfaces.BuildTaskExecuteTypeKeys:
		if !resourcelogic.HasWritableLocalIndex(resource) || !resource.Enabled {
			err = errors.New("keys build requires an enabled resource with an available published index")
			btw.failTask(taskCtx, taskID, err.Error())
			return err
		}
		indexName = &resource.LocalIndexName
		claimed, err = btw.bts.InternalMarkRunning(taskCtx, nil, taskID, indexName)
	default:
		claimed, err = btw.bts.InternalMarkRunning(taskCtx, nil, taskID, nil)
	}
	if err != nil {
		if errors.Is(err, errIncrementalBatchResourceInvalid) {
			btw.failTask(taskCtx, taskID, err.Error())
		}
		return fmt.Errorf("claim batch build task execution: %w", err)
	}
	if !claimed {
		logger.Infof("Batch build task was not claimed for running: id=%s", taskID)
		return nil
	}
	if indexName != nil {
		task.IndexName = *indexName
	}

	if err := btw.bbw.Run(taskCtx, task, resource, catalog); err != nil {
		if _, statusErr := markBatchBuildFailed(taskCtx, btw.bts, taskID, err.Error()); statusErr != nil {
			logger.Errorf("Retry batch build task status update failed: id=%s, error=%v", taskID, statusErr)
		}
		return err
	}
	return nil
}

func (btw *BuildTaskWorker) claimIncrementalBatchTask(ctx context.Context, task *interfaces.BuildTask,
	resource *interfaces.Resource) (bool, *interfaces.Resource, error) {
	if logics.DB == nil {
		return false, resource, fmt.Errorf("database is not initialized")
	}

	tx, err := logics.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, resource, fmt.Errorf("begin incremental claim transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	current, err := btw.bbw.rs.InternalGetByID(ctx, tx, resource.ID)
	if err != nil {
		return false, resource, fmt.Errorf("reload resource before incremental claim: %w", err)
	}
	if err := validateIncrementalBatchResource(current, task); err != nil {
		return false, resource, fmt.Errorf("%w: %w", errIncrementalBatchResourceInvalid, err)
	}

	if task.IndexName != current.LocalIndexName {
		return false, resource, fmt.Errorf("%w: resource local index changed before incremental build", errIncrementalBatchResourceInvalid)
	}

	claimed, err := btw.bts.InternalMarkRunning(ctx, tx, task.ID, nil)
	if err != nil {
		return false, resource, err
	}
	if !claimed {
		return false, resource, nil
	}
	if current.SyncMark != "" {
		progress := interfaces.BuildTaskProgress{SyncedMark: &current.SyncMark}
		updated, err := btw.bts.InternalSetProgress(ctx, tx, task.ID, progress, runningOrStoppingStatuses)
		if err != nil {
			return false, resource, fmt.Errorf("initialize incremental task checkpoint: %w", err)
		}
		if !updated {
			return false, resource, fmt.Errorf("initialize incremental task checkpoint: task status changed")
		}
	}
	if err := tx.Commit(); err != nil {
		return false, resource, fmt.Errorf("commit incremental claim transaction: %w", err)
	}
	committed = true
	task.Status = interfaces.BuildTaskStatusRunning
	if current.SyncMark != "" {
		task.SyncedMark = current.SyncMark
	}
	return true, current, nil
}

func validateIncrementalBatchResource(resource *interfaces.Resource, task *interfaces.BuildTask) error {
	if resource == nil {
		return fmt.Errorf("incremental build resource was deleted")
	}
	if resource.LocalIndexStatus == interfaces.ResourceLocalIndexStatusReadOnly {
		return errors.New("published local index is read-only; run a full build before starting incremental builds")
	}
	if resource.IndexConfig == nil || len(resource.IndexConfig.IncrementalFields) == 0 {
		return errors.New("incremental build resource requires incremental_fields")
	}
	if task == nil || task.IndexConfig == nil || len(task.IndexConfig.IncrementalFields) == 0 {
		return errors.New("incremental build task snapshot requires incremental_fields")
	}
	if err := validateBuildTaskResourceFingerprint(resource, task); err != nil {
		return err
	}
	if !resourcelogic.HasWritableLocalIndex(resource) {
		return errors.New("incremental build requires an available local index")
	}
	if resource.SyncMark == "" {
		return errors.New("incremental baseline is missing; run a full build with the current incremental_fields")
	}
	checkpoint, err := sync_checkpoint.DecodeBatch(resource.SyncMark)
	if err != nil {
		return fmt.Errorf("invalid incremental checkpoint: %w", err)
	}
	effectiveCursorFields := sync_checkpoint.EffectiveCursorFields(
		resource.IndexConfig.IncrementalFields, resource.IndexConfig.PrimaryKeyFields)
	if err := sync_checkpoint.ValidateCursor(checkpoint,
		effectiveCursorFields, resource.SchemaDefinition); err != nil {
		return fmt.Errorf("invalid incremental checkpoint: %w", err)
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
	claimed, err := btw.bts.InternalMarkRunning(ctx, nil, taskID, nil)
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
	if _, err := btw.bts.InternalMarkFailed(ctx, nil, taskID, detail, nil); err != nil {
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

// One worker manager owns the database queues. Reserve the resource before
// dispatch so its batch workers cannot read and write competing snapshots.
func (btw *BuildTaskWorker) reserveBatchTask(task *interfaces.BuildTaskSummary) bool {
	btw.mu.Lock()
	defer btw.mu.Unlock()
	if _, exists := btw.inFlight[task.ID]; exists {
		return false
	}
	for _, id := range btw.batchResourceByTask {
		if id == task.ResourceID {
			return false
		}
	}
	if btw.batchResourceByTask == nil {
		btw.batchResourceByTask = make(map[string]string)
	}
	btw.batchResourceByTask[task.ID] = task.ResourceID
	btw.inFlight[task.ID] = struct{}{}
	return true
}

func (btw *BuildTaskWorker) reservedBatchResources() []string {
	btw.mu.Lock()
	defer btw.mu.Unlock()
	var ids []string
	for _, id := range btw.batchResourceByTask {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (btw *BuildTaskWorker) removeInFlight(id string) {
	btw.mu.Lock()
	defer btw.mu.Unlock()
	delete(btw.inFlight, id)
	delete(btw.batchResourceByTask, id)
}
