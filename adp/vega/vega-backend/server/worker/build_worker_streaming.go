// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/segmentio/kafka-go"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
)

// getServerID generates a unique server ID based on the connector name
func getServerID(connectorName string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(connectorName))
	return h.Sum32()
}

// getServerName generates a server name based on the hostname hash
func getServerName(hostname string) string {
	h := fnv.New32a()
	h.Write([]byte(hostname))
	return fmt.Sprintf("vega-%d", h.Sum32())
}

// streamingBuildWorker handles build tasks.
type streamingBuildWorker struct {
	appSetting  *common.AppSetting
	taskAccess  interfaces.BuildTaskAccess
	resAccess   interfaces.ResourceAccess
	cs          interfaces.CatalogService
	lim         interfaces.LocalIndexManager
	client      *asynq.Client
	httpClient  rest.HTTPClient
	kafkaAccess interfaces.KafkaAccess
}

// NewStreamingBuildWorker creates a new build worker.
func NewStreamingBuildWorker(appSetting *common.AppSetting) *streamingBuildWorker {
	return &streamingBuildWorker{
		appSetting:  appSetting,
		taskAccess:  logics.BTA,
		resAccess:   logics.RA,
		cs:          catalog.NewCatalogService(appSetting),
		lim:         local_index.NewLocalIndexManager(appSetting),
		client:      logics.AQA.CreateClient(),
		httpClient:  common.NewHTTPClient(),
		kafkaAccess: logics.KA,
	}
}

// HandleTask handles a build task from the queue.
func (sbw *streamingBuildWorker) HandleTask(ctx context.Context, task *asynq.Task) error {
	var msg interfaces.StreamingBuildTaskMessage
	if err := sonic.Unmarshal(task.Payload(), &msg); err != nil {
		logger.Errorf("Failed to unmarshal task message: %v", err)
		return err
	}

	taskID := msg.TaskID
	buildTaskInfo, err := sbw.taskAccess.GetByID(ctx, taskID)
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
			if _, err := sbw.taskAccess.UpdateStatus(ctx, nil, taskID, update); err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
		}
		return nil
	}
	claimed, err := claimBuildTaskExecution(ctx, sbw.taskAccess, taskID)
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
	resource, err := sbw.resAccess.GetByID(ctx, resourceID)
	if err != nil {
		logger.Errorf("Failed to get resource for task %s: %v", taskID, err)
		return err
	}
	if resource == nil {
		logger.Errorf("Resource not found for task %s, resourceID: %s", taskID, resourceID)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg("resource not found")
		_, err = sbw.taskAccess.UpdateStatus(ctx, nil, taskID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		// Resource not found, return nil to  stop the task
		return nil
	}

	// Get catalog for MySQL connection
	catalog, err := sbw.cs.GetByID(ctx, resource.CatalogID, true)
	if err != nil {
		return fmt.Errorf("get catalog failed: %w", err)
	}
	if catalog == nil {
		logger.Errorf("Catalog not found for task %s, catalogID: %s", buildTaskInfo.ID, resource.CatalogID)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg("catalog not found")
		_, err = sbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
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
		_, err = sbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeMySQL && catalog.ConnectorType != interfaces.ConnectorTypePostgreSQL {
		logger.Errorf("Streaming build only supports MySQL and PostgreSQL connectors. Unsupported connector type: %s", catalog.ConnectorType)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg("unsupported connector type")
		_, err = sbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		// Catalog not found, return nil to stop the task
		return nil
	}

	database := catalog.ConnectorCfg["database"]
	if database == nil || database == "" {
		database = resource.Database
	}
	sourceId, err := sbw.formatTableName(resource.SourceIdentifier, catalog.ConnectorType, database)
	if err != nil {
		logger.Errorf("Failed to format table name: %v", err)
		update := interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusFailed).
			WithErrorMsg(err.Error())
		_, err = sbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}

	indexName := getIndexName(resource.ID, buildTaskInfo.ID)
	err = createManagedLocalIndex(ctx, sbw.lim, indexName, buildTaskInfo, resource)
	if err != nil {
		return fmt.Errorf("create local index failed: %w", err)
	}
	if buildTaskHasEmbedding(buildTaskInfo) {
		err = sendEmbeddingTask(sbw.client, taskID)
		if err != nil {
			return fmt.Errorf("send embedding task failed: %w", err)
		}
		logger.Infof("Embedding task sent for task %s", taskID)
	}

	// Execute build
	err = sbw.executeBuild(ctx, catalog, resource, buildTaskInfo, indexName, database, sourceId)
	if err != nil {
		update := interfaces.NewBuildTaskUpdate().WithErrorMsg(err.Error())
		if isAsynqFinalRetry(ctx) {
			update = update.WithStatus(interfaces.BuildTaskStatusFailed)
		}
		_, _ = sbw.taskAccess.UpdateStatus(ctx, nil, taskID, update)
		return err
	}

	logger.Infof("Build stopped for task: %s, resource: %s", taskID, resourceID)
	return nil
}

