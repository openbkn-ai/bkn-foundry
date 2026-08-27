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
	"vega-backend/logics/catalog"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/discover_task"
	"vega-backend/logics/resource"
)

const (
	discoverTaskPollInterval   = 30 * time.Second
	defaultDiscoverWorkerCount = 1
)

type discoverTaskQueueItem struct {
	taskID    string
	catalogID string
}

// DiscoverTaskWorker handles discover tasks.
type DiscoverTaskWorker struct {
	appSetting *common.AppSetting
	cf         interfaces.ConnectorFactory
	cs         interfaces.CatalogService
	dts        interfaces.DiscoverTaskService
	rs         interfaces.ResourceService

	workerCount      int
	queueSize        int
	queue            chan discoverTaskQueueItem
	mu               sync.Mutex
	activeTaskIDs    map[string]struct{}
	activeCatalogIDs map[string]struct{}

	wg      sync.WaitGroup
	stopCh  chan struct{}
	stopped atomic.Bool
}

// NewDiscoverTaskWorker creates a new discover worker.
func NewDiscoverTaskWorker(appSetting *common.AppSetting) *DiscoverTaskWorker {
	workerCount := defaultDiscoverWorkerCount
	if appSetting != nil && appSetting.TaskWorker.DiscoverWorkerCount > 0 {
		workerCount = appSetting.TaskWorker.DiscoverWorkerCount
	}
	queueSize := workerCount * taskQueueSizeMultiplier
	worker := &DiscoverTaskWorker{
		appSetting: appSetting,
		cf:         factory.GetFactory(appSetting),
		cs:         catalog.NewCatalogService(appSetting),
		dts:        discover_task.NewDiscoverTaskService(appSetting),
		rs:         resource.NewResourceService(appSetting),

		workerCount:      workerCount,
		queueSize:        queueSize,
		queue:            make(chan discoverTaskQueueItem, queueSize),
		activeTaskIDs:    make(map[string]struct{}),
		activeCatalogIDs: make(map[string]struct{}),
		stopCh:           make(chan struct{}),
	}
	worker.stopped.Store(false)
	return worker
}

// Start starts the local worker pool and database producer after startup recovery succeeds.
func (dtw *DiscoverTaskWorker) Start() {
	dtw.wg.Add(dtw.workerCount + 1)
	// Start a fixed worker pool that only consumes the bounded local queue.
	for i := 0; i < dtw.workerCount; i++ {
		go func() {
			defer dtw.wg.Done()
			dtw.runQueuedTasks(context.Background())
		}()
	}
	// Start the only producer. It owns the initial scan and all later refills.
	go func() {
		defer dtw.wg.Done()
		dtw.pollTasks(context.Background())
	}()
}

func (dtw *DiscoverTaskWorker) Stop() {
	dtw.stopped.Store(true)
	close(dtw.stopCh)
	dtw.wg.Wait()
}

func (dtw *DiscoverTaskWorker) recoverInterruptedTasks(ctx context.Context) error {
	const recoveryFailure = "discover task interrupted by service restart"
	for {
		// Always read from offset zero because each batch is removed from the running result set.
		tasks, err := dtw.dts.InternalList(ctx, interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{
				Limit:     dtw.queueSize,
				Sort:      interfaces.DiscoverTaskSortCreateTime,
				Direction: interfaces.ASC_DIRECTION,
			},
			Statuses: []string{interfaces.DiscoverTaskStatusRunning},
		})
		if err != nil {
			return fmt.Errorf("list interrupted discover tasks: %w", err)
		}
		if len(tasks) == 0 {
			return nil
		}
		for _, task := range tasks {
			if task == nil {
				return fmt.Errorf("list interrupted discover tasks returned a nil task")
			}
			changed, err := dtw.dts.InternalMarkFailed(ctx, task.ID, recoveryFailure)
			if err != nil {
				return fmt.Errorf("mark interrupted discover task %s failed: %w", task.ID, err)
			}
			if !changed {
				return fmt.Errorf("interrupted discover task %s was not recovered", task.ID)
			}
		}
	}
}

func (dtw *DiscoverTaskWorker) pollTasks(ctx context.Context) {
	// The producer performs an initial pending-task scan before waiting for signals.
	dtw.fillQueue(ctx)

	ticker := time.NewTicker(discoverTaskPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-dtw.stopCh:
			return
		// The 30-second poll is only a fallback for missed notifications and restart recovery.
		case <-ticker.C:
		// The service signals only after the new task is durably persisted.
		case <-dtw.dts.DispatchSignal():
		}
		dtw.fillQueue(ctx)
	}
}

