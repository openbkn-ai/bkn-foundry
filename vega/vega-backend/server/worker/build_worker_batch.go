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
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/build_task"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/catalog"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/dataset"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/local_index"
	model_factory "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/model_factory"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/sync_checkpoint"
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
	lim := local_index.NewLocalIndexManager(appSetting)
	rs := resourcelogic.NewResourceService(appSetting, dataset.NewDatasetService(appSetting))
	return &batchBuildWorker{
		appSetting: appSetting,
		bts:        build_task.NewBuildTaskService(appSetting, rs),
		cf:         factory.NewConnectorFactory(appSetting),
		rs:         rs,
		cs:         catalog.NewCatalogService(appSetting),
		lim:        lim,
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

	var err error
	if buildTaskInfo.ExecuteType == interfaces.BuildTaskExecuteTypeKeys {
		err = bbw.reconcileKeys(ctx, catalog, resource, buildTaskInfo)
	} else {
		err = bbw.executeBuild(ctx, catalog, resource, buildTaskInfo)
	}
	if err != nil {
		if errors.Is(err, errBuildTaskMarkedFailed) {
			logger.Infof("Build task failed during final configuration check: %s", taskID)
			return nil
		}
		logger.Errorf("Batch build execution error: id=%s, error=%v", taskID, err)
		if _, statusErr := markBatchBuildFailed(ctx, bbw.bts, taskID, err.Error()); statusErr != nil {
			return errors.Join(err, statusErr)
		}
		return nil
	}

	logger.Infof("Build completed for task: %s, resource: %s", taskID, resourceID)
	return nil
}