// executeBuild executes the build logic
func (sbw *streamingBuildWorker) executeBuild(ctx context.Context, catalog *interfaces.Catalog, resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask, indexName string, database any, sourceId string) error {
	// Use the connector name as the Kafka topic prefix
	topic := fmt.Sprintf("%s-%s.%s", interfaces.BUILD_PREFIX, catalog.ID, sourceId)
	groupID := fmt.Sprintf("%s-%s", interfaces.BUILD_PREFIX, resource.ID)

	// Create Kafka topic if it doesn't exist
	if err := sbw.kafkaAccess.CreateTopic(ctx, topic); err != nil {
		return fmt.Errorf("failed to create Kafka topic %s: %w", topic, err)
	}

	// Create Kafka reader
	reader, err := sbw.kafkaAccess.NewReader(ctx, topic, groupID)
	if err != nil {
		return fmt.Errorf("failed to create Kafka reader for topic %s: %w", topic, err)
	}
	defer sbw.kafkaAccess.CloseReader(reader)

	logger.Infof("Started Kafka subscription for topic %s with group ID %s", topic, groupID)

	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	// Create embedding topic if needed
	var writer *kafka.Writer
	if buildTaskHasEmbedding(buildTaskInfo) {
		topic := getEmbeddingTopic(resource.ID, buildTaskInfo.ID)
		// Create Kafka writer
		writer, err = sbw.kafkaAccess.NewWriter(ctx, topic)
		if err != nil {
			logger.Errorf("failed to create Kafka writer: %v", err)
		}
		// Create Kafka topic if it doesn't exist
		if err := sbw.kafkaAccess.CreateTopic(ctx, topic); err != nil {
			logger.Errorf("Failed to create Kafka topic %s failed: %v", topic, err)
		}
		defer sbw.kafkaAccess.CloseWriter(writer)
	}

	err = sbw.createKafkaConnector(ctx, catalog, resource, database, sourceId)
	if err != nil {
		return fmt.Errorf("create kafka connector failed: %w", err)
	}

	retryInterval := interfaces.BUILD_TASK_RETRY_INTERVAL * time.Second
	updatedIndexName := false
	lastUpdateTime := time.Now()
	syncedCount := buildTaskInfo.SyncedCount
	// Message processing loop
	for {
		// Check task status before each batch
		taskStatus, err := sbw.taskAccess.GetStatus(ctx, buildTaskInfo.ID)
		if err != nil {
			logger.Errorf("Failed to get task status: %v", err)
			time.Sleep(retryInterval)
			continue
		}

		// Handle stopping status
		if taskStatus == interfaces.BuildTaskStatusStopping {
			needStop, err := sbw.checkConnectorNeedToStop(ctx, catalog.ID)
			if err != nil {
				logger.Errorf("Failed to check connector need to stop: %v", err)
				time.Sleep(retryInterval)
				continue
			}
			if needStop {
				_, _, _ = sbw.httpClient.Put(ctx, fmt.Sprintf("%s/%s/stop",
					fmt.Sprintf("%s://%s:%d/connectors", sbw.appSetting.KafkaConnectSetting.Protocol, sbw.appSetting.KafkaConnectSetting.Host, sbw.appSetting.KafkaConnectSetting.Port),
					fmt.Sprintf("%s-%s", interfaces.BUILD_PREFIX, catalog.ID)),
					map[string]string{interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON},
					map[string]interface{}{})
			}
			logger.Infof("Task %s is stopping, exiting...", buildTaskInfo.ID)
			update := interfaces.NewBuildTaskUpdate().
				WithStatus(interfaces.BuildTaskStatusStopped).
				WithSyncedCount(syncedCount)
			_, err = sbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			logger.Infof("Kafka subscription context canceled, exiting")
			update := interfaces.NewBuildTaskUpdate().WithSyncedCount(syncedCount)
			_, err = sbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
		default:
			// Read message from Kafka
			// 创建带超时的上下文，避免ReadMessage一直阻塞
			timeoutCtx, cancel := context.WithTimeout(context.Background(), retryInterval)
			defer cancel()
			msg, err := sbw.kafkaAccess.ReadMessage(timeoutCtx, reader)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					// 超时，检查是否需要更新任务状态
					if syncedCount > buildTaskInfo.SyncedCount && time.Since(lastUpdateTime) > retryInterval {
						update := interfaces.NewBuildTaskUpdate().WithSyncedCount(syncedCount)
						_, _ = sbw.taskAccess.UpdateStatus(ctx, nil, buildTaskInfo.ID, update)
						buildTaskInfo.SyncedCount = syncedCount
						lastUpdateTime = time.Now()
					}
				} else {
					logger.Errorf("Streaming task Failed to read message from Kafka: %v", err)
					time.Sleep(retryInterval)
				}
				continue
			}
			// 打印消息的基本信息和内容
			//logger.Debugf("Received message: key=%s, value=%s", string(msg.Key), string(msg.Value))

			// Parse Kafka message to extract data
			var keyMap map[string]any
			var valueMap map[string]any

			// Check if message value is empty
			if len(msg.Value) == 0 {
				logger.Debugf("Empty message value, skipping processing")
				// Commit the message to avoid reprocessing
				if err := sbw.kafkaAccess.CommitMessages(ctx, reader, msg); err != nil {
					logger.Errorf("Failed to commit message: %v", err)
				}
				continue
			}

			if err := sonic.Unmarshal(msg.Key, &keyMap); err != nil {
				logger.Errorf("Failed to unmarshal message key: %v", err)
				time.Sleep(retryInterval)
				continue
			} else if err := sonic.Unmarshal(msg.Value, &valueMap); err != nil {
				logger.Errorf("Failed to unmarshal message value: %v", err)
				time.Sleep(retryInterval)
				continue
			}
			// Extract data from the message
			if payload, ok := valueMap["payload"].(map[string]any); ok {
				op := payload["op"].(string)
				after, _ := payload["after"].(map[string]any)

				// Determine operation type
				switch op {
				case "r", "c":
					// Full snapshot or create operation
					// Create document from the after data
					document := make(map[string]any)
					for k, v := range after {
						document[k] = v
					}

					if docIDs, err := sbw.lim.UpsertDocuments(ctx, indexName, []map[string]any{{"id": getOldDocID(getPrimaryKeyValue(keyMap)), "document": document}}); err != nil {
						logger.Errorf("Failed to write document to local index: %v", err)
						time.Sleep(retryInterval)
						continue
					} else if buildTaskHasEmbedding(buildTaskInfo) && len(docIDs) > 0 {
						// Send document ID to Kafka for embedding
						err = sendEmbeddingMessage(ctx, writer, sbw.kafkaAccess, docIDs)
						if err != nil {
							logger.Errorf(err.Error())
							time.Sleep(retryInterval)
							continue
						}
					}
				case "u":
					// Update operation
					if err := sbw.handleUpdateOperation(ctx, keyMap, after, indexName, buildTaskInfo, writer); err != nil {
						logger.Errorf("Failed to handle update operation: %v", err)
						time.Sleep(retryInterval)
						continue
					}
				case "d":
					// Delete operation
					if err := sbw.handleDeleteOperation(ctx, keyMap, indexName); err != nil {
						logger.Errorf("Failed to handle delete operation: %v", err)
						time.Sleep(retryInterval)
						continue
					}
				default:
					logger.Errorf("Unknown operation type: %s", op)
					time.Sleep(retryInterval)
					continue
				}

				if !updatedIndexName && op != "r" {
					// Full snapshot is completed, update index name in resource
					if err := updateResourceIndexName(ctx, resource, sbw.resAccess, indexName); err != nil {
						logger.Errorf("Failed to update resource index name: %v", err)
					} else {
						updatedIndexName = true
					}
				}
			}

			// Commit the message
			if err := sbw.kafkaAccess.CommitMessages(ctx, reader, msg); err != nil {
				logger.Errorf("Failed to commit message: %v", err)
			}
			syncedCount++
		}
	}
}