func (dtw *DiscoverTaskWorker) fillQueue(ctx context.Context) {
	// Refill in batches only after workers have drained the local waiting queue.
	if len(dtw.queue) != 0 {
		return
	}
	limit := cap(dtw.queue)
	offset := 0
	for len(dtw.queue) < cap(dtw.queue) {
		tasks, err := dtw.dts.InternalList(ctx, interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{
				Limit:     limit,
				Offset:    offset,
				Sort:      interfaces.DiscoverTaskSortQueuePriority,
				Direction: interfaces.DESC_DIRECTION,
			},
			Statuses: []string{interfaces.DiscoverTaskStatusPending},
		})
		if err != nil {
			logger.Errorf("List pending discover tasks failed: %v", err)
			return
		}
		if len(tasks) == 0 {
			return
		}
		offset += len(tasks)
		for _, task := range tasks {
			if dtw.stopped.Load() {
				return
			}
			// A Catalog remains reserved from queueing until its task completes, so
			// another worker cannot run the same Catalog concurrently.
			if task == nil {
				continue
			}
			taskItem := discoverTaskQueueItem{
				taskID:    task.ID,
				catalogID: task.CatalogID,
			}
			if !dtw.reserveTask(taskItem) {
				continue
			}
			select {
			case dtw.queue <- taskItem:
			case <-dtw.stopCh:
				dtw.releaseTask(taskItem)
				return
			}
			if len(dtw.queue) == cap(dtw.queue) {
				return
			}
		}
		if len(tasks) < limit {
			return
		}
	}
}

func (dtw *DiscoverTaskWorker) runQueuedTasks(ctx context.Context) {
	for {
		select {
		case <-dtw.stopCh:
			return
		case taskItem := <-dtw.queue:
			if dtw.stopped.Load() {
				return
			}
			dtw.runSafely(ctx, taskItem.taskID)
			dtw.releaseTask(taskItem)
			dtw.dts.RequestDispatch()
		}
	}
}

func (dtw *DiscoverTaskWorker) runSafely(ctx context.Context, taskID string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			detail := fmt.Sprintf("discover task panicked: %v", recovered)
			logger.Errorf("Run discover task panicked: id=%s, error=%v", taskID, recovered)
			if _, err := dtw.dts.InternalMarkFailed(ctx, taskID, detail); err != nil {
				logger.Errorf("Mark discover task failed after panic: id=%s, error=%v", taskID, err)
			}
		}
	}()
	if err := dtw.Run(ctx, taskID); err != nil {
		logger.Errorf("Run discover task failed: id=%s, error=%v", taskID, err)
	}
}

func (dtw *DiscoverTaskWorker) reserveTask(taskItem discoverTaskQueueItem) bool {
	dtw.mu.Lock()
	defer dtw.mu.Unlock()
	if _, exists := dtw.activeTaskIDs[taskItem.taskID]; exists {
		return false
	}
	if _, exists := dtw.activeCatalogIDs[taskItem.catalogID]; exists {
		return false
	}
	dtw.activeTaskIDs[taskItem.taskID] = struct{}{}
	dtw.activeCatalogIDs[taskItem.catalogID] = struct{}{}
	return true
}

func (dtw *DiscoverTaskWorker) releaseTask(taskItem discoverTaskQueueItem) {
	dtw.mu.Lock()
	defer dtw.mu.Unlock()
	delete(dtw.activeCatalogIDs, taskItem.catalogID)
	delete(dtw.activeTaskIDs, taskItem.taskID)
}

