// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics/build_task"
	"vega-backend/logics/catalog"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/filter_condition"
	"vega-backend/logics/local_index"
	model_factory "vega-backend/logics/model_factory"
	"vega-backend/logics/resource"
)

// batchBuildWorker handles build tasks.
type batchBuildWorker struct {
	appSetting *common.AppSetting
	bts        interfaces.BuildTaskService
	cf         interfaces.ConnectorFactory
	cs         interfaces.CatalogService
	lim        interfaces.LocalIndexManager
	mfs        interfaces.ModelFactoryService
	rs         interfaces.ResourceService
	stopped    *atomic.Bool
}

// NewBatchBuildWorker creates a new build worker.
func NewBatchBuildWorker(appSetting *common.AppSetting) *batchBuildWorker {
	rs := resource.NewResourceService(appSetting)
	return &batchBuildWorker{
		appSetting: appSetting,
		bts:        build_task.NewBuildTaskService(appSetting, rs),
		cf:         factory.GetFactory(appSetting),
		rs:         rs,
		cs:         catalog.NewCatalogService(appSetting),
		lim:        local_index.NewLocalIndexManager(appSetting),
		mfs:        model_factory.NewModelFactoryService(appSetting),
	}
}

// Run executes one persisted batch build task already claimed by the database producer.
func (bbw *batchBuildWorker) Run(ctx context.Context, buildTaskInfo *interfaces.BuildTask) error {
	if buildTaskInfo == nil {
		return nil
	}
	taskID := buildTaskInfo.ID
	logger.Infof("Starting batch build task: %s", taskID)
	// Asynchronous tasks have no original request context and perform downstream permission checks as the task creator
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, buildTaskInfo.Creator)

	resourceID := buildTaskInfo.ResourceID
	logger.Infof("Starting build for task: %s, resource: %s", taskID, resourceID)

	// Get resource info
	resource, err := bbw.rs.InternalGetByID(ctx, nil, resourceID)
	if err != nil {
		logger.Errorf("Failed to get resource for task %s: %v", taskID, err)
		return err
	}
	if resource == nil {
		logger.Errorf("Resource not found for task %s, resourceID: %s", taskID, resourceID)
		if err := cancelBuildTaskForDeletedParent(ctx, bbw.bts, taskID, "resource deleted"); err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		// Resource not found, return nil to stop the task
		return nil
	}

	// Before executeBuild creates the index and connects to the data source, confirm the Catalog first. If you Catalog during the queue
	// If it has been deleted, the task will be cancelled directly.
	catalog, err := bbw.cs.InternalGetByID(ctx, resource.CatalogID, true)
	if err != nil {
		if isNotFoundError(err) {
			if updateErr := cancelBuildTaskForDeletedParent(ctx, bbw.bts, taskID, "catalog deleted"); updateErr != nil {
				return fmt.Errorf("update build task status failed: %w", updateErr)
			}
			return nil
		}
		err = fmt.Errorf("get catalog failed: %w", err)
	} else if !catalog.Enabled {
		err = fmt.Errorf("catalog is disabled")
	}

	if err == nil {
		err = bbw.executeBuild(ctx, catalog, resource, buildTaskInfo)
	}
	if err != nil {
		logger.Errorf("Build failed for task %s: %w", taskID, err)
		_, err = bbw.bts.InternalMarkFailed(ctx, nil, taskID, err.Error())
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}

	logger.Infof("Build completed for task: %s, resource: %s", taskID, resourceID)
	return nil
}

func (bbw *batchBuildWorker) isStopping() bool {
	return bbw.stopped != nil && bbw.stopped.Load()
}

func batchBuildExecuteType(buildTask *interfaces.BuildTask) string {
	// Incremental tasks keep using their existing index and checkpoint. They
	// cannot become full rebuilds because that index is not disposable.
	if buildTask.ExecuteType == interfaces.BuildTaskExecuteTypeIncremental {
		return interfaces.BuildTaskExecuteTypeIncremental
	}
	// A full task starts from the beginning only when its persisted progress is
	// empty. Restart with reset clears that progress before dispatching the task.
	if buildTask.SyncedMark == "" && buildTask.SyncedCount == 0 {
		return interfaces.BuildTaskExecuteTypeFull
	}
	return interfaces.BuildTaskExecuteTypeIncremental
}

