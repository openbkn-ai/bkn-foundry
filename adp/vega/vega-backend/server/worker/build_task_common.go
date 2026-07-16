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
	"math"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/mohae/deepcopy"
	"github.com/segmentio/kafka-go"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/build_task"
)

func getIndexName(resourceID, buildTaskID string) string {
	return interfaces.BuildIndexName(resourceID, buildTaskID)
}

func getEmbeddingTopic(resourceID, buildTaskID string) string {
	return fmt.Sprintf("%s-%s-%s-embedding", interfaces.BUILD_PREFIX, resourceID, buildTaskID)
}

func getOldDocID(primaryKeyValues []interfaces.KeyValue) string {
	// 将primaryKeyValues中的所有值拼接成id
	var idBuilder strings.Builder
	for _, item := range primaryKeyValues {
		fmt.Fprintf(&idBuilder, "%v", item.Value)
		fmt.Fprintf(&idBuilder, "-")
	}
	return idBuilder.String()
}

func getNewDocID(primaryKeyValues []interfaces.KeyValue, document map[string]any) string {
	// 构造新的文档ID，确保与oldDocID的拼接顺序相同
	var newDocIDBuilder strings.Builder
	for _, item := range primaryKeyValues {
		if value, ok := document[item.Key]; ok {
			fmt.Fprintf(&newDocIDBuilder, "%v", value)
			fmt.Fprintf(&newDocIDBuilder, "-")
		}
	}
	return newDocIDBuilder.String()
}

// updateResourceIndexName updates the index name of a resource
func updateResourceIndexName(ctx context.Context, resource *interfaces.Resource, ra interfaces.ResourceAccess, indexName string) error {
	if resource.LocalIndexName == "" {
		resource.LocalIndexName = indexName
		return ra.Update(ctx, nil, resource)
	}

	if resource.LocalIndexName == indexName {
		return nil
	}

	oldIndexName := resource.LocalIndexName
	resource.LocalIndexName = indexName
	if err := ra.Update(ctx, nil, resource); err != nil {
		resource.LocalIndexName = oldIndexName
		return err
	}

	return nil
}

func completeBuildTaskWithoutEmbedding(ctx context.Context, resource *interfaces.Resource, ra interfaces.ResourceAccess, taskAccess interfaces.BuildTaskAccess, taskID, indexName string) error {
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

	oldIndexName := resource.LocalIndexName
	if resource.LocalIndexName != indexName {
		resource.LocalIndexName = indexName
		if err := ra.Update(ctx, tx, resource); err != nil {
			resource.LocalIndexName = oldIndexName
			return fmt.Errorf("update resource index name: %w", err)
		}
	}

	update := interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusCompleted)
	if _, err := taskAccess.UpdateStatus(ctx, tx, taskID, update); err != nil {
		resource.LocalIndexName = oldIndexName
		return fmt.Errorf("update build task status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		resource.LocalIndexName = oldIndexName
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

func claimBuildTaskExecution(ctx context.Context, taskAccess interfaces.BuildTaskAccess, taskID string) (bool, error) {
	allowedStatuses := []string{interfaces.BuildTaskStatusInit}
	if retryCount, ok := asynq.GetRetryCount(ctx); ok && retryCount > 0 {
		allowedStatuses = append(allowedStatuses, interfaces.BuildTaskStatusRunning)
	}
	return taskAccess.UpdateStatus(ctx, nil, taskID,
		interfaces.NewBuildTaskUpdate().
			WithStatus(interfaces.BuildTaskStatusRunning).
			WithErrorMsg(""),
		allowedStatuses...,
	)
}

func isAsynqFinalRetry(ctx context.Context) bool {
	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		return false
	}
	maxRetry, ok := asynq.GetMaxRetry(ctx)
	if !ok {
		return false
	}
	return retryCount >= maxRetry
}

// createManagedLocalIndex creates a build-task local index through LocalIndexManager.
func createManagedLocalIndex(ctx context.Context, lim interfaces.LocalIndexManager, indexName string, buildTask *interfaces.BuildTask, resource *interfaces.Resource) error {
	schema, err := buildLocalIndexSchema(buildTask, resource)
	if err != nil {
		return err
	}
	exist, err := lim.CheckExist(ctx, indexName)
	if err != nil {
		return fmt.Errorf("check local index exist failed: %w", err)
	}
	if exist {
		return nil
	}
	return lim.CreateIndex(ctx, indexName, schema)
}

func buildLocalIndexSchema(buildTask *interfaces.BuildTask, resource *interfaces.Resource) ([]*interfaces.Property, error) {
	var schema []*interfaces.Property
	if resource.SchemaDefinition != nil {
		schemaDefinition, ok := deepcopy.Copy(resource.SchemaDefinition).([]*interfaces.Property)
		if !ok {
			return nil, fmt.Errorf("copy resource schema failed")
		}
		schema = schemaDefinition
	}

	if err := validateTaskFulltextFeatures(schema, buildTask); err != nil {
		return nil, err
	}
	if err := validateTaskEmbeddingFeatures(schema, buildTask); err != nil {
		return nil, err
	}
	return appendTaskEmbeddingVectorFields(schema, buildTask), nil
}

func appendTaskEmbeddingVectorFields(schema []*interfaces.Property, buildTask *interfaces.BuildTask) []*interfaces.Property {
	newSchema := append([]*interfaces.Property{}, schema...)
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector == nil {
			continue
		}
		newSchema = append(newSchema, &interfaces.Property{
			Name: field + "_vector",
			Type: interfaces.DataType_Vector,
			Features: []interfaces.PropertyFeature{
				{
					FeatureType: interfaces.DataType_Vector,
					Config: map[string]any{
						"dimension": feature.Vector.Dimensions,
						"method": map[string]any{
							"name":   "hnsw",
							"engine": "lucene",
							"parameters": map[string]any{
								"ef_construction": 256,
							},
						},
					},
				},
			},
		})
	}
	return newSchema
}