// Run executes one discover task selected from the task table.
func (dtw *DiscoverTaskWorker) Run(ctx context.Context, taskID string) error {
	logger.Infof("Starting discover task: %s", taskID)

	taskInfo, err := dtw.dts.InternalGetByID(ctx, taskID)
	if err != nil {
		logger.Errorf("Failed to get task info for task %s: %v", taskID, err)
		return err
	}
	if taskInfo == nil {
		logger.Infof("Discover task not found: id=%s", taskID)
		return nil
	}
	if taskInfo.Status == interfaces.DiscoverTaskStatusCancelled ||
		taskInfo.Status == interfaces.DiscoverTaskStatusCompleted ||
		taskInfo.Status == interfaces.DiscoverTaskStatusFailed {
		logger.Infof("Discover task already finished: id=%s, status=%s", taskInfo.ID, taskInfo.Status)
		return nil
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, taskInfo.Creator)

	actions := interfaces.ActionsFromDiscoverStrategy(taskInfo.Strategy)
	taskInfo.DiscoverActions = &actions

	// Claim the pending task before any execution-time dependency lookup. If the
	// conditional update fails, leave the task pending for a later database poll.
	updated, err := dtw.dts.InternalMarkRunning(ctx, taskID)
	if err != nil {
		return fmt.Errorf("mark discover task running: %w", err)
	}
	if !updated {
		logger.Infof("Discover task status changed before running: id=%s", taskID)
		return nil
	}

	catalog, err := dtw.cs.InternalGetByID(ctx, taskInfo.CatalogID, true)
	if err != nil {
		if isNotFoundError(err) {
			if _, updateErr := dtw.dts.InternalMarkCancelled(ctx, taskID, "catalog deleted"); updateErr != nil {
				return fmt.Errorf("cancel discover task after catalog deletion: %w", updateErr)
			}
			logger.Infof("Discover task cancelled because catalog was deleted: id=%s, catalog_id=%s",
				taskID, taskInfo.CatalogID)
			return nil
		}
		logger.Errorf("Failed to get catalog for task %s: %v", taskID, err)
		if _, updateErr := dtw.dts.InternalMarkFailed(ctx, taskID, err.Error()); updateErr != nil {
			logger.Errorf("Mark discover task failed after catalog lookup error: id=%s, error=%v", taskID, updateErr)
		}
		return err
	}
	if !catalog.Enabled {
		if _, updateErr := dtw.dts.InternalMarkFailed(ctx, taskID, "catalog is disabled"); updateErr != nil {
			return fmt.Errorf("fail discover task for disabled catalog: %w", updateErr)
		}
		logger.Infof("Discover task failed because catalog is disabled: id=%s, catalog_id=%s",
			taskID, taskInfo.CatalogID)
		return nil
	}

	// Execute discover: The main logic of metadata collection
	//First, obtain the catalog information based on the catalog ID.
	//Then obtain the connector information based on the catalog information
	//Then obtain the connector instance based on the connector information
	//Then obtain the metadata of the catalog based on the connector instance
	//Then obtain the resource information of the catalog based on its metadata: metadata
	progress := &discoverTaskReconcileProgress{}
	var result *interfaces.DiscoverResult
	if taskInfo.ResourceID != "" {
		result, err = dtw.discoverResource(ctx, catalog, taskInfo, progress)
	} else {
		result, err = dtw.discoverCatalog(ctx, catalog, taskInfo, progress)
	}
	if err != nil {
		if _, updateErr := dtw.dts.InternalMarkFailed(ctx, taskID, err.Error()); updateErr != nil {
			logger.Errorf("Mark discover task failed after execution error: id=%s, error=%v", taskID, updateErr)
		}
		return err
	}

	// Update task result
	completed, err := dtw.dts.InternalMarkCompleted(ctx, taskID, result)
	if err != nil {
		logger.Errorf("Failed to update result for task %s: %v", taskID, err)
		if _, updateErr := dtw.dts.InternalMarkFailed(ctx, taskID, err.Error()); updateErr != nil {
			logger.Errorf("Mark discover task failed after result update error: id=%s, error=%v", taskID, updateErr)
		}
		return err
	}
	if !completed {
		return nil
	}

	logger.Infof("Discover completed for task: %s, catalog: %s", taskID, catalog.ID)
	return nil
}

