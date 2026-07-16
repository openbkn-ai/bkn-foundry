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
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/segmentio/kafka-go"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	"vega-backend/logics/connectors/factory"
	"vega-backend/logics/filter_condition"
	"vega-backend/logics/local_index"
)

// batchBuildWorker handles build tasks.
type batchBuildWorker struct {
	appSetting  *common.AppSetting
	client      *asynq.Client
	taskAccess  interfaces.BuildTaskAccess
	resAccess   interfaces.ResourceAccess
	cs          interfaces.CatalogService
	lim         interfaces.LocalIndexManager
	kafkaAccess interfaces.KafkaAccess
}

// NewBatchBuildWorker creates a new build worker.
func NewBatchBuildWorker(appSetting *common.AppSetting) *batchBuildWorker {
	var client *asynq.Client
	if !common.GetDebugMode() && logics.AQA != nil {
		client = logics.AQA.CreateClient()
	}
	return &batchBuildWorker{
		appSetting:  appSetting,
		client:      client,
		taskAccess:  logics.BTA,
		resAccess:   logics.RA,
		cs:          catalog.NewCatalogService(appSetting),
		lim:         local_index.NewLocalIndexManager(appSetting),
		kafkaAccess: logics.KA,
	}
}

// HandleTask handles a build task from the queue.
func (bbw *batchBuildWorker) HandleTask(ctx context.Context, task *asynq.Task) error {
	var msg interfaces.BatchBuildTaskMessage
	if err := sonic.Unmarshal(task.Payload(), &msg); err != nil {
		logger.Errorf("Failed to unmarshal task message: %v", err)
		return err
	}

	taskID := msg.TaskID
	buildTaskInfo, err := bbw.taskAccess.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get build task failed: %w", err)
	}
	if buildTaskInfo == nil {
		// Task not found, return nil
		return nil
	}
	// 排队期间被停止的任务直接跳过，避免出队后复活覆写状态。
	// stopping 出队说明原 worker 已不在，兜底落停。
	if buildTaskInfo.Status == interfaces.BuildTaskStatusStopped ||
		buildTaskInfo.Status == interfaces.BuildTaskStatusStopping {
		logger.Infof("Task %s is %s, skip execution", taskID, buildTaskInfo.Status)
		if buildTaskInfo.Status == interfaces.BuildTaskStatusStopping {
			update := interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusStopped)
			if _, err := bbw.taskAccess.UpdateStatus(ctx, nil, taskID, update); err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
		}
		return nil
	}
	claimed, err := claimBuildTaskExecution(ctx, bbw.taskAccess, taskID)
	if err != nil {
		return fmt.Errorf("claim build task execution failed: %w", err)
	}
	if !claimed {
		logger.Infof("Task %s is already claimed or not executable, skip execution", taskID)
		return nil
	}
	// 异步任务无原始请求上下文，以任务创建者身份执行下游权限检查
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, buildTaskInfo.Creator)
	resourceID := buildTaskInfo.ResourceID
	logger.Infof("Starting build for task: %s, resource: %s", taskID, resourceID)

	// Get resource info
	resource, err := bbw.resAccess.GetByID(ctx, resourceID)
	if err != nil {
		logger.Errorf("Failed to get resource for task %s: %v", taskID, err)
		return err
	}
	if resource == nil {
		logger.Errorf("Resource not found for task %s, resourceID: %s", taskID, resourceID)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg("resource not found")
		_, err = bbw.taskAccess.UpdateStatus(ctx, nil, taskID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		// Resource not found, return nil to stop the task
		return nil
	}

	// Execute build
	err = bbw.executeBuild(ctx, resource, buildTaskInfo, msg.ExecuteType)
	if err != nil {
		// Update task status to failed
		logger.Errorf("Build failed for task %s: %w", taskID, err)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg(err.Error())
		_, err = bbw.taskAccess.UpdateStatus(ctx, nil, taskID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}

	logger.Infof("Build completed for task: %s, resource: %s", taskID, resourceID)
	return nil
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
func (bbw *batchBuildWorker) executeBuild(ctx context.Context, resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask, executeType string) error {
	hasEmbedding := buildTaskHasEmbedding(buildTaskInfo)
	// 两个操作均幂等（embedding 任务靠 asynq TaskID 去重，索引已存在则跳过），
	// 不能只在 init 时执行：stop→start 重启后老 embedding worker 已退出，
	// 若不补发，文档 ID 堆积在 Kafka 无消费者，向量化永远停滞
	if hasEmbedding {
		err := sendEmbeddingTask(bbw.client, buildTaskInfo.ID)
		if err != nil {
			return fmt.Errorf("send embedding task failed: %w", err)
		}
		logger.Infof("Embedding task sent for task %s", buildTaskInfo.ID)
	}
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
		update := interfaces.NewBuildTaskUpdate().
			WithSyncedCount(0).
			WithVectorizedCount(0).
			WithSyncedMark("")
		if _, err := bbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update); err != nil {
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

	// Get catalog for MySQL connection
	catalog, err := bbw.cs.GetByID(ctx, resource.CatalogID, true)
	if err != nil {
		return fmt.Errorf("get catalog failed: %w", err)
	}
	if catalog == nil {
		logger.Errorf("Catalog not found for task %s, catalogID: %s", buildTaskInfo.ID, resource.CatalogID)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg("catalog not found")
		_, err = bbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		// Catalog not found, return nil to stop the task
		return nil
	}
	if !catalog.Enabled {
		logger.Errorf("Catalog is disabled for task %s, catalogID: %s", buildTaskInfo.ID, resource.CatalogID)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg("catalog is disabled")
		_, err = bbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}

	// Batch read data from MySQL and write to dataset
	batchSize := 1000
	firstQuery := true

	// get total rows from MySQL
	connector, err := factory.GetFactory().CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
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

	syncedCount := buildTaskInfo.SyncedCount
	for {
		// Check task status before each batch
		taskStatus, err := bbw.taskAccess.GetStatus(ctx, buildTaskInfo.ID)
		if err != nil {
			return fmt.Errorf("failed to get task status: %w", err)
		}

		// Handle stopping status
		if taskStatus == interfaces.BuildTaskStatusStopping {
			// Task is stopping, exit the loop
			logger.Infof("Task %s is stopping, exiting...", buildTaskInfo.ID)
			// Update task status to stopped
			update := interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusStopped)
			_, err = bbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
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
		readRows := len(result.Rows)

		if readRows > 0 {
			// Update lastBatchKeyValues with the last values in this batch
			newSyncedMark := map[string]any{}
			lastItem := result.Rows[readRows-1]
			lastBatchKeyValues = advanceCursor(lastBatchKeyValues, keys, lastItem)
			for _, field := range batchFields {
				newSyncedMark[field] = lastItem[field]
			}

			// Convert documents to upsert format
			upsertRequests := make([]map[string]any, 0, readRows)
			for _, doc := range result.Rows {
				// Create document ID using getNewDocID function
				docID := getNewDocID(lastBatchKeyValues, doc)
				upsertRequests = append(upsertRequests, map[string]any{"id": docID, "document": doc})
			}

			docIDs, err := bbw.lim.UpsertDocuments(ctx, indexName, upsertRequests)
			if err != nil {
				return fmt.Errorf("create documents failed: %w", err)
			}

			syncedCount += int64(readRows)
			// Set firstQuery to false after the first query
			update := interfaces.NewBuildTaskUpdate().WithSyncedCount(syncedCount)
			if firstQuery {
				firstQuery = false
				update = update.WithTotalCount(int64(totalRows))
			}
			if len(newSyncedMark) > 0 {
				syncedMarkStr, err := sonic.MarshalString(newSyncedMark)
				if err != nil {
					return fmt.Errorf("failed to marshal synced mark: %w", err)
				} else {
					update = update.WithSyncedMark(syncedMarkStr)
				}
			}
			_, err = bbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
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
		if err := completeBuildTaskWithoutEmbedding(ctx, resource, bbw.resAccess, bbw.taskAccess, buildTaskInfo.ID, indexName); err != nil {
			return fmt.Errorf("complete build task without embedding: %w", err)
		}
	}

	return nil
}