// buildBatchCursorFilter builds a lexicographic cursor filter for composite keys.
func buildBatchCursorFilter(keys []string, keyValues []interfaces.KeyValue) *interfaces.FilterCondCfg {
	branches := make([]*interfaces.FilterCondCfg, 0, len(keys))
	for i, key := range keys {
		subConditions := make([]*interfaces.FilterCondCfg, 0, i+1)
		for j := 0; j < i; j++ {
			subConditions = append(subConditions, &interfaces.FilterCondCfg{
				Name:        keys[j],
				Operation:   "==",
				ValueOptCfg: interfaces.ValueOptCfg{Value: keyValues[j].Value, ValueFrom: interfaces.ValueFrom_Const},
			})
		}
		subConditions = append(subConditions, &interfaces.FilterCondCfg{
			Name:        key,
			Operation:   "gt",
			ValueOptCfg: interfaces.ValueOptCfg{Value: keyValues[i].Value, ValueFrom: interfaces.ValueFrom_Const},
		})
		branches = append(branches, &interfaces.FilterCondCfg{Operation: "and", SubConds: subConditions})
	}
	return &interfaces.FilterCondCfg{Operation: "or", SubConds: branches}
}

// executeBuild executes the build logic
func (bbw *batchBuildWorker) executeBuild(ctx context.Context, catalog *interfaces.Catalog,
	resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask) error {
	executeType := batchBuildExecuteType(buildTaskInfo)
	indexName := getIndexName(resource.ID, buildTaskInfo.ID)
	err := createManagedLocalIndex(ctx, bbw.lim, indexName, buildTaskInfo, resource)
	if err != nil {
		return fmt.Errorf("create local index failed: %w", err)
	}

	lastSyncedMark := buildTaskInfo.SyncedMark
	if executeType == interfaces.BuildTaskExecuteTypeFull {
		lastSyncedMark = ""
		// All runs are redone from scratch, the vectors are also redone as a whole, and the progress counter is reset to zero at the same time.
		// Otherwise, the display of synced > total will be accumulated across runs
		buildTaskInfo.SyncedCount = 0
		zero := int64(0)
		emptyMark := ""
		progress := interfaces.BuildTaskProgress{
			SyncedCount: &zero,
			SyncedMark:  &emptyMark,
		}
		if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress); err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
	}

	keys := buildTaskBuildKeyFields(buildTaskInfo)
	var lastBatchKeyValues []interfaces.KeyValue
	if lastSyncedMark != "" {
		if err := sonic.Unmarshal([]byte(lastSyncedMark), &lastBatchKeyValues); err != nil {
			return fmt.Errorf("failed to unmarshal synced mark: %w", err)
		}
		if len(lastBatchKeyValues) != len(keys) {
			return fmt.Errorf("invalid synced mark: expected %d key values, got %d", len(keys), len(lastBatchKeyValues))
		}
		for i, key := range keys {
			if lastBatchKeyValues[i].Key != key {
				return fmt.Errorf("invalid synced mark: expected key %q at position %d, got %q", key, i, lastBatchKeyValues[i].Key)
			}
		}
	}

	// Batch read data from MySQL and write to dataset
	batchSize := 1000
	firstQuery := true

	// get total rows from MySQL
	connector, err := bbw.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return fmt.Errorf("create connector instance failed: %w", err)
	}
	if err := connector.Connect(ctx); err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}
	defer func() { _ = connector.Close(ctx) }()
	tableConnector, ok := connector.(interfaces.TableConnector)
	if !ok {
		return fmt.Errorf("connector is not a table connector")
	}

	// Build sort fields
	sortFields := make([]*interfaces.SortField, len(keys))
	for i, field := range keys {
		sortFields[i] = &interfaces.SortField{
			Field: field,
		}
	}

	hasEmbedding := buildTaskHasEmbedding(buildTaskInfo)
	pipeline := &embeddingPipeline{mfs: bbw.mfs}

	syncedCount := buildTaskInfo.SyncedCount
	for {
		if bbw.isStopping() {
			return ErrWorkerManagerStopping
		}
		// Check task status before each batch
		taskStatus, err := bbw.bts.InternalGetStatus(ctx, buildTaskInfo.ID)
		if err != nil {
			return fmt.Errorf("failed to get task status: %w", err)
		}

		if taskStatus == interfaces.BuildTaskStatusCancelled {
			logger.Infof("Task %s is cancelled, exiting...", buildTaskInfo.ID)
			return nil
		}
		if taskStatus == interfaces.BuildTaskStatusFailed ||
			taskStatus == interfaces.BuildTaskStatusStopped ||
			taskStatus == interfaces.BuildTaskStatusCompleted {
			logger.Infof("Task %s is %s, stop batch build", buildTaskInfo.ID, taskStatus)
			return nil
		}

		// Handle stopping status
		if taskStatus == interfaces.BuildTaskStatusStopping {
			// Task is stopping, exit the loop
			logger.Infof("Task %s is stopping, exiting...", buildTaskInfo.ID)
			// Update task status to stopped
			_, err = bbw.bts.InternalMarkStopped(ctx, buildTaskInfo.ID)
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
			return nil
		}

		params := &interfaces.ResourceDataQueryParams{
			Limit:     batchSize,
			Sort:      sortFields,
			NeedTotal: firstQuery,
		}

		// Add filter condition for batch fields if we have last values
		if len(lastBatchKeyValues) > 0 {
			params.FilterCondCfg = buildBatchCursorFilter(keys, lastBatchKeyValues)

			// Convert FilterCondCfg to ActualFilterCond
			fieldMap := map[string]*interfaces.Property{}
			if resource.SchemaDefinition != nil {
				for _, prop := range resource.SchemaDefinition {
					fieldMap[prop.Name] = prop
				}
			}
			actualFilterCond, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldMap)
			if err != nil {
				return fmt.Errorf("create filter condition failed: %w", err)
			}
			params.ActualFilterCond = actualFilterCond
		}

		result, err := tableConnector.ExecuteQuery(ctx, resource, params)
		if err != nil {
			return fmt.Errorf("execute query failed: %w", err)
		}

		totalRows := result.Total
		readRows := len(result.Entries)

		if readRows > 0 {
			// Update lastBatchKeyValues with the last values in this batch
			lastItem := result.Entries[readRows-1]
			lastBatchKeyValues, err = extractKeyValues(keys, lastItem)
			if err != nil {
				return fmt.Errorf("extract cursor key values: %w", err)
			}
			indexDocuments := make(map[string]map[string]any, readRows)
			for _, doc := range result.Entries {
				keyValues, err := extractKeyValues(keys, doc)
				if err != nil {
					return err
				}
				docID, err := generateDocumentID(keyValues)
				if err != nil {
					return err
				}
				if _, exists := indexDocuments[docID]; exists {
					return fmt.Errorf("build document ID: duplicate build key values %q", docID)
				}
				indexDocuments[docID] = doc
			}

			if hasEmbedding {
				if err := pipeline.enrich(ctx, indexDocuments, buildTaskEmbeddingConfig(buildTaskInfo)); err != nil {
					return fmt.Errorf("vectorize batch: %w", err)
				}
			}
			_, err = bbw.lim.IndexDocuments(ctx, indexName, indexDocuments)
			if err != nil {
				return fmt.Errorf("index documents failed: %w", err)
			}

			syncedCount += int64(readRows)
			// Set firstQuery to false after the first query
			progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount}
			if firstQuery {
				firstQuery = false
				totalCount := int64(totalRows)
				progress.TotalCount = &totalCount
			}
			if len(lastBatchKeyValues) > 0 {
				syncedMarkStr, err := sonic.MarshalString(lastBatchKeyValues)
				if err != nil {
					return fmt.Errorf("failed to marshal synced mark: %w", err)
				} else {
					progress.SyncedMark = &syncedMarkStr
				}
			}
			_, err = bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
		}

		if readRows < batchSize {
			break
		}
	}

	if err := completeBuildTaskWithoutEmbedding(ctx, resource, bbw.rs, bbw.bts, buildTaskInfo.ID, indexName); err != nil {
		return fmt.Errorf("complete build task: %w", err)
	}

	return nil
}
