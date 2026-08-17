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
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/segmentio/kafka-go"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/build_task"
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
	"vega-backend/logics/resource"
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
	bts         interfaces.BuildTaskService
	cs          interfaces.CatalogService
	httpClient  rest.HTTPClient
	kafkaAccess interfaces.KafkaAccess
	lim         interfaces.LocalIndexManager
	rs          interfaces.ResourceService

	embeddingQueue chan<- string
}

// NewStreamingBuildWorker creates a new build worker.
func NewStreamingBuildWorker(appSetting *common.AppSetting) *streamingBuildWorker {
	rs := resource.NewResourceService(appSetting)
	return &streamingBuildWorker{
		appSetting:  appSetting,
		bts:         build_task.NewBuildTaskService(appSetting, rs),
		cs:          catalog.NewCatalogService(appSetting),
		httpClient:  common.NewHTTPClient(),
		kafkaAccess: logics.KA,
		lim:         local_index.NewLocalIndexManager(appSetting),
		rs:          rs,
	}
}

// Run executes one persisted streaming build task already claimed by the database producer.
func (sbw *streamingBuildWorker) Run(ctx context.Context, buildTaskInfo *interfaces.BuildTask) error {
	if buildTaskInfo == nil {
		return nil
	}
	taskID := buildTaskInfo.ID
	logger.Infof("Starting streaming build task: %s", taskID)
	// Asynchronous tasks have no original request context and perform downstream permission checks as the task creator
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, buildTaskInfo.Creator)

	// Tasks that are stopped during the queue are skipped directly to avoid reviving and overwriting the status after leaving the queue.
	// stopping the queue indicates that the original worker is no longer available, and it will be stopped at the end.
	if buildTaskInfo.Status == interfaces.BuildTaskStatusStopped ||
		buildTaskInfo.Status == interfaces.BuildTaskStatusStopping ||
		buildTaskInfo.Status == interfaces.BuildTaskStatusCancelled {
		logger.Infof("Task %s is %s, skip execution", taskID, buildTaskInfo.Status)
		if buildTaskInfo.Status == interfaces.BuildTaskStatusStopping {
			if _, err := sbw.bts.InternalMarkStopped(ctx, taskID); err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
		}
		return nil
	}
	resourceID := buildTaskInfo.ResourceID
	logger.Infof("Starting build for task: %s, resource: %s", taskID, resourceID)

	// Get resource info
	resource, err := sbw.rs.InternalGetByID(ctx, resourceID)
	if err != nil {
		logger.Errorf("Failed to get resource for task %s: %v", taskID, err)
		return err
	}
	if resource == nil {
		logger.Errorf("Resource not found for task %s, resourceID: %s", taskID, resourceID)
		if err := cancelBuildTaskForDeletedParent(ctx, sbw.bts, taskID, "resource deleted"); err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		// Resource not found, return nil to  stop the task
		return nil
	}

	// Get catalog for MySQL connection
	catalog, err := sbw.cs.InternalGetByID(ctx, resource.CatalogID, true)
	if err != nil {
		if isNotFoundError(err) {
			if updateErr := cancelBuildTaskForDeletedParent(ctx, sbw.bts, buildTaskInfo.ID, "catalog deleted"); updateErr != nil {
				return fmt.Errorf("update build task status failed: %w", updateErr)
			}
			return nil
		}
		return fmt.Errorf("get catalog failed: %w", err)
	}
	if !catalog.Enabled {
		logger.Errorf("Catalog is disabled for task %s, catalogID: %s", buildTaskInfo.ID, resource.CatalogID)
		_, err = sbw.bts.InternalMarkFailed(ctx, buildTaskInfo.ID, "catalog is disabled")
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeMySQL && catalog.ConnectorType != interfaces.ConnectorTypePostgreSQL {
		logger.Errorf("Streaming build only supports MySQL and PostgreSQL connectors. Unsupported connector type: %s", catalog.ConnectorType)
		_, err = sbw.bts.InternalMarkFailed(ctx, buildTaskInfo.ID, "unsupported connector type")
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		// Catalog not found, return nil to stop the task
		return nil
	}

	sourceIdentifier := resource.SourceIdentifier
	database, err := streamingDatabase(catalog)
	if err != nil {
		logger.Errorf("Invalid streaming connector configuration for task %s: %v", taskID, err)
		if _, updateErr := sbw.bts.InternalMarkFailed(ctx, taskID, err.Error()); updateErr != nil {
			return fmt.Errorf("update build task status failed: %w", updateErr)
		}
		return nil
	}

	indexName := getIndexName(resource.ID, buildTaskInfo.ID)
	err = createManagedLocalIndex(ctx, sbw.lim, indexName, buildTaskInfo, resource)
	if err != nil {
		return fmt.Errorf("create local index failed: %w", err)
	}
	if buildTaskHasEmbedding(buildTaskInfo) {
		err = sendEmbeddingTask(ctx, sbw.embeddingQueue, taskID)
		if err != nil {
			return fmt.Errorf("send embedding task failed: %w", err)
		}
		logger.Infof("Embedding task sent for task %s", taskID)
	}

	// Execute build
	err = sbw.executeBuild(ctx, catalog, resource, buildTaskInfo, indexName, database, sourceIdentifier)
	if err != nil {
		_, _ = sbw.bts.InternalMarkFailed(ctx, taskID, err.Error())
		return err
	}

	logger.Infof("Build stopped for task: %s, resource: %s", taskID, resourceID)
	return nil
}