// markBatchBuildFailed records a running task's failure or finishes a stop that won the race.
func markBatchBuildFailed(ctx context.Context, bts interfaces.BuildTaskService, taskID, detail string) (bool, error) {
	failed, err := bts.InternalMarkFailed(ctx, nil, taskID, detail, []string{interfaces.BuildTaskStatusRunning})
	if err != nil {
		return false, fmt.Errorf("mark batch build task failed: %w", err)
	}
	if failed {
		return true, nil
	}
	if _, err := bts.InternalMarkStopped(ctx, nil, taskID, []string{interfaces.BuildTaskStatusStopping}); err != nil {
		return false, fmt.Errorf("mark batch build task stopped: %w", err)
	}
	return false, nil
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
		failed, updateErr := bbw.bts.InternalMarkFailed(ctx, tx, buildTask.ID, detail, []string{interfaces.BuildTaskStatusRunning})
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

	// 仅主键的任务游标用于断点续跑，发布索引时清除 Resource 上的增量基线。
	if len(buildTask.IndexConfig.IncrementalFields) == 0 {
		syncMark = ""
	}
	updated, err := bbw.rs.InternalPublishLocalIndex(ctx, tx, current.ID, indexName, syncMark, current.UpdateTime)
	if err != nil {
		return fmt.Errorf("publish resource local index state: %w", err)
	}
	if !updated {
		return errors.New("resource changed before publishing local index")
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
		if _, err := bbw.bts.InternalMarkStopped(ctx, nil, buildTask.ID, nil); err != nil {
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
	if logics.DB == nil || logics.RA == nil {
		return errors.New("incremental checkpoint access is not initialized")
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

	updated, err := logics.RA.CompareAndSetSyncMark(ctx, tx,
		current.ID, indexName, interfaces.ResourceLocalIndexStatusAvailable, previousMark, syncMark)
	if err != nil {
		return fmt.Errorf("update resource incremental checkpoint: %w", err)
	}
	if !updated {
		return errors.New("resource local index or checkpoint changed during incremental build")
	}
	updated, err = bbw.bts.InternalSetProgress(ctx, tx, buildTask.ID, progress, runningOrStoppingStatuses)
	if err != nil {
		return fmt.Errorf("update incremental task checkpoint: %w", err)
	}
	if !updated {
		return errors.New("build task status changed before incremental checkpoint")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incremental checkpoint transaction: %w", err)
	}
	committed = true
	resource.SyncMark = syncMark
	buildTask.SyncedMark = syncMark
	return nil
}

func (bbw *batchBuildWorker) completeInPlaceBuildTask(ctx context.Context, taskID string) error {
	completed, err := bbw.bts.InternalMarkCompleted(ctx, nil, taskID)
	if err != nil {
		return fmt.Errorf("update build task status: %w", err)
	}
	if completed {
		return nil
	}
	// A stop request may win the race with completion.
	if _, err := bbw.bts.InternalMarkStopped(ctx, nil, taskID, nil); err != nil {
		return fmt.Errorf("mark build task stopped: %w", err)
	}
	return nil
}

// buildBatchCursorFilter builds a lexicographic cursor filter for composite keys.
func buildBatchCursorFilter(keys []string, keyValues []interfaces.KeyValue) (*interfaces.FilterCondCfg, error) {
	if len(keys) != len(keyValues) {
		return nil, fmt.Errorf("batch cursor key count %d does not match value count %d", len(keys), len(keyValues))
	}
	branches := make([]*interfaces.FilterCondCfg, 0, len(keys))
	for i, key := range keys {
		subConditions := make([]*interfaces.FilterCondCfg, 0, i+1)
		for j := 0; j < i; j++ {
			subConditions = append(subConditions, &interfaces.FilterCondCfg{
				Field:     keys[j], //nolint:gosec // keyValues length is checked to equal keys above.
				Operation: "==",
				Value:     keyValues[j].Value, //nolint:gosec // keyValues length is checked to equal keys above.
				ValueFrom: interfaces.ValueFrom_Const,
			})
		}
		subConditions = append(subConditions, &interfaces.FilterCondCfg{
			Field:     key,
			Operation: "gt",
			Value:     keyValues[i].Value, //nolint:gosec // keyValues length is checked to equal keys above.
			ValueFrom: interfaces.ValueFrom_Const,
		})
		branches = append(branches, &interfaces.FilterCondCfg{Operation: "and", SubConds: subConditions})
	}
	return &interfaces.FilterCondCfg{Operation: "or", SubConds: branches}, nil
}

// buildBatchReadParams converts the checkpoint into the filter for a new source query.
func buildBatchReadParams(ctx context.Context, resource *interfaces.Resource, limit int, needTotal bool,
	sortFields []*interfaces.SortField, outputFields, effectiveCursorFields []string,
	lastBatchKeyValues []interfaces.KeyValue) (*interfaces.ResourceDataQueryParams, error) {

	params := &interfaces.ResourceDataQueryParams{
		Paging:       interfaces.PagingRequest{Limit: limit},
		Sort:         sortFields,
		NeedTotal:    needTotal,
		OutputFields: outputFields,
	}
	// Add filter condition for batch fields if we have last values
	if len(lastBatchKeyValues) > 0 {
		filter, err := buildBatchCursorFilter(effectiveCursorFields, lastBatchKeyValues)
		if err != nil {
			return nil, err
		}
		params.FilterCondCfg = filter

		// Convert FilterCondCfg to ActualFilterCond
		fieldMap := map[string]*interfaces.Property{}
		for _, prop := range resource.SchemaDefinition {
			fieldMap[prop.Name] = prop
		}
		// Internal keyset pagination is not a caller-supplied filter.
		// Permit cursor comparisons on copies without changing public metadata.
		for _, name := range effectiveCursorFields {
			if property, exists := fieldMap[name]; exists {
				copied := *property
				copied.Filterable = new(true)
				fieldMap[name] = &copied
			}
		}
		actualFilterCond, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldMap)
		if err != nil {
			return nil, fmt.Errorf("create filter condition failed: %w", err)
		}
		params.ActualFilterCond = actualFilterCond
	}

	return params, nil
}

// executeBuild executes the build logic
func (bbw *batchBuildWorker) executeBuild(ctx context.Context, catalog *interfaces.Catalog,
	resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask) error {
	isIncremental := buildTaskInfo.ExecuteType == interfaces.BuildTaskExecuteTypeIncremental
	indexName := buildTaskInfo.IndexName
	if indexName == "" {
		return errors.New("build task target index is missing; create a new task after upgrade")
	}
	if isIncremental && indexName != resource.LocalIndexName {
		return errors.New("resource local index changed before incremental build")
	}
	columns, err := table.ColumnMetadata(resource)
	if err != nil {
		return err
	}
	restartFromBeginning := buildTaskInfo.ExecuteType == interfaces.BuildTaskExecuteTypeFull &&
		buildTaskInfo.SyncedMark == ""
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
	decimalSchema, err := local_index.DecimalIndexSchema(resource.SchemaDefinition, columns)
	if err != nil {
		return err
	}
	validationResource := &interfaces.Resource{SchemaDefinition: decimalSchema}

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
		if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress, runningOrStoppingStatuses); err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
	}

	primaryKeyFields := buildTaskInfo.IndexConfig.PrimaryKeyFields
	incrementalFields := buildTaskInfo.IndexConfig.IncrementalFields
	effectiveCursorFields := sync_checkpoint.EffectiveCursorFields(incrementalFields, primaryKeyFields)
	var lastBatchKeyValues []interfaces.KeyValue
	if lastSyncedMark != "" {
		checkpoint, err := sync_checkpoint.DecodeBatch(lastSyncedMark)
		if err != nil {
			return fmt.Errorf("decode synced mark: %w", err)
		}
		if err := sync_checkpoint.ValidateCursor(checkpoint, effectiveCursorFields, resource.SchemaDefinition); err != nil {
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
	defer func() { _ = connector.Close(ctx) }()

	if err := connector.Connect(ctx); err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}

	tableConnector, ok := connector.(interfaces.TableConnector)
	if !ok {
		return fmt.Errorf("connector is not a table connector")
	}

	var streamConnector interfaces.TableReadSessionConnector
	if !isIncremental {
		streamConnector, _ = tableConnector.(interfaces.TableReadSessionConnector)
		if streamConnector == nil {
			logger.Warnf("Full build source does not support snapshot reads: task_id=%s, connector_type=%s",
				buildTaskInfo.ID, catalog.ConnectorType)
		}
	}
	var stream interfaces.QueryPageSession
	readCtx := context.WithoutCancel(ctx)
	defer func() {
		if stream != nil {
			_ = stream.Close()
		}
	}()

	// Build sort fields
	sortFields := make([]*interfaces.SortField, len(effectiveCursorFields))
	for i, field := range effectiveCursorFields {
		sortFields[i] = &interfaces.SortField{
			Field: field,
		}
	}

	embeddingConfig := buildTaskEmbeddingConfig(buildTaskInfo)
	hasEmbedding := len(embeddingConfig) > 0
	pipeline := &embeddingPipeline{mfs: bbw.mfs}
	outputFields := buildIndexableFieldNames(resource.SchemaDefinition)

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
			_, err = bbw.bts.InternalMarkStopped(ctx, nil, buildTaskInfo.ID, nil)
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
			return nil
		}

		var result *interfaces.ResourceDataQueryResult
		var sourceTotal *int64
		hasMore := true
		if stream == nil {
			limit := batchSize
			if streamConnector != nil {
				limit = 0 // 会话只执行一次数据查询，批大小由读取循环控制。
			}
			params, paramsErr := buildBatchReadParams(ctx, resource, limit, firstQuery,
				sortFields, outputFields, effectiveCursorFields, lastBatchKeyValues)
			if paramsErr != nil {
				return paramsErr
			}
			if streamConnector != nil {
				stream, err = streamConnector.OpenTableRead(readCtx, resource, params)
				if err != nil {
					return fmt.Errorf("open full build source stream: %w", err)
				}
				if params.NeedTotal {
					sourceTotal, err = stream.ReadTotal(readCtx)
					if err != nil {
						return fmt.Errorf("count full build source rows: %w", err)
					}
				}
			} else {
				result, err = tableConnector.ExecuteQuery(ctx, resource, params)
			}
		}
		if stream != nil {
			page, readErr := stream.ReadPage(readCtx, batchSize)
			if readErr != nil {
				return fmt.Errorf("read full build source page: %w", readErr)
			}
			result = &interfaces.ResourceDataQueryResult{Entries: page.Entries}
			if firstQuery {
				if sourceTotal == nil {
					sourceTotal = page.TotalCount
				}
				result.TotalCount = sourceTotal
			}
			hasMore = page.HasMore
		}
		if err != nil {
			return fmt.Errorf("execute query failed: %w", err)
		}

		readRows := len(result.Entries)
		if firstQuery {
			firstQuery = false
			if result.TotalCount != nil && (*result.TotalCount > 0 || readRows > 0) {
				totalCount := *result.TotalCount
				// 从已提交 checkpoint 恢复时，连接器返回的只有剩余范围；
				// SyncedCount 则在同一任务生命周期内持续累计。全量任务未 reset
				// 重启时同样会恢复 checkpoint，因此不能只按任务类型判断。
				if len(lastBatchKeyValues) > 0 {
					totalCount += syncedCount
				}
				if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID,
					interfaces.BuildTaskProgress{TotalCount: &totalCount}, runningOrStoppingStatuses); err != nil {
					return fmt.Errorf("set build task total count: %w", err)
				}
			}
		}

		if readRows > 0 {
			// Update lastBatchKeyValues with the last values in this batch
			lastItem := result.Entries[readRows-1]
			lastBatchKeyValues, err = extractKeyValues(effectiveCursorFields, lastItem)
			if err != nil {
				return fmt.Errorf("extract cursor key values: %w", err)
			}
			indexDocuments := make(map[string]map[string]any, readRows)
			for _, doc := range result.Entries {
				keyValues, err := extractKeyValues(primaryKeyFields, doc)
				if err != nil {
					return err
				}
				docID, err := generateDocumentID(keyValues)
				if err != nil {
					return err
				}
				if _, exists := indexDocuments[docID]; exists {
					return fmt.Errorf("build document ID: duplicate primary key values %q", docID)
				}
				doc, err = encodeBatchBuildDocument(validationResource, resource, doc)
				if err != nil {
					return fmt.Errorf("build document %q: %w", docID, err)
				}
				indexDocuments[docID] = doc
			}

			if hasEmbedding {
				if err := pipeline.enrich(ctx, indexDocuments, embeddingConfig); err != nil {
					return fmt.Errorf("vectorize batch: %w", err)
				}
			}
			logger.Infof("Indexing batch task %s: source rows=%d, vector fields=%s", buildTaskInfo.ID,
				readRows, formatEmbeddingFields(embeddingConfig))
			err = indexBuildDocuments(ctx, bbw.lim, indexName, indexDocuments)
			if err != nil {
				return fmt.Errorf("index documents failed: %w", err)
			}

			syncedCount += int64(readRows)
			progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount}
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
				if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress, runningOrStoppingStatuses); err != nil {
					return fmt.Errorf("update build task status failed: %w", err)
				}
			}
			lastSyncedMark = syncedMarkStr
		}

		if !hasMore || (stream == nil && readRows < batchSize) {
			break
		}
	}

	if stream != nil {
		closeErr := stream.Close()
		stream = nil
		if closeErr != nil {
			return fmt.Errorf("close full build source stream: %w", closeErr)
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
		if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress, runningOrStoppingStatuses); err != nil {
			return fmt.Errorf("update empty build task progress: %w", err)
		}
	}
	if isIncremental {
		if err := bbw.completeInPlaceBuildTask(ctx, buildTaskInfo.ID); err != nil {
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

func (bbw *batchBuildWorker) reconcileKeys(ctx context.Context, catalog *interfaces.Catalog,
	resource *interfaces.Resource, task *interfaces.BuildTask) error {
	keys, err := build_task.ValidateKeys(resource, task.Keys)
	if err != nil {
		return err
	}
	params, requested, err := buildKeyReadParams(ctx, resource, keys)
	if err != nil {
		return err
	}
	indexName := task.IndexName
	if err := requireManagedLocalIndex(ctx, bbw.lim, indexName); err != nil {
		return errors.New("published index is unavailable; run a full build")
	}
	zero := int64(0)
	total := int64(len(keys))
	progress := interfaces.BuildTaskProgress{TotalCount: &total, SyncedCount: &zero}
	updated, err := bbw.bts.InternalSetProgress(ctx, nil, task.ID, progress, runningOrStoppingStatuses)
	if err != nil {
		return errors.New("could not record keys build progress")
	}
	if !updated {
		return nil
	}

	rows, err := bbw.readKeyRows(ctx, catalog, resource, params, len(keys))
	if err != nil {
		return err
	}
	documents, missing, err := prepareKeyIndexChanges(resource, requested, rows)
	if err != nil {
		return err
	}
	embedding := buildTaskEmbeddingConfig(task)
	if len(documents) > 0 && len(embedding) > 0 {
		pipeline := &embeddingPipeline{mfs: bbw.mfs}
		if err := pipeline.enrich(ctx, documents, embedding); err != nil {
			return errors.New("could not generate vectors for keys build")
		}
	}

	if bbw.isStopping() {
		return ErrWorkerManagerStopping
	}
	current, err := bbw.rs.InternalGetByID(ctx, nil, resource.ID)
	if err != nil {
		return errors.New("could not recheck the published index")
	}
	if !resourcelogic.HasWritableLocalIndex(current) || !current.Enabled || current.LocalIndexName != indexName ||
		validateBuildTaskResourceFingerprint(current, task) != nil {
		return errors.New("published index or resource configuration changed during keys build")
	}
	status, err := bbw.bts.InternalGetStatusByID(ctx, task.ID)
	if err != nil {
		return errors.New("could not recheck keys task status")
	}
	if status == interfaces.BuildTaskStatusStopping {
		_, err = bbw.bts.InternalMarkStopped(ctx, nil, task.ID, []string{interfaces.BuildTaskStatusStopping})
		return err
	}
	if status != interfaces.BuildTaskStatusRunning {
		return nil
	}

	if len(documents) > 0 {
		if err := indexBuildDocuments(ctx, bbw.lim, indexName, documents); err != nil {
			return errors.New("could not write all found keys to the published index")
		}
	}
	if len(missing) > 0 {
		if err := bbw.lim.DeleteDocuments(ctx, indexName, missing); err != nil {
			return errors.New("could not delete all absent keys from the published index")
		}
	}
	// The managed OpenSearch bulk write and delete paths refresh before returning.
	progress = interfaces.BuildTaskProgress{SyncedCount: &total}
	if _, err := bbw.bts.InternalSetProgress(ctx, nil, task.ID, progress, runningOrStoppingStatuses); err != nil {
		return errors.New("could not record keys build completion")
	}
	return bbw.completeInPlaceBuildTask(ctx, task.ID)
}

func (bbw *batchBuildWorker) readKeyRows(ctx context.Context, catalog *interfaces.Catalog, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams, limit int) ([]map[string]any, error) {
	connector, err := bbw.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, errors.New("could not initialize the source connector")
	}
	defer func() { _ = connector.Close(ctx) }()

	source, ok := connector.(interfaces.TableReadSessionConnector)
	if !ok {
		return nil, errors.New("source connector does not support table read sessions")
	}
	if err := connector.Connect(ctx); err != nil {
		return nil, errors.New("could not connect to the source")
	}
	readCtx := context.WithoutCancel(ctx)
	session, err := source.OpenTableRead(readCtx, resource, params)
	if err != nil {
		return nil, errors.New("could not open a source key read session")
	}
	page, readErr := session.ReadPage(readCtx, limit)
	closeErr := session.Close()
	if readErr != nil || closeErr != nil || page == nil || page.HasMore || len(page.Entries) > limit {
		return nil, errors.New("could not complete the source key read")
	}
	return page.Entries, nil
}

func buildKeyReadParams(ctx context.Context, resource *interfaces.Resource,
	keys []map[string]any) (*interfaces.ResourceDataQueryParams, map[string]struct{}, error) {
	fields := resource.IndexConfig.PrimaryKeyFields
	branches := make([]*interfaces.FilterCondCfg, 0, len(keys))
	requested := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		tuple, err := extractKeyValues(fields, key)
		if err != nil {
			return nil, nil, err
		}
		id, err := generateDocumentID(tuple)
		if err != nil {
			return nil, nil, err
		}
		requested[id] = struct{}{}
		conditions := make([]*interfaces.FilterCondCfg, 0, len(tuple))
		for _, value := range tuple {
			conditions = append(conditions, &interfaces.FilterCondCfg{
				Field: value.Key, Operation: filter_condition.OperationEqual, Value: value.Value,
				ValueFrom: interfaces.ValueFrom_Const,
			})
		}
		branches = append(branches, combineKeyConditions("and", conditions))
	}
	props := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, prop := range resource.SchemaDefinition {
		if prop == nil {
			continue
		}
		copied := *prop
		if slices.Contains(fields, prop.Name) {
			// Internal PK lookup is independent of user-configured filter exposure.
			copied.Filterable = new(true)
		}
		props[prop.Name] = &copied
	}
	filter := combineKeyConditions("or", branches)
	actual, err := filter_condition.NewFilterCondition(ctx, filter, props)
	if err != nil {
		return nil, nil, errors.New("could not construct a source primary key lookup")
	}
	return &interfaces.ResourceDataQueryParams{
		OutputFields:  buildIndexableFieldNames(resource.SchemaDefinition),
		FilterCondCfg: filter, ActualFilterCond: actual,
	}, requested, nil
}