func buildTaskIndexFeatures(buildTask *interfaces.BuildTask) map[string]interfaces.BuildTaskFieldIndexFeature {
	if buildTask == nil || buildTask.IndexConfig == nil {
		return nil
	}
	return buildTask.IndexConfig.Features
}

func buildTaskHasEmbedding(buildTask *interfaces.BuildTask) bool {
	for _, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector != nil {
			return true
		}
	}
	return false
}

func buildTaskBuildKeyFields(buildTask *interfaces.BuildTask) []string {
	if buildTask == nil || buildTask.IndexConfig == nil {
		return nil
	}
	return append([]string(nil), buildTask.IndexConfig.BuildKeyFields...)
}

// hasFulltextFeature 判断字段是否已带 fulltext 特性。
func hasFulltextFeature(prop *interfaces.Property) bool {
	for _, f := range prop.Features {
		if f.FeatureType == interfaces.PropertyFeatureType_Fulltext {
			return true
		}
	}
	return false
}

// analyzerOf 取 fulltext 特性 config 里的分词器名，无则空串。
func analyzerOf(config map[string]any) string {
	if config == nil {
		return ""
	}
	if v, ok := config["analyzer"].(string); ok {
		return v
	}
	return ""
}

func validateTaskFulltextFeatures(schema []*interfaces.Property, buildTask *interfaces.BuildTask) error {
	fulltextConfigs := map[string]*interfaces.BuildTaskFulltextConfig{}
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Fulltext != nil {
			fulltextConfigs[field] = feature.Fulltext
		}
	}

	schemaFulltextFields := map[string]struct{}{}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for i := range prop.Features {
			feature := &prop.Features[i]
			if feature.FeatureType != interfaces.PropertyFeatureType_Fulltext {
				continue
			}
			fieldName := indexFeatureFieldName(prop, *feature)
			schemaFulltextFields[fieldName] = struct{}{}
			fulltextConfig, ok := fulltextConfigs[fieldName]
			if !ok {
				return fmt.Errorf("resource schema fulltext field %q is not in build task index config", fieldName)
			}
			taskAnalyzer := fulltextConfig.Analyzer
			schemaAnalyzer := analyzerOf(feature.Config)
			if schemaAnalyzer != "" && schemaAnalyzer != taskAnalyzer {
				return fmt.Errorf("resource schema fulltext analyzer %q for field %q does not match build task analyzer %q", schemaAnalyzer, fieldName, taskAnalyzer)
			}
			if taskAnalyzer != "" && schemaAnalyzer == "" {
				feature.Config = map[string]any{"analyzer": taskAnalyzer}
			}
		}
	}
	for field := range fulltextConfigs {
		if _, ok := schemaFulltextFields[field]; !ok {
			return fmt.Errorf("build task fulltext field %q is not in resource schema features", field)
		}
	}
	return nil
}