// executeBuild executes the build logic
func (sbw *streamingBuildWorker) executeBuild(ctx context.Context, catalog *interfaces.Catalog, resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask, indexName string, database string, sourceIdentifier string) error {
	// Use the connector name as the Kafka topic prefix
	topic := fmt.Sprintf("%s-%s.%s", interfaces.BUILD_PREFIX, catalog.ID, sourceIdentifier)
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

	err = sbw.createKafkaConnector(ctx, catalog, resource, database, sourceIdentifier)
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
		taskStatus, err := sbw.bts.InternalGetStatus(ctx, buildTaskInfo.ID)
		if err != nil {
			logger.Errorf("Failed to get task status: %v", err)
			time.Sleep(retryInterval)
			continue
		}
		if taskStatus == interfaces.BuildTaskStatusFailed ||
			taskStatus == interfaces.BuildTaskStatusStopped ||
			taskStatus == interfaces.BuildTaskStatusCompleted {
			logger.Infof("Task %s is %s, stop streaming", buildTaskInfo.ID, taskStatus)
			return nil
		}

		// A streaming connector is shared by the catalog. Before a stopped or
		// cancelled task exits, stop it only when no running task still uses it.
		if taskStatus == interfaces.BuildTaskStatusStopping ||
			taskStatus == interfaces.BuildTaskStatusCancelled {
			needStop, err := sbw.checkConnectorNeedToStop(ctx, catalog.ID)
			if err != nil {
				logger.Errorf("Failed to check connector need to stop: %v", err)
				if taskStatus == interfaces.BuildTaskStatusCancelled {
					return nil
				}
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
			if taskStatus == interfaces.BuildTaskStatusCancelled {
				logger.Infof("Task %s is cancelled, exiting...", buildTaskInfo.ID)
				return nil
			}
			logger.Infof("Task %s is stopping, exiting...", buildTaskInfo.ID)
			progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount}
			if _, err = sbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress); err != nil {
				return fmt.Errorf("update build task progress failed: %w", err)
			}
			_, err = sbw.bts.InternalMarkStopped(ctx, buildTaskInfo.ID)
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			logger.Infof("Kafka subscription context canceled, exiting")
			progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount}
			_, err = sbw.bts.InternalSetProgress(context.Background(), nil, buildTaskInfo.ID, progress)
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
			return ctx.Err()
		default:
			// Read message from Kafka
			// Create a context with a timeout to prevent ReadMessage from constantly blocking
			timeoutCtx, cancel := context.WithTimeout(ctx, retryInterval)
			msg, err := sbw.kafkaAccess.ReadMessage(timeoutCtx, reader)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					// If it times out, check if the task status needs to be updated
					if syncedCount > buildTaskInfo.SyncedCount && time.Since(lastUpdateTime) > retryInterval {
						progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount}
						_, _ = sbw.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
						buildTaskInfo.SyncedCount = syncedCount
						lastUpdateTime = time.Now()
					}
				} else {
					logger.Errorf("Streaming task Failed to read message from Kafka: %v", err)
					time.Sleep(retryInterval)
				}
				continue
			}
			// Print the basic information and content of the message
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
					if err := updateResourceIndexName(ctx, resource, sbw.rs, indexName); err != nil {
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
func (sbw *streamingBuildWorker) createKafkaConnector(ctx context.Context, catalog *interfaces.Catalog, _ *interfaces.Resource, database string, sourceIdentifier string) error {
	// get connector
	kafkaConnectSetting := sbw.appSetting.KafkaConnectSetting
	// The connector name is bound to the catalog. Under the catalog, multiple resources share one connector, each subscribing to the topic of its own table
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
		connectorBody := sbw.buildConnectorConfig(connectorName, catalog, database, sourceIdentifier)
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
			if strings.TrimSpace(table) == sourceIdentifier {
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
			newTableList += sourceIdentifier
			config["table.include.list"] = newTableList
			_, _, err = sbw.httpClient.Put(ctx, fmt.Sprintf("%s/%s/config", connectorUrl, connectorName), headers, config)
			if err != nil {
				return fmt.Errorf("Failed to update kafka connector config: %w", err)
			}
			logger.Infof("Updated kafka connector config to include table: %s", sourceIdentifier)
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
func (sbw *streamingBuildWorker) buildConnectorConfig(connectorName string, catalog *interfaces.Catalog, database string, _ string) map[string]any {
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
			// "column.include.list": ?,
			"schema.history.internal.kafka.bootstrap.servers": fmt.Sprintf("%s:%d", mqSetting.MQHost, mqSetting.MQPort),
			"schema.history.internal.kafka.topic":             fmt.Sprintf("%s-schema-changes", interfaces.BUILD_PREFIX),
			"include.schema.changes":                          "true",
			"topic.prefix":                                    fmt.Sprintf("%s-%s", interfaces.BUILD_PREFIX, catalog.ID),
			// "table.include.list": sourceIdentifier,
			// Do not set table.include.list for a catalog-level shared connector: resources added later
			// would otherwise miss their initial full snapshot.
			// "snapshot.mode": "when_needed",
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
		//connectorBody["config"].(map[string]any)["schema.include.list"] = "public" // It is generally not used. table.include.list contains schema information
		connectorBody["config"].(map[string]any)["plugin.name"] = "pgoutput"
	}

	return connectorBody
}

// streamingDatabase returns the Debezium capture database configuration for a catalog.
func streamingDatabase(catalog *interfaces.Catalog) (string, error) {
	switch catalog.ConnectorType {
	case interfaces.ConnectorTypePostgreSQL:
		database, ok := catalog.ConnectorCfg["database"].(string)
		if !ok || strings.TrimSpace(database) == "" {
			return "", fmt.Errorf("PostgreSQL streaming build requires connector_config.database")
		}
		return database, nil
	case interfaces.ConnectorTypeMySQL:
		value, ok := catalog.ConnectorCfg["databases"]
		if !ok {
			return "", fmt.Errorf("MySQL streaming build requires a non-empty connector_config.databases")
		}

		var databases []string
		switch configuredDatabases := value.(type) {
		case []string:
			databases = configuredDatabases
		case []any:
			databases = make([]string, 0, len(configuredDatabases))
			for _, configuredDatabase := range configuredDatabases {
				database, ok := configuredDatabase.(string)
				if !ok {
					return "", fmt.Errorf("MySQL streaming build requires connector_config.databases to contain only strings")
				}
				databases = append(databases, database)
			}
		default:
			return "", fmt.Errorf("MySQL streaming build requires connector_config.databases to be an array")
		}

		if len(databases) == 0 {
			return "", fmt.Errorf("MySQL streaming build requires a non-empty connector_config.databases")
		}
		for _, database := range databases {
			if strings.TrimSpace(database) == "" {
				return "", fmt.Errorf("MySQL streaming build requires connector_config.databases without empty values")
			}
		}
		return strings.Join(databases, ","), nil
	default:
		return "", fmt.Errorf("unsupported streaming connector type: %s", catalog.ConnectorType)
	}
}

// handleUpdateOperation handles update operations
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

// handleDeleteOperation handles deletion operations
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

// check connector need to be stop
func (sbw *streamingBuildWorker) checkConnectorNeedToStop(ctx context.Context, catalogID string) (bool, error) {
	tasks, err := sbw.bts.InternalGetByCatalogID(ctx, catalogID)
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

// getPrimaryKeyValue gets the primary key value
func getPrimaryKeyValue(keyMap map[string]any) []interfaces.KeyValue {
	keySize := len(keyMap)
	primaryKeyValues := make([]interfaces.KeyValue, 0, keySize)
	// Check whether the keyMap contains the payload field
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