func combineKeyConditions(operation string, conditions []*interfaces.FilterCondCfg) *interfaces.FilterCondCfg {
	if len(conditions) == 1 {
		return conditions[0]
	}
	return &interfaces.FilterCondCfg{Operation: operation, SubConds: conditions}
}

func prepareKeyIndexChanges(resource *interfaces.Resource, requested map[string]struct{}, rows []map[string]any) (
	map[string]map[string]any, []string, error) {
	columns, err := table.ColumnMetadata(resource)
	if err != nil {
		return nil, nil, errors.New("could not read source column metadata")
	}
	schema, err := local_index.DecimalIndexSchema(resource.SchemaDefinition, columns)
	if err != nil {
		return nil, nil, errors.New("could not prepare the index schema")
	}
	validationResource := &interfaces.Resource{SchemaDefinition: schema}
	documents := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		tuple, err := extractKeyValues(resource.IndexConfig.PrimaryKeyFields, row)
		if err != nil {
			return nil, nil, errors.New("source row is missing its primary key")
		}
		id, err := generateDocumentID(tuple)
		if err != nil {
			return nil, nil, errors.New("could not encode source primary key")
		}
		if _, found := requested[id]; !found {
			// SQL collation can deem two different strings equal. Never delete the
			// requested ID when the result actually has a different identity.
			return nil, nil, errors.New("source returned a row whose primary key does not exactly match the request")
		}
		if _, duplicate := documents[id]; duplicate {
			return nil, nil, errors.New("source returned multiple rows with the same primary key")
		}
		document, err := encodeBatchBuildDocument(validationResource, resource, row)
		if err != nil {
			return nil, nil, errors.New("could not encode a source row for the local index")
		}
		documents[id] = document
	}

	var missing []string
	for id := range requested {
		if _, found := documents[id]; !found {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	return documents, missing, nil
}

func encodeBatchBuildDocument(validationResource, resource *interfaces.Resource, doc map[string]any) (map[string]any, error) {
	document, err := local_index.EncodeDecimalDocument(validationResource, doc)
	if err != nil {
		return nil, err
	}
	if err := normalizeBuildDocumentFields(document, resource.SchemaDefinition, nil); err != nil {
		return nil, err
	}
	return document, nil
}

func formatEmbeddingFields(config map[string]*interfaces.SmallModel) string {
	if len(config) == 0 {
		return "none"
	}
	fields := make([]string, 0, len(config))
	for field, model := range config {
		dimension := 0
		if model != nil {
			dimension = model.EmbeddingDim
		}
		fields = append(fields, fmt.Sprintf("%s(%d)", field, dimension))
	}
	sort.Strings(fields)
	return strings.Join(fields, ",")
}