// createKafkaConnector creates a Kafka connector for the build task
func (sbw *streamingBuildWorker) createKafkaConnector(ctx context.Context, catalog *interfaces.Catalog, _ *interfaces.Resource, database any, sourceId string) error {
	// get connector
	kafkaConnectSetting := sbw.appSetting.KafkaConnectSetting
	// connector name 和 catalog 绑定，catalog 下多个 resource 公有一个 connector，各自订阅自己的表的 topic
	connectorName := fmt.Sprintf("%s-%s", interfaces.BUILD_PREFIX, catalog.ID)
	connectorUrl := fmt.Sprintf("%s://%s:%d/connectors", kafkaConnectSetting.Protocol, kafkaConnectSetting.Host, kafkaConnectSetting.Port)

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}
	respCode, _, err := sbw.httpClient.Get(ctx, fmt.Sprintf("%s/%s", connectorUrl, connectorName), nil, headers)
	if err != nil {
		return fmt.Errorf("failed to get kafka connector: %w", err)
	}
	switch respCode {
	case http.StatusNotFound:
		connectorBody := sbw.buildConnectorConfig(connectorName, catalog, database, sourceId)
		respCode, respBody, err := sbw.httpClient.Post(ctx, connectorUrl, headers, connectorBody)
		if err != nil {
			return fmt.Errorf("failed to create kafka connector: %w", err)
		}
		if respCode != http.StatusCreated {
			return fmt.Errorf("create kafka connector %s failed, status code: %d, body: %v", connectorName, respCode, respBody)
		}

		logger.Infof("Create kafka connector %s success", connectorName)
	case http.StatusOK:
		// Connector found
		/*config := respBody.(map[string]any)["config"].(map[string]any)
		tableIncludeList, ok := config["table.include.list"].(string)
		if !ok {
			return fmt.Errorf("Invalid table.include.list type: %T", config["table.include.list"])
		}
		table_lists := strings.Split(tableIncludeList, ",")
		tableExist := false
		for _, table := range table_lists {
			if strings.TrimSpace(table) == sourceId {
				tableExist = true
				break
			}
		}
		if !tableExist {
			// update kafka connector config
			newTableList := tableIncludeList
			if newTableList != "" {
				newTableList += ","
			}
			newTableList += sourceId
			config["table.include.list"] = newTableList
			_, _, err = sbw.httpClient.Put(ctx, fmt.Sprintf("%s/%s/config", connectorUrl, connectorName), headers, config)
			if err != nil {
				return fmt.Errorf("Failed to update kafka connector config: %w", err)
			}
			logger.Infof("Updated kafka connector config to include table: %s", sourceId)
		}*/
		// check kafka connector status
		_, respBody, err := sbw.httpClient.Get(ctx, fmt.Sprintf("%s/%s/status", connectorUrl, connectorName), nil, headers)
		if err != nil {
			return fmt.Errorf("failed to get kafka connector status: %w", err)
		}
		// Type assertion for respBody
		if statusBody, ok := respBody.(map[string]any); ok {
			// Type assertion for connector field
			if connector, ok := statusBody["connector"].(map[string]any); ok {
				if state, ok := connector["state"].(string); ok && state != "RUNNING" {
					_, _, err = sbw.httpClient.Put(ctx, fmt.Sprintf("%s/%s/resume", connectorUrl, connectorName), headers, map[string]interface{}{})
					if err != nil {
						return fmt.Errorf("failed to resume kafka connector: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// buildConnectorConfig builds the connector configuration
func (sbw *streamingBuildWorker) buildConnectorConfig(connectorName string, catalog *interfaces.Catalog, database any, _ string) map[string]any {
	// Connector not found, create connector
	mqSetting := sbw.appSetting.MQSetting
	connectorBody := map[string]any{
		"name": connectorName,
		"config": map[string]any{
			"connector.class":   interfaces.ConnectorClassMapping[catalog.ConnectorType],
			"tasks.max":         "1",
			"database.hostname": catalog.ConnectorCfg["host"],
			"database.port":     catalog.ConnectorCfg["port"],
			"database.user":     catalog.ConnectorCfg["username"],
			"database.password": catalog.ConnectorCfg["password"],
			//"column.include.list":   ?,
			"schema.history.internal.kafka.bootstrap.servers": fmt.Sprintf("%s:%d", mqSetting.MQHost, mqSetting.MQPort),
			"schema.history.internal.kafka.topic":             fmt.Sprintf("%s-schema-changes", interfaces.BUILD_PREFIX),
			"include.schema.changes":                          "true",
			"topic.prefix":                                    fmt.Sprintf("%s-%s", interfaces.BUILD_PREFIX, catalog.ID),
			//"table.include.list":                              sourceId, // 同-catalog下多resource构建，公用一个connector，但是如果加了table.include.list，其他resource就没有全量快照，除非一开始就设置到table.include.list中
			//"snapshot.mode":                                   "when_needed",
		},
	}

	if mqSetting.Auth.Mechanism != "" && mqSetting.Auth.Username != "" && mqSetting.Auth.Password != "" {
		jaasConfig := fmt.Sprintf("org.apache.kafka.common.security.plain.PlainLoginModule required username=\"%s\" password=\"%s\";", mqSetting.Auth.Username, mqSetting.Auth.Password)
		connectorBody["config"].(map[string]any)["schema.history.internal.consumer.security.protocol"] = "SASL_PLAINTEXT"
		connectorBody["config"].(map[string]any)["schema.history.internal.consumer.sasl.mechanism"] = mqSetting.Auth.Mechanism
		connectorBody["config"].(map[string]any)["schema.history.internal.consumer.sasl.jaas.config"] = jaasConfig
		connectorBody["config"].(map[string]any)["schema.history.internal.producer.security.protocol"] = "SASL_PLAINTEXT"
		connectorBody["config"].(map[string]any)["schema.history.internal.producer.sasl.mechanism"] = mqSetting.Auth.Mechanism
		connectorBody["config"].(map[string]any)["schema.history.internal.producer.sasl.jaas.config"] = jaasConfig
	}
	switch catalog.ConnectorType {
	case interfaces.ConnectorTypeMySQL:
		connectorBody["config"].(map[string]any)["database.server.id"] = fmt.Sprintf("%d", getServerID(connectorName))
		connectorBody["config"].(map[string]any)["database.server.name"] = getServerName(fmt.Sprintf("%v", catalog.ConnectorCfg["host"]))
		connectorBody["config"].(map[string]any)["database.include.list"] = database
		connectorBody["config"].(map[string]any)["schema.history.internal.store.only.captured.databases.ddl"] = true
		//connectorBody["config"].(map[string]any)["schema.history.internal.store.only.captured.tables.ddl"] = true
	case interfaces.ConnectorTypePostgreSQL:
		connectorBody["config"].(map[string]any)["database.dbname"] = database
		//connectorBody["config"].(map[string]any)["schema.include.list"] = "public" //一般用不上，table.include.list包含schema信息
		connectorBody["config"].(map[string]any)["plugin.name"] = "pgoutput"
	}

	return connectorBody
}

// handleUpdateOperation 处理更新操作
func (sbw *streamingBuildWorker) handleUpdateOperation(ctx context.Context, keyMap, after map[string]any, indexName string, buildTaskInfo *interfaces.BuildTask, writer *kafka.Writer) error {
	primaryKeyValues := getPrimaryKeyValue(keyMap)
	if primaryKeyValues == nil {
		return fmt.Errorf("failed to extract unique key values from keyMap")
	}
	oldDocID := getOldDocID(primaryKeyValues)

	// Create updated document from the after data
	document := make(map[string]any)
	for k, v := range after {
		document[k] = v
	}

	newDocID := getNewDocID(primaryKeyValues, document)
	if newDocID != oldDocID {
		err := sbw.lim.DeleteDocument(ctx, indexName, oldDocID)
		if err != nil {
			return fmt.Errorf("failed to delete document in local index: %w", err)
		}
	}

	_, err := sbw.lim.UpsertDocuments(ctx, indexName, []map[string]any{{"id": newDocID, "document": document}})
	if err != nil {
		return fmt.Errorf("failed to update document in local index: %w", err)
	} else if buildTaskHasEmbedding(buildTaskInfo) {
		// Send document ID to Kafka for embedding
		err = sendEmbeddingMessage(ctx, writer, sbw.kafkaAccess, []string{newDocID})
		if err != nil {
			return err
		}
	}

	return nil
}

// handleDeleteOperation 处理删除操作
func (sbw *streamingBuildWorker) handleDeleteOperation(ctx context.Context, keyMap map[string]any, indexName string) error {
	primaryKeyValues := getPrimaryKeyValue(keyMap)
	if primaryKeyValues == nil {
		return fmt.Errorf("failed to extract unique key values from keyMap")
	}
	oldDocID := getOldDocID(primaryKeyValues)

	// Delete documents by query
	if err := sbw.lim.DeleteDocument(ctx, indexName, oldDocID); err != nil {
		return fmt.Errorf("failed to delete document in local index: %w", err)
	}

	return nil
}

// 格式化table名称
func (sbw *streamingBuildWorker) formatTableName(sourceIdentifier string, connectorType string, database any) (string, error) {
	if database == nil || database == "" {
		return "", fmt.Errorf("database is empty or nil")
	}
	sourceId := sourceIdentifier
	switch connectorType {
	case interfaces.ConnectorTypeMySQL:
		// 如果不是 db.table 格式，前面加上 dbname.
		if !strings.Contains(sourceIdentifier, ".") {
			sourceId = fmt.Sprintf("%v", database) + "." + sourceIdentifier
		}
	case interfaces.ConnectorTypePostgreSQL:
		// 如果是 db.schema.table 格式，去掉 db.
		if strings.Count(sourceIdentifier, ".") >= 2 {
			parts := strings.Split(sourceIdentifier, ".")
			sourceId = strings.Join(parts[1:], ".")
		} else if !strings.Contains(sourceIdentifier, ".") {
			return "", fmt.Errorf("sourceIdentifier %s is not contain database name or schema name", sourceIdentifier)
		}
	default:
		return "", fmt.Errorf("connector type %s is not supported", connectorType)
	}
	return sourceId, nil
}

// check connector need to be stop
func (sbw *streamingBuildWorker) checkConnectorNeedToStop(ctx context.Context, catalogID string) (bool, error) {
	tasks, err := sbw.taskAccess.GetByCatalogID(ctx, catalogID)
	if err != nil {
		return false, fmt.Errorf("failed to get tasks: %w", err)
	}
	for _, task := range tasks {
		if task.Status == interfaces.BuildTaskStatusRunning {
			return false, nil
		}
	}
	return true, nil
}

// getPrimaryKeyValue 获取主键值
func getPrimaryKeyValue(keyMap map[string]any) []interfaces.KeyValue {
	keySize := len(keyMap)
	primaryKeyValues := make([]interfaces.KeyValue, 0, keySize)
	// 检查keyMap是否包含payload字段
	keyData := keyMap
	if payload, ok := keyMap["payload"].(map[string]any); ok {
		keyData = payload
	}

	keys := make([]string, 0, keySize)
	for key := range keyData {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value, ok := keyData[key]; ok {
			primaryKeyValues = append(primaryKeyValues, interfaces.KeyValue{
				Key:   key,
				Value: value,
			})
		}
	}
	return primaryKeyValues
}
