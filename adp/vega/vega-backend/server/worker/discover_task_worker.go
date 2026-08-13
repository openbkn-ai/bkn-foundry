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

// DiscoverTaskWorker handles discover tasks.
type DiscoverTaskWorker struct {
	appSetting *common.AppSetting
	cf         interfaces.ConnectorFactory
	cs         interfaces.CatalogService
	dts        interfaces.DiscoverTaskService
	rs         interfaces.ResourceService

	workerCount int
	queueSize   int
	queue       chan string
	mu          sync.Mutex
	inFlight    map[string]struct{}
}

// NewDiscoverTaskWorker creates a new discover worker.
func NewDiscoverTaskWorker(appSetting *common.AppSetting) *DiscoverTaskWorker {
	workerCount := defaultDiscoverWorkerCount
	if appSetting != nil && appSetting.TaskWorker.DiscoverWorkerCount > 0 {
		workerCount = appSetting.TaskWorker.DiscoverWorkerCount
	}
	queueSize := workerCount * taskQueueSizeMultiplier
	return &DiscoverTaskWorker{
		appSetting: appSetting,
		cf:         factory.GetFactory(appSetting),
		cs:         catalog.NewCatalogService(appSetting),
		dts:        discover_task.NewDiscoverTaskService(appSetting),
		rs:         resource.NewResourceService(appSetting),

		workerCount: workerCount,
		queueSize:   queueSize,
		queue:       make(chan string, queueSize),
		inFlight:    make(map[string]struct{}),
	}
}

// startLoops starts the local worker pool and database producer after startup recovery succeeds.
func (dtw *DiscoverTaskWorker) startLoops(ctx context.Context) {
	// Start a fixed worker pool that only consumes the bounded local queue.
	for i := 0; i < dtw.workerCount; i++ {
		go dtw.runQueuedTasks(ctx)
	}
	// Start the only producer. It owns the initial scan and all later refills.
	go dtw.pollTasks(ctx)
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
		case <-ctx.Done():
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
	tasks, err := dtw.dts.InternalList(ctx, interfaces.DiscoverTaskQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{
			Limit:     limit,
			Sort:      interfaces.DiscoverTaskSortCreateTime,
			Direction: interfaces.ASC_DIRECTION,
		},
		Statuses: []string{interfaces.DiscoverTaskStatusPending},
	})
	if err != nil {
		logger.Errorf("List pending discover tasks failed: %v", err)
		return
	}
	for _, task := range tasks {
		// inFlight covers both queued and running tasks while the database may still show pending.
		if task == nil || !dtw.addInFlight(task.ID) {
			continue
		}
		select {
		case dtw.queue <- task.ID:
		case <-ctx.Done():
			dtw.removeInFlight(task.ID)
			return
		}
	}
}

func (dtw *DiscoverTaskWorker) runQueuedTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case taskID := <-dtw.queue:
			dtw.runSafely(ctx, taskID)
			dtw.removeInFlight(taskID)
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

func (dtw *DiscoverTaskWorker) addInFlight(id string) bool {
	dtw.mu.Lock()
	defer dtw.mu.Unlock()
	if _, exists := dtw.inFlight[id]; exists {
		return false
	}
	dtw.inFlight[id] = struct{}{}
	return true
}

func (dtw *DiscoverTaskWorker) removeInFlight(id string) {
	dtw.mu.Lock()
	defer dtw.mu.Unlock()
	delete(dtw.inFlight, id)
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

	// Execute discover : 元数据采集主要逻辑
	//首先根据 catalog ID 获取 catalog 信息，
	//然后根据 catalog 信息获取 connector 信息，
	//然后根据 connector 信息获取 connector 实例，
	//然后根据 connector 实例获取 catalog 的元数据，
	//然后根据 catalog 的元数据获取 catalog 的资源信息：元数据
	result, err := dtw.discoverCatalog(ctx, catalog, taskInfo)
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

// discoverCatalog discovers resources for a specific catalog.
// discoverCatalog 是一个发现目录资源的方法
// 它接收上下文和目录信息，返回发现结果或错误
// 参数:
//   - ctx: 上下文信息，用于控制请求的超时和取消
//   - catalog: 目录信息，包含目录ID和类型等
//
// 返回值:
//   - *interfaces.DiscoverResult: 发现结果，包含发现的资源信息
//   - error: 错误信息，如果发现过程中出现错误
func (dtw *DiscoverTaskWorker) discoverCatalog(ctx context.Context, catalog *interfaces.Catalog,
	task *interfaces.DiscoverTask) (*interfaces.DiscoverResult, error) {

	logger.Infof("Starting discover for catalog: %s", catalog.ID)

	// 验证 catalog 类型
	if catalog.Type != interfaces.CatalogTypePhysical {
		return nil, fmt.Errorf("discover only supports physical catalogs")
	}

	// 1. 创建 Connector 并连接
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
	// 2. 根据 connector category 分发到不同的发现函数：例如mysql会到mysql.go下面进行元数据的采集，里面会有具体的实现
	category := connector.GetCategory()
	switch category {
	// table类型的会到这里，例如mysql
	case interfaces.ConnectorCategoryTable:
		return dtw.discoverTableResources(ctx, catalog, connector, task)
	// index类型的会到这里，例如open search
	case interfaces.ConnectorCategoryIndex:
		return dtw.discoverIndexResources(ctx, catalog, connector, task)
	// fileset类型的会到这里，例如anyshare
	case interfaces.ConnectorCategoryFileset:
		return dtw.discoverFilesetResources(ctx, catalog, connector, task)
	default:
		return nil, fmt.Errorf("unsupported connector category for discover: %s", category)
	}
}

// createAndConnectConnector creates and connects a connector for the catalog.
func (dtw *DiscoverTaskWorker) createAndConnectConnector(ctx context.Context, catalog *interfaces.Catalog) (interfaces.Connector, error) {

	// 创建 connector
	connector, err := dtw.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connector: %w", err)
	}

	// 连接
	if err := connector.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return connector, nil
}