func validateTaskEmbeddingFeatures(schema []*interfaces.Property, buildTask *interfaces.BuildTask) error {
	embeddingFields := map[string]struct{}{}
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector != nil {
			embeddingFields[field] = struct{}{}
		}
	}

	schemaEmbeddingFields := map[string]struct{}{}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			fieldName := indexFeatureFieldName(prop, feature)
			schemaEmbeddingFields[fieldName] = struct{}{}
			if _, ok := embeddingFields[fieldName]; !ok {
				return fmt.Errorf("resource schema embedding field %q is not in build task index config", fieldName)
			}
		}
	}
	for field := range embeddingFields {
		if _, ok := schemaEmbeddingFields[field]; !ok {
			return fmt.Errorf("build task embedding field %q is not in resource schema features", field)
		}
	}
	return nil
}

func indexFeatureFieldName(prop *interfaces.Property, feature interfaces.PropertyFeature) string {
	if feature.RefProperty != "" {
		return feature.RefProperty
	}
	return prop.Name
}

// sendEmbeddingTask sends a embedding task to the queue
func sendEmbeddingTask(client *asynq.Client, taskID string) error {
	embeddingTaskMsg := interfaces.EmbeddingBuildTaskMessage{
		TaskID: taskID,
	}
	payload, err := sonic.Marshal(embeddingTaskMsg)
	if err != nil {
		return err
	} else {
		embeddingTask := asynq.NewTask(interfaces.BuildTaskTypeEmbedding, payload)
		if common.GetDebugMode() || client == nil {
			if !build_task.EnqueueDebugTask(embeddingTask) {
				return fmt.Errorf("debug build task queue is not initialized")
			}
			return nil
		}
		_, err = client.Enqueue(embeddingTask,
			asynq.Queue(interfaces.DefaultQueue),
			asynq.TaskID(fmt.Sprintf("%s-%s", interfaces.BuildTaskTypeEmbedding, taskID)),
			asynq.MaxRetry(interfaces.TaskMaxRetryCount),
			asynq.Timeout(math.MaxInt64),                                                  // 永不超时
			asynq.Deadline(time.Unix(math.MaxInt64/1000000000, math.MaxInt64%1000000000)), // 永不过期
		)
		if err != nil {
			if !errors.Is(err, asynq.ErrTaskIDConflict) {
				return err
			}
		}
	}
	return nil
}

// sendEmbeddingMessage sends a document ID to Kafka for embedding
func sendEmbeddingMessage(ctx context.Context, writer *kafka.Writer, kafkaAccess interfaces.KafkaAccess, docIDs []string) error {
	for _, docID := range docIDs {
		// Create message
		messageData := map[string]any{
			"document_id": docID,
		}
		messageBytes, err := sonic.Marshal(messageData)
		if err != nil {
			return fmt.Errorf("failed to marshal message: %w", err)
		}

		// Write message to Kafka
		// Use docID + timestamp as key to avoid conflicts even if document is modified multiple times
		err = kafkaAccess.WriteMessages(ctx, writer, []kafka.Message{
			{
				Key:   []byte(fmt.Sprintf("%s-%d", docID, time.Now().UnixNano())),
				Value: messageBytes,
			},
		}...)
		if err != nil {
			return fmt.Errorf("failed to write message to Kafka: %w", err)
		}
	}
	return nil
}
