// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"fmt"
	"sort"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/segmentio/kafka-go"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/build_task"
	"vega-backend/logics/catalog"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/filter_condition"
	"vega-backend/logics/local_index"
	"vega-backend/logics/resource"
)

// batchBuildWorker handles build tasks.
type batchBuildWorker struct {
	appSetting  *common.AppSetting
	bts         interfaces.BuildTaskService
	cf          interfaces.ConnectorFactory
	cs          interfaces.CatalogService
	kafkaAccess interfaces.KafkaAccess
	lim         interfaces.LocalIndexManager
	rs          interfaces.ResourceService

	embeddingQueue chan<- string
}

// NewBatchBuildWorker creates a new build worker.
func NewBatchBuildWorker(appSetting *common.AppSetting) *batchBuildWorker {
	rs := resource.NewResourceService(appSetting)
	return &batchBuildWorker{
		appSetting:  appSetting,
		bts:         build_task.NewBuildTaskService(appSetting, rs),
		cf:          factory.GetFactory(appSetting),
		rs:          rs,
		cs:          catalog.NewCatalogService(appSetting),
		lim:         local_index.NewLocalIndexManager(appSetting),
		kafkaAccess: logics.KA,
	}
}

// Run executes one persisted batch build task already selected by the database producer.
func (bbw *batchBuildWorker) Run(ctx context.Context, buildTaskInfo *interfaces.BuildTask) error {
	if buildTaskInfo == nil {
		return nil
	}
	taskID := buildTaskInfo.ID
	logger.Infof("Starting batch build task: %s", taskID)
	// 异步任务无原始请求上下文，以任务创建者身份执行下游权限检查
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, buildTaskInfo.Creator)

	// 排队期间被停止的任务直接跳过，避免出队后复活覆写状态。
	// stopping 出队说明原 worker 已不在，兜底落停。
	if buildTaskInfo.Status == interfaces.BuildTaskStatusStopped ||
		buildTaskInfo.Status == interfaces.BuildTaskStatusStopping ||
		buildTaskInfo.Status == interfaces.BuildTaskStatusCancelled {
		logger.Infof("Task %s is %s, skip execution", taskID, buildTaskInfo.Status)
		if buildTaskInfo.Status == interfaces.BuildTaskStatusStopping {
			if _, err := bbw.bts.InternalMarkStopped(ctx, taskID); err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
		}
		return nil
	}
	claimed, err := bbw.bts.InternalMarkRunning(ctx, taskID)
	if err != nil {
		return fmt.Errorf("claim build task execution failed: %w", err)
	}
	if !claimed {
		logger.Infof("Task %s is already claimed or not executable, skip execution", taskID)
		return nil
	}
	resourceID := buildTaskInfo.ResourceID
	logger.Infof("Starting build for task: %s, resource: %s", taskID, resourceID)

	// Get resource info
	resource, err := bbw.rs.InternalGetByID(ctx, resourceID)
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

	// executeBuild 创建索引和连接数据源前先确认 Catalog；若排队期间 Catalog
	// 已被删除，则直接取消任务。
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

	executeType := batchBuildExecuteType(buildTaskInfo)
	if err == nil {
		err = bbw.executeBuild(ctx, catalog, resource, buildTaskInfo, executeType)
	}
	if err != nil {
		logger.Errorf("Build failed for task %s: %w", taskID, err)
		_, err = bbw.bts.InternalMarkFailed(ctx, taskID, err.Error())
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}

	logger.Infof("Build completed for task: %s, resource: %s", taskID, resourceID)
	return nil
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

// advanceCursor 把批读游标推进到本批最后一行的键值。
// 注意必须按下标写回切片：此前用 `for _, kv := range` 改副本，游标永远停在
// 第一批末尾，超过一个批次的表会无限重读同一区间（synced_count 膨胀、压垮索引）。
func advanceCursor(cursor []interfaces.KeyValue, keys []string, lastItem map[string]any) []interfaces.KeyValue {
	if len(cursor) == 0 {
		for _, key := range keys {
			cursor = append(cursor, interfaces.KeyValue{Key: key, Value: lastItem[key]})
		}
		return cursor
	}
	for i := range cursor {
		cursor[i].Value = lastItem[cursor[i].Key]
	}
	return cursor
}

