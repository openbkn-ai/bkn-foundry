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
	"math"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/segmentio/kafka-go"

	"vega-backend/interfaces"
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
		return ra.Update(ctx, resource)
	}

	if resource.LocalIndexName == indexName {
		return nil
	}

	oldIndexName := resource.LocalIndexName
	resource.LocalIndexName = indexName
	if err := ra.Update(ctx, resource); err != nil {
		resource.LocalIndexName = oldIndexName
		return err
	}

	return nil
}

// createLocalIndex creates a local index with vector fields for embedding
func createLocalIndex(ctx context.Context, ds interfaces.DatasetService, buildTask *interfaces.BuildTask, resource *interfaces.Resource) error {
	newResource := *resource
	newResource.ID = getIndexName(resource.ID, buildTask.ID)
	exist, err := ds.CheckExist(ctx, newResource.ID)
	if err != nil {
		return fmt.Errorf("check dataset exist failed: %w", err)
	}
	if exist {
		return nil
	}
	// resource.SchemaDefinition 已由 executeBuild 写入 fulltext 特性（injectFulltextFeatures），
	// 这里直接复用——buildFieldMappings 会据此给 string 字段建 text 子字段。
	// embedding 字段额外追加独立的 _vector 字段。
	if buildTask.EmbeddingFields != "" {
		var newSchema []*interfaces.Property
		newSchema = append(newSchema, resource.SchemaDefinition...)
		for _, field := range strings.Split(buildTask.EmbeddingFields, ",") {
			field = strings.TrimSpace(field)
			if field != "" {
				newSchema = append(newSchema, &interfaces.Property{
					Name: field + "_vector",
					Type: interfaces.DataType_Vector,
					Features: []interfaces.PropertyFeature{
						{
							FeatureType: interfaces.DataType_Vector,
							Config: map[string]any{
								"dimension": buildTask.ModelDimensions,
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
		}
		newResource.SchemaDefinition = newSchema
	}
	return ds.Create(ctx, &newResource)
}

// fieldNameSet 把逗号分隔的字段名解析为集合。
func fieldNameSet(csv string) map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.Split(csv, ",") {
		if f = strings.TrimSpace(f); f != "" {
			set[f] = true
		}
	}
	return set
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

// reconcileFulltextFeatures 让 resource schema 中的 fulltext 特性与 fulltextFields 完全一致：
// 集合内字段补齐(并校正 analyzer)、集合外字段移除残留。返回是否有改动。
//
// 批量构建任务是该资源 fulltext 配置的唯一来源，故可权威重写。必须移除残留：
// 编辑任务去掉某个 fulltext 字段后(PUT /build-tasks/:id)，若只增不减，旧特性留在
// schema 里，drop+recreate 仍会按残留特性重建出多余子字段。analyzer 为空走默认分词器。
func reconcileFulltextFeatures(resource *interfaces.Resource, fulltextFields, analyzer string) bool {
	set := fieldNameSet(fulltextFields)
	var config map[string]any
	if analyzer != "" {
		config = map[string]any{"analyzer": analyzer}
	}
	changed := false
	for _, prop := range resource.SchemaDefinition {
		if prop == nil {
			continue
		}
		switch {
		case set[prop.Name] && !hasFulltextFeature(prop):
			prop.Features = append(prop.Features, interfaces.PropertyFeature{
				FeatureName: "fulltext",
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
				Config:      config,
			})
			changed = true
		case set[prop.Name]:
			// 已有 fulltext 特性：校正 analyzer(用户改了分词器需重建生效)
			for i := range prop.Features {
				if prop.Features[i].FeatureType == interfaces.PropertyFeatureType_Fulltext &&
					analyzerOf(prop.Features[i].Config) != analyzer {
					prop.Features[i].Config = config
					changed = true
				}
			}
		case !set[prop.Name] && hasFulltextFeature(prop):
			// 不在集合内但有残留 fulltext 特性：移除(原地过滤)
			kept := prop.Features[:0]
			for _, f := range prop.Features {
				if f.FeatureType == interfaces.PropertyFeatureType_Fulltext {
					changed = true
					continue
				}
				kept = append(kept, f)
			}
			prop.Features = kept
		}
	}
	return changed
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
		_, err = client.Enqueue(embeddingTask,
			asynq.Queue(interfaces.DefaultQueue),
			asynq.TaskID(fmt.Sprintf("%s-%s", interfaces.BuildTaskTypeEmbedding, taskID)),
			asynq.MaxRetry(interfaces.BUILD_TASK_MAX_RETRY_COUNT),
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
