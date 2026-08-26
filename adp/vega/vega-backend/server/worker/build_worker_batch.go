// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/build_task"
	"vega-backend/logics/catalog"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/filter_condition"
	"vega-backend/logics/local_index"
	model_factory "vega-backend/logics/model_factory"
	"vega-backend/logics/resource"
	"vega-backend/logics/sync_checkpoint"
)

var errBuildTaskMarkedFailed = errors.New("build task was marked failed")

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

// Run executes one persisted batch build task with its validated execution context.
func (bbw *batchBuildWorker) Run(ctx context.Context, buildTaskInfo *interfaces.BuildTask,
	resource *interfaces.Resource, catalog *interfaces.Catalog) error {
	if buildTaskInfo == nil {
		return nil
	}
	taskID := buildTaskInfo.ID
	logger.Infof("Starting batch build task: %s", taskID)

	resourceID := buildTaskInfo.ResourceID
	logger.Infof("Starting build for task: %s, resource: %s", taskID, resourceID)

	err := bbw.executeBuild(ctx, catalog, resource, buildTaskInfo)
	if err != nil {
		if errors.Is(err, errBuildTaskMarkedFailed) {
			logger.Infof("Build task failed during final configuration check: %s", taskID)
			return nil
		}
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

func (bbw *batchBuildWorker) completeFullBuildTask(ctx context.Context, resource *interfaces.Resource,
	buildTask *interfaces.BuildTask, indexName, syncMark string) error {
	if logics.DB == nil {
		return errors.New("database is not initialized")
	}

	tx, err := logics.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	current, err := bbw.rs.InternalGetByID(ctx, tx, resource.ID)
	if err != nil {
		return fmt.Errorf("reload resource before publishing index: %w", err)
	}
	if current == nil {
		return errors.New("resource was deleted before publishing index")
	}
	if err := validateBuildTaskResourceFingerprint(current, buildTask); err != nil {
		detail := fmt.Sprintf("cannot publish full build result: %v", err)
		failed, updateErr := bbw.bts.InternalMarkFailed(ctx, tx, buildTask.ID, detail)
		if updateErr != nil {
			return fmt.Errorf("mark build task failed after config change: %w", updateErr)
		}
		if !failed {
			return errors.New("build task status changed before config mismatch could be recorded")
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit build task config failure: %w", err)
		}
		committed = true
		return fmt.Errorf("%w: %s", errBuildTaskMarkedFailed, detail)
	}

	updated, err := bbw.rs.InternalUpdateLocalIndexState(ctx, tx, current.ID,
		interfaces.ResourceLocalIndexStatusAvailable, indexName, syncMark)
	if err != nil {
		return fmt.Errorf("publish resource local index state: %w", err)
	}
	if !updated {
		return errors.New("resource disappeared while publishing local index state")
	}

	completed, err := bbw.bts.InternalMarkCompleted(ctx, tx, buildTask.ID)
	if err != nil {
		return fmt.Errorf("update build task status: %w", err)
	}
	if !completed {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return fmt.Errorf("rollback completion after build task state changed: %w", err)
		}
		committed = true
		// A stop request may win the race with completion. The resource update
		// has been rolled back, so finish the stopping -> stopped transition.
		if _, err := bbw.bts.InternalMarkStopped(ctx, nil, buildTask.ID); err != nil {
			return fmt.Errorf("mark build task stopped: %w", err)
		}
		return nil
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	resource.LocalIndexName = indexName
	resource.SyncMark = syncMark
	return nil
}

func (bbw *batchBuildWorker) commitIncrementalProgress(ctx context.Context, resource *interfaces.Resource,
	buildTask *interfaces.BuildTask, indexName, previousMark, syncMark string,
	progress interfaces.BuildTaskProgress) error {
	if logics.DB == nil {
		return errors.New("database is not initialized")
	}

	tx, err := logics.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incremental checkpoint transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	current, err := bbw.rs.InternalGetByID(ctx, tx, resource.ID)
	if err != nil {
		return fmt.Errorf("reload resource before incremental checkpoint: %w", err)
	}
	if err := validateIncrementalBatchResource(current, buildTask); err != nil {
		return fmt.Errorf("validate resource before incremental checkpoint: %w", err)
	}
	if current.LocalIndexName != indexName {
		return errors.New("resource local index changed during incremental build")
	}
	if current.SyncMark != previousMark {
		return errors.New("resource checkpoint changed during incremental build")
	}

	updated, err := bbw.bts.InternalSetProgress(ctx, tx, buildTask.ID, progress)
	if err != nil {
		return fmt.Errorf("update incremental task checkpoint: %w", err)
	}
	if !updated {
		return errors.New("build task status changed before incremental checkpoint")
	}
	updated, err = bbw.rs.InternalUpdateLocalIndexState(ctx, tx, current.ID,
		interfaces.ResourceLocalIndexStatusAvailable, indexName, syncMark)
	if err != nil {
		return fmt.Errorf("update resource incremental checkpoint: %w", err)
	}
	if !updated {
		return errors.New("resource disappeared while updating incremental checkpoint")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incremental checkpoint transaction: %w", err)
	}
	committed = true
	resource.SyncMark = syncMark
	buildTask.SyncedMark = syncMark
	return nil
}

func (bbw *batchBuildWorker) completeIncrementalBuildTask(ctx context.Context, taskID string) error {
	completed, err := bbw.bts.InternalMarkCompleted(ctx, nil, taskID)
	if err != nil {
		return fmt.Errorf("update build task status: %w", err)
	}
	if completed {
		return nil
	}
	// A stop request may win the race with completion.
	if _, err := bbw.bts.InternalMarkStopped(ctx, nil, taskID); err != nil {
		return fmt.Errorf("mark build task stopped: %w", err)
	}
	return nil
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
	isIncremental := buildTaskInfo.ExecuteType == interfaces.BuildTaskExecuteTypeIncremental
	indexName := buildIndexName(resource.ID, buildTaskInfo.ID)
	if isIncremental {
		indexName = resource.LocalIndexName
	}
	restartFromBeginning := buildTaskInfo.ExecuteType == interfaces.BuildTaskExecuteTypeFull &&
		buildTaskInfo.SyncedMark == ""
	var err error
	switch {
	case restartFromBeginning:
		err = recreateManagedLocalIndex(ctx, bbw.lim, indexName, buildTaskInfo, resource)
	case buildTaskInfo.ExecuteType == interfaces.BuildTaskExecuteTypeFull || isIncremental:
		err = requireManagedLocalIndex(ctx, bbw.lim, indexName)
	default:
		err = fmt.Errorf("unsupported batch build execute type %q", buildTaskInfo.ExecuteType)
	}
	if err != nil {
		return fmt.Errorf("prepare local index failed: %w", err)
	}

	lastSyncedMark := buildTaskInfo.SyncedMark
	if restartFromBeginning {
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
		checkpoint, err := sync_checkpoint.DecodeBatch(lastSyncedMark)
		if err != nil {
			return fmt.Errorf("decode synced mark: %w", err)
		}
		if err := sync_checkpoint.ValidateCursor(checkpoint, keys, resource.SchemaDefinition); err != nil {
			return fmt.Errorf("validate synced mark: %w", err)
		}
		lastBatchKeyValues = checkpoint.Cursor
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
		taskStatus, err := bbw.bts.InternalGetStatusByID(ctx, buildTaskInfo.ID)
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
			_, err = bbw.bts.InternalMarkStopped(ctx, nil, buildTaskInfo.ID)
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
			syncedMarkStr, err := sync_checkpoint.EncodeBatch(lastBatchKeyValues)
			if err != nil {
				return fmt.Errorf("encode synced mark: %w", err)
			}
			progress.SyncedMark = &syncedMarkStr
			if isIncremental {
				if err := bbw.commitIncrementalProgress(ctx, resource, buildTaskInfo,
					indexName, lastSyncedMark, syncedMarkStr, progress); err != nil {
					return fmt.Errorf("commit incremental progress: %w", err)
				}
			} else {
				if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress); err != nil {
					return fmt.Errorf("update build task status failed: %w", err)
				}
			}
			lastSyncedMark = syncedMarkStr
		}

		if readRows < batchSize {
			break
		}
	}

	if !isIncremental && lastSyncedMark == "" {
		lastSyncedMark, err = sync_checkpoint.EncodeBatch(nil)
		if err != nil {
			return fmt.Errorf("encode empty synced mark: %w", err)
		}
		zero := int64(0)
		progress := interfaces.BuildTaskProgress{
			TotalCount:  &zero,
			SyncedCount: &zero,
			SyncedMark:  &lastSyncedMark,
		}
		if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress); err != nil {
			return fmt.Errorf("update empty build task progress: %w", err)
		}
	}
	if isIncremental {
		if err := bbw.completeIncrementalBuildTask(ctx, buildTaskInfo.ID); err != nil {
			return fmt.Errorf("complete incremental build task: %w", err)
		}
		return nil
	} else {
		if err := bbw.completeFullBuildTask(ctx, resource, buildTaskInfo, indexName, lastSyncedMark); err != nil {
			return fmt.Errorf("complete build task: %w", err)
		}
	}
	return nil
}