// executeBuild executes the build logic
func (bbw *batchBuildWorker) executeBuild(ctx context.Context, catalog *interfaces.Catalog,
	resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask, executeType string) error {
	indexName := getIndexName(resource.ID, buildTaskInfo.ID)
	err := createManagedLocalIndex(ctx, bbw.lim, indexName, buildTaskInfo, resource)
	if err != nil {
		return fmt.Errorf("create local index failed: %w", err)
	}

	lastSyncedMark := buildTaskInfo.SyncedMark
	if executeType == interfaces.BuildTaskExecuteTypeFull {
		lastSyncedMark = ""
		// 全量重跑从头读、向量也整体重做，进度计数器一并清零，
		// 否则跨运行累计出 synced > total 的显示
		buildTaskInfo.SyncedCount = 0
		buildTaskInfo.VectorizedCount = 0
		zero := int64(0)
		emptyMark := ""
		progress := interfaces.BuildTaskProgress{
			SyncedCount:     &zero,
			VectorizedCount: &zero,
			SyncedMark:      &emptyMark,
		}
		if _, err := bbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress); err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
	}

	batchFields := buildTaskBuildKeyFields(buildTaskInfo)
	keys := batchFields
	sort.Strings(keys)
	var lastBatchKeyValues []interfaces.KeyValue
	if lastSyncedMark != "" {
		// syncMark format : {"filed1_name":field1_value,"filed2_name":field2_value}
		var syncedMark map[string]interface{}
		if err := sonic.Unmarshal([]byte(lastSyncedMark), &syncedMark); err != nil {
			return fmt.Errorf("failed to unmarshal synced mark: %w", err)
		}
		// Extract field names from synced mark
		for _, key := range keys {
			lastBatchKeyValues = append(lastBatchKeyValues, interfaces.KeyValue{
				Key:   key,
				Value: syncedMark[key],
			})
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
	sortFields := make([]*interfaces.SortField, len(batchFields))
	for i, field := range batchFields {
		sortFields[i] = &interfaces.SortField{
			Field: field,
		}
	}

	hasEmbedding := buildTaskHasEmbedding(buildTaskInfo)
	var writer *kafka.Writer
	if hasEmbedding {
		topic := getEmbeddingTopic(resource.ID, buildTaskInfo.ID)
		// Create Kafka writer
		writer, err = bbw.kafkaAccess.NewWriter(ctx, topic)
		if err != nil {
			return fmt.Errorf("failed to create Kafka writer: %w", err)
		}

		err = bbw.kafkaAccess.CreateTopic(ctx, topic)
		if err != nil {
			return fmt.Errorf("failed to create Kafka topic: %w", err)
		}
		defer bbw.kafkaAccess.CloseWriter(writer)
	}
	// Start the embedding phase only after all batch prerequisites are ready,
	// so an early batch failure cannot leave a competing phase running.
	if hasEmbedding {
		if err := sendEmbeddingTask(ctx, bbw.embeddingQueue, buildTaskInfo.ID); err != nil {
			return fmt.Errorf("send embedding task failed: %w", err)
		}
		logger.Infof("Embedding task sent for task %s", buildTaskInfo.ID)
	}

	syncedCount := buildTaskInfo.SyncedCount
	for {
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
			// Build AND condition for multiple batch fields
			subConditions := make([]*interfaces.FilterCondCfg, len(batchFields))
			for i, field := range batchFields {
				subConditions[i] = &interfaces.FilterCondCfg{
					Name:        field,
					Operation:   "gt",
					ValueOptCfg: interfaces.ValueOptCfg{Value: lastBatchKeyValues[i].Value, ValueFrom: interfaces.ValueFrom_Const},
				}
			}
			params.FilterCondCfg = &interfaces.FilterCondCfg{
				Operation: "and",
				SubConds:  subConditions,
			}

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
			newSyncedMark := map[string]any{}
			lastItem := result.Entries[readRows-1]
			lastBatchKeyValues = advanceCursor(lastBatchKeyValues, keys, lastItem)
			for _, field := range batchFields {
				newSyncedMark[field] = lastItem[field]
			}

			// Convert documents to upsert format
			upsertRequests := make([]map[string]any, 0, readRows)
			for _, doc := range result.Entries {
				docID := getNewDocID(lastBatchKeyValues, doc)
				if docID == "" {
					return fmt.Errorf("build document ID: no build key values found in source row")
				}
				upsertRequests = append(upsertRequests, map[string]any{"id": docID, "document": doc})
			}

			docIDs, err := bbw.lim.UpsertDocuments(ctx, indexName, upsertRequests)
			if err != nil {
				return fmt.Errorf("create documents failed: %w", err)
			}

			syncedCount += int64(readRows)
			// Set firstQuery to false after the first query
			progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount}
			if firstQuery {
				firstQuery = false
				totalCount := int64(totalRows)
				progress.TotalCount = &totalCount
			}
			if len(newSyncedMark) > 0 {
				syncedMarkStr, err := sonic.MarshalString(newSyncedMark)
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

			// Send document IDs to Kafka for embedding
			if len(docIDs) > 0 && hasEmbedding {
				err = sendEmbeddingMessage(ctx, writer, bbw.kafkaAccess, docIDs)
				if err != nil {
					return err
				}
			}
		}

		if readRows < batchSize {
			if hasEmbedding {
				// sync complete, push a empty document to trigger embedding
				err = sendEmbeddingMessage(ctx, writer, bbw.kafkaAccess, []string{interfaces.EmptyDocumentID})
				if err != nil {
					return err
				}
			}
			break
		}
	}

	if !hasEmbedding {
		if err := completeBuildTaskWithoutEmbedding(ctx, resource, bbw.rs, bbw.bts, buildTaskInfo.ID, indexName); err != nil {
			return fmt.Errorf("complete build task without embedding: %w", err)
		}
	}

	return nil
}