// discoverResource refreshes one already-discovered Resource without listing or reconciling its Catalog.
func (dtw *DiscoverTaskWorker) discoverResource(ctx context.Context, catalog *interfaces.Catalog,
	task *interfaces.DiscoverTask, progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {
	resource, err := dtw.rs.InternalGetByID(ctx, nil, task.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("get resource for discovery: %w", err)
	}
	if resource == nil || resource.CatalogID != catalog.ID {
		return nil, fmt.Errorf("resource %s not found in catalog %s", task.ResourceID, catalog.ID)
	}

	connector, err := dtw.createAndConnectConnector(ctx, catalog)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to data source: %w", err)
	}
	defer func() { _ = connector.Close(ctx) }()

	result := &interfaces.DiscoverResult{CatalogID: catalog.ID}
	switch resource.Category {
	case interfaces.ResourceCategoryTable:
		tableConnector, ok := connector.(interfaces.TableConnector)
		if !ok {
			return nil, fmt.Errorf("connector does not support single-resource table metadata refresh")
		}
		table, err := tableConnector.GetTableMetaByIdentifier(ctx, resource.SourceIdentifier)
		if err != nil {
			return nil, fmt.Errorf("get table metadata: %w", err)
		}
		if err := dtw.enrichTableMetadata(ctx, task, tableConnector, []tableDiscoverItem{{
			resource: resource, tableMeta: table, markAfterEnrich: true,
		}}, result, progress); err != nil {
			return nil, err
		}
	case interfaces.ResourceCategoryIndex:
		indexConnector, ok := connector.(interfaces.IndexConnector)
		if !ok {
			return nil, fmt.Errorf("connector does not support single-resource index metadata refresh")
		}
		index, err := indexConnector.GetIndexMetaByIdentifier(ctx, resource.SourceIdentifier)
		if err != nil {
			return nil, fmt.Errorf("get index metadata: %w", err)
		}
		if err := dtw.enrichIndexMetadata(ctx, task, indexConnector, []indexDiscoverItem{{
			resource: resource, indexMeta: index, markAfterEnrich: true,
		}}, result, progress); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("resource category does not support single-resource metadata refresh: %s", resource.Category)
	}
	return result, nil
}

func (dtw *DiscoverTaskWorker) updateProgress(ctx context.Context, taskID string, progress int, message string) error {
	updated, err := dtw.dts.InternalUpdateProgress(ctx, taskID, progress, message)
	if err != nil {
		return fmt.Errorf("update discover task progress: %w", err)
	}
	if !updated {
		return fmt.Errorf("discover task progress was not updated")
	}
	return nil
}

// discoverCatalog discovers resources for a specific catalog.
// discoverCatalog is a method for discovering catalog resources
// It receives context and directory information and returns the discovery results or errors
// Parameter
//
//	-ctx: Context information, used to control the timeout and cancellation of requests
//	-catalog: Directory information, including directory ID and type, etc
//
// Return value:
//   - * interfaces. DiscoverResult: findings, including resource information
//     -error: Error message, if an error occurs during the discovery process
func (dtw *DiscoverTaskWorker) discoverCatalog(ctx context.Context, catalog *interfaces.Catalog,
	task *interfaces.DiscoverTask, progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {

	logger.Infof("Starting discover for catalog: %s", catalog.ID)

	// Verify the catalog type
	if catalog.Type != interfaces.CatalogTypePhysical {
		return nil, fmt.Errorf("discover only supports physical catalogs")
	}

	// 1. Create a Connector and connect
	connector, err := dtw.createAndConnectConnector(ctx, catalog)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to data source: %w", err)
	}
	defer func() { _ = connector.Close(ctx) }()

	if meta, err := connector.GetMetadata(ctx); err == nil {
		catalog.Metadata = meta
		if err := dtw.cs.UpdateMetadata(ctx, catalog.ID, meta); err != nil {
			logger.Errorf("Failed to update catalog metadata: %v", err)
		}
	} else {
		logger.Warnf("Failed to get metadata: %v", err)
	}
	// 2. Distribute to different discovery functions based on the connector category: For example, mysql will collect metadata under mysql.go, where there will be specific implementations
	category := connector.GetCategory()
	switch category {
	// The table type will come here, such as mysql
	case interfaces.ConnectorCategoryTable:
		return dtw.discoverTableResources(ctx, task, catalog, connector, progress)
	// The index type will come here, such as open search
	case interfaces.ConnectorCategoryIndex:
		return dtw.discoverIndexResources(ctx, task, catalog, connector, progress)
	// fileset type ones will come here, such as anyshare
	case interfaces.ConnectorCategoryFileset:
		return dtw.discoverFilesetResources(ctx, task, catalog, connector, progress)
	default:
		return nil, fmt.Errorf("unsupported connector category for discover: %s", category)
	}
}

// createAndConnectConnector creates and connects a connector for the catalog.
func (dtw *DiscoverTaskWorker) createAndConnectConnector(ctx context.Context, catalog *interfaces.Catalog) (interfaces.Connector, error) {

	// Create a connector
	connector, err := dtw.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connector: %w", err)
	}

	// Connect to the data source.
	if err := connector.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return connector, nil
}
