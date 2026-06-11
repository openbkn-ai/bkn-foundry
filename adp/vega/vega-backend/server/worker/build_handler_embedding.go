// Copyright 2026 openbkn.ai
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
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/kweaver-ai/kweaver-go-lib/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/connectors"
	opensearchConnector "vega-backend/logics/connectors/local/index/opensearch"
	"vega-backend/logics/dataset"
)

// embeddingHandler handles embedding tasks.
type embeddingHandler struct {
	appSetting  *common.AppSetting
	taskAccess  interfaces.BuildTaskAccess
	resAccess   interfaces.ResourceAccess
	ds          interfaces.DatasetService
	connector   connectors.IndexConnector
	kafkaAccess interfaces.KafkaAccess
	mfa         interfaces.ModelFactoryAccess
	sleep       func(time.Duration) // 重试等待，测试中注入空实现避免真实 sleep
}

// pause 等待指定时长；未注入时使用 time.Sleep
func (eh *embeddingHandler) pause(d time.Duration) {
	if eh.sleep != nil {
		eh.sleep(d)
		return
	}
	time.Sleep(d)
}

// NewEmbeddingBuildHandler creates a new embedding handler.
func NewEmbeddingBuildHandler(appSetting *common.AppSetting) *embeddingHandler {
	opensearchSetting, ok := appSetting.DepServices["opensearch"]
	if !ok {
		panic("opensearch service not found in depServices")
	}
	cfg := interfaces.ConnectorConfig{
		"host":          opensearchSetting["host"],
		"port":          opensearchSetting["port"],
		"username":      opensearchSetting["user"],
		"password":      opensearchSetting["password"],
		"index_pattern": opensearchSetting["index_pattern"],
	}
	connector, err := opensearchConnector.NewOpenSearchConnector().New(cfg)
	if err != nil {
		panic(fmt.Sprintf("failed to create OpenSearch connector: %v", err))
	}
	return &embeddingHandler{
		appSetting:  appSetting,
		taskAccess:  logics.BTA,
		resAccess:   logics.RA,
		ds:          dataset.NewDatasetService(appSetting),
		connector:   connector.(connectors.IndexConnector),
		kafkaAccess: logics.KA,
		mfa:         logics.MFA,
	}
}

// HandleTask handles an embedding task from the queue.
func (eh *embeddingHandler) HandleTask(ctx context.Context, task *asynq.Task) error {
	var msg interfaces.EmbeddingBuildTaskMessage
	if err := sonic.Unmarshal(task.Payload(), &msg); err != nil {
		logger.Errorf("Failed to unmarshal task message: %v", err)
		return err
	}

	taskID := msg.TaskID
	buildTaskInfo, err := eh.taskAccess.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get build task failed: %w", err)
	}
	if buildTaskInfo == nil {
		// Task not found, return nil
		return nil
	}
	// 异步任务无原始请求上下文，以任务创建者身份执行下游权限检查
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, buildTaskInfo.Creator)
	logger.Infof("Starting embedding for task: %s, resource: %s", taskID, buildTaskInfo.ResourceID)

	// Get resource info
	resource, err := eh.resAccess.GetByID(ctx, buildTaskInfo.ResourceID)
	if err != nil {
		logger.Errorf("Failed to get resource for task %s: %v", taskID, err)
		return err
	}
	if resource == nil {
		logger.Errorf("Resource not found for task %s, resourceID: %s", taskID, buildTaskInfo.ResourceID)
		// Resource not found, return nil to  stop the task
		return nil
	}

	// Update task status to running
	err = eh.taskAccess.UpdateStatus(ctx, taskID, map[string]interface{}{"status": interfaces.BuildTaskStatusRunning})
	if err != nil {
		return fmt.Errorf("update build task status failed: %w", err)
	}

	// Execute embedding
	embed_err := eh.executeEmbedding(ctx, resource, buildTaskInfo)
	logger.Infof("executeEmbedding completed")
	if embed_err != nil {
		// Update task status to failed
		err = eh.taskAccess.UpdateStatus(ctx, taskID, map[string]interface{}{"errorMsg": embed_err.Error()})
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return embed_err
	}

	logger.Infof("Embedding completed for task: %s, resource: %s", taskID, buildTaskInfo.ResourceID)
	return nil
}

// executeEmbedding executes the embedding logic
func (eh *embeddingHandler) executeEmbedding(ctx context.Context, resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask) error {
	// get vector fields from resource.schema_definition
	fieldsMap := make(map[string]*interfaces.Property)
	embeddingFields := strings.Split(buildTaskInfo.EmbeddingFields, ",")
	for _, prop := range resource.SchemaDefinition {
		fieldsMap[prop.Name] = prop
	}

	// Use the connector name as the Kafka topic prefix
	topic := getEmbeddingTopic(resource.ID, buildTaskInfo.ID)
	groupID := fmt.Sprintf("%s-embedding-%s", interfaces.BUILD_PREFIX, resource.ID)

	// Create Kafka topic if it doesn't exist
	if err := eh.kafkaAccess.CreateTopic(ctx, topic); err != nil {
		return fmt.Errorf("failed to create Kafka topic %s: %w", topic, err)
	}

	// Create Kafka reader
	reader, err := eh.kafkaAccess.NewReader(ctx, topic, groupID)
	if err != nil {
		return fmt.Errorf("failed to create Kafka reader for topic %s: %w", topic, err)
	}
	defer eh.kafkaAccess.CloseReader(reader)

	logger.Infof("Started Kafka subscription for embedding topic %s with group ID %s", topic, groupID)
	indexName := getIndexName(resource.ID, buildTaskInfo.ID)

	// Message processing loop
	retryInterval := interfaces.BUILD_TASK_RETRY_INTERVAL * time.Second
	totalProcessed := buildTaskInfo.VectorizedCount
	// 重试耗尽仍失败的文档：完成前补扫一轮，仍失败则写入 error_msg
	// （仅会话内记录；worker 中途崩溃时这些文档的位点已提交，靠全量重跑恢复）
	failedDocIDs := []string{}
	lastUpdateTime := time.Now()
	updateInterval := 30 * time.Second // embedding速度慢，至少每30秒更新一次
	consecutiveReadErrs := 0           // 连续非超时读错误计数，达到上限放弃本轮交给 asynq 重试
	for {
		// Check task status before each iteration
		taskStatus, err := eh.taskAccess.GetStatus(ctx, buildTaskInfo.ID)
		if err != nil {
			logger.Errorf("Failed to get task status: %v", err)
			eh.pause(retryInterval)
			continue
		}

		// Handle stopping status
		if taskStatus == interfaces.BuildTaskStatusStopping {
			// Task is stopping, exit the loop
			logger.Infof("Task %s is stopping, exiting...", buildTaskInfo.ID)
			// Update task status to stopped
			err := eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"status": interfaces.BuildTaskStatusStopped, "vectorizedCount": totalProcessed})
			if err != nil {
				return fmt.Errorf("update build task status failed: %w", err)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			// context canceled(eg: process stopped by SIGTERM), exit the loop
			logger.Infof("Kafka subscription context canceled, exiting")
			// 最后一次更新任务状态
			_ = eh.taskAccess.UpdateStatus(context.Background(), buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
			// 必须返回错误：返回 nil 会让 asynq 把任务标记成功，重启后不再投递，
			// 任务状态永久停在 running（界面"构建中"冻结），只能人工 stop→start 救活
			return ctx.Err()
		default:
			// 创建带超时的上下文，避免ReadMessage一直阻塞
			timeoutCtx, cancel := context.WithTimeout(context.Background(), updateInterval)

			// Read message from Kafka
			msg, err := eh.kafkaAccess.ReadMessage(timeoutCtx, reader)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					consecutiveReadErrs = 0
					// 超时，检查是否需要更新任务状态
					if totalProcessed > buildTaskInfo.VectorizedCount && time.Since(lastUpdateTime) > updateInterval {
						_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
						buildTaskInfo.VectorizedCount = totalProcessed
						lastUpdateTime = time.Now()
					}
				} else {
					logger.Errorf("Embedding task Failed to read message from Kafka: %v", err)
					// 消费组协调连接死亡（broker 重启/rebalance）后读取永远失败，
					// 原地重试只会让任务永久冻结：放弃本轮，交给 asynq 重试重建
					// reader 与消费组会话，从已提交位点续读
					consecutiveReadErrs++
					if consecutiveReadErrs >= embeddingReadMaxConsecutiveErrors {
						_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
						return fmt.Errorf("read message from kafka: %w", err)
					}
					eh.pause(retryInterval)
				}
				continue
			}
			consecutiveReadErrs = 0

			// Parse Kafka message to extract document ID
			var messageData map[string]any
			if err := sonic.Unmarshal(msg.Value, &messageData); err != nil {
				// 消息畸形，重试无意义：提交跳过，避免后续位点提交把它悄悄盖掉
				logger.Errorf("Failed to unmarshal message value: %v", err)
				_ = eh.kafkaAccess.CommitMessages(ctx, reader, msg)
				continue
			}

			// Get document ID from message
			docID, ok := messageData["document_id"].(string)
			if !ok || docID == "" {
				logger.Errorf("Invalid document ID in message")
				_ = eh.kafkaAccess.CommitMessages(ctx, reader, msg)
				continue
			}

			// 结束哨兵：同步侧已发完全部文档，先补扫失败文档，再收尾
			if docID == interfaces.EmptyDocumentID {
				stillFailed := []string{}
				for _, failedID := range failedDocIDs {
					if err := eh.vectorizeDoc(ctx, indexName, failedID, buildTaskInfo.EmbeddingModel, embeddingFields); err != nil {
						logger.Errorf("Vectorize document %s failed in final sweep: %v", failedID, err)
						stillFailed = append(stillFailed, failedID)
					} else {
						totalProcessed++
					}
				}

				// 索引名落账持久失败则不提交哨兵，整个任务交给 asynq 重试：
				// 重启后从最后提交位点续读，哨兵会重新投递
				if err := updateResourceIndexName(ctx, resource, eh.resAccess, eh.ds, indexName); err != nil {
					logger.Errorf("Failed to update resource index name: %v", err)
					return fmt.Errorf("update resource index name: %w", err)
				}

				// 哨兵到达说明同步侧已发完、且组内已消费全部文档消息。
				// 同任务可能短暂存在两个消费者（asynq 重投的旧实例 + 新一轮入队的实例），
				// 单分区下旧实例抢走文档、新实例只读到哨兵，内存计数只覆盖自己的切片；
				// 以最新 synced - 已知失败 为下限对齐，避免完成态写出 0 这类假计数
				finalCount := totalProcessed
				if fresh, err := eh.taskAccess.GetByID(ctx, buildTaskInfo.ID); err == nil && fresh != nil {
					if c := fresh.SyncedCount - int64(len(stillFailed)); c > finalCount {
						logger.Infof("Embedding count for task %s aligned to synced: local=%d, final=%d (split consumers suspected)", buildTaskInfo.ID, totalProcessed, c)
						finalCount = c
					}
				}

				updates := map[string]interface{}{
					"status":          interfaces.BuildTaskStatusCompleted,
					"vectorizedCount": finalCount,
				}
				// 重试耗尽的文档如实记录：完成态但向量不全时，error_msg 说明缺了哪些
				if len(stillFailed) > 0 {
					updates["errorMsg"] = formatVectorizeFailures(stillFailed)
				}
				// 必须同时回写最终计数：常规回写有 30 秒批量窗口，
				// 不在这里 flush 会丢最后一个窗口的进度（短任务界面会停在 0%）
				if err := eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, updates); err != nil {
					logger.Errorf("update build task status to completed failed: task %s, %v", buildTaskInfo.ID, err)
				}

				// 哨兵提交失败会在消费组留下 LAG 并触发日后重投，必须留痕
				if err := eh.kafkaAccess.CommitMessages(ctx, reader, msg); err != nil {
					logger.Errorf("Failed to commit end sentinel for task %s: %v", buildTaskInfo.ID, err)
				}
				logger.Infof("Embedding finished for task %s: %d processed, %d failed", buildTaskInfo.ID, finalCount, len(stillFailed))
				return nil
			}

			// 单文档带重试：嵌入服务限流等瞬时错误最常见。
			// 重试耗尽则记入失败清单并照常提交位点——原先的 sleep+continue 看似会重试，
			// 实际 reader 已前移，后续消息提交位点时把失败文档悄悄盖掉，向量永久缺失且无痕迹
			var vErr error
			for attempt := 1; attempt <= embeddingDocMaxAttempts; attempt++ {
				if vErr = eh.vectorizeDoc(ctx, indexName, docID, buildTaskInfo.EmbeddingModel, embeddingFields); vErr == nil {
					break
				}
				logger.Errorf("Vectorize document %s attempt %d/%d failed: %v", docID, attempt, embeddingDocMaxAttempts, vErr)
				if attempt < embeddingDocMaxAttempts {
					eh.pause(retryInterval)
				}
			}
			if vErr != nil {
				failedDocIDs = append(failedDocIDs, docID)
			} else {
				totalProcessed++
			}

			// 批量更新任务状态
			if time.Since(lastUpdateTime) > updateInterval {
				_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
				lastUpdateTime = time.Now()
			}

			// Commit the message to avoid reprocessing
			if err := eh.kafkaAccess.CommitMessages(ctx, reader, msg); err != nil {
				logger.Errorf("Failed to commit message: %v", err)
			}
		}
	}
}

// 单文档向量化的最大尝试次数（含首次）；超过后记入失败清单，完成前补扫一轮
const embeddingDocMaxAttempts = 3

// 连续非超时读错误达到该次数即放弃本轮执行：消费组协调连接一旦死亡，
// 旧 reader 上的读写永远失败，必须由 asynq 重试重建会话
const embeddingReadMaxConsecutiveErrors = 3

// vectorizeDoc 对单个文档执行取数→嵌入→写回，返回错误表示本次尝试整体失败、可重试
func (eh *embeddingHandler) vectorizeDoc(ctx context.Context, indexName, docID, model string, embeddingFields []string) error {
	document, err := eh.ds.GetDocument(ctx, indexName, docID)
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}

	fields := []string{}
	words := []string{}
	for _, field := range embeddingFields {
		if value, exists := document[field]; exists {
			if text, ok := value.(string); ok && text != "" {
				fields = append(fields, field)
				words = append(words, text)
			}
		}
	}
	// 源字段全为空的文档没有可嵌入文本，视为成功：
	// 分母（synced_count）包含它们，不计数则进度永远到不了 100%
	if len(words) == 0 {
		return nil
	}

	vectorResp, err := eh.mfa.GetVector(ctx, model, words)
	if err != nil {
		return fmt.Errorf("get vector: %w", err)
	}
	if len(vectorResp) != len(words) {
		return fmt.Errorf("get vector: got %d vectors for %d texts", len(vectorResp), len(words))
	}

	updateDoc := make(map[string]any)
	for i, field := range fields {
		if resp := vectorResp[i]; resp.Vector != nil {
			updateDoc[field+"_vector"] = resp.Vector
		}
	}
	if len(updateDoc) == 0 {
		return nil
	}

	updateReq := map[string]any{
		"id":       docID,
		"document": updateDoc,
	}
	if _, err := eh.ds.UpsertDocuments(ctx, indexName, []map[string]any{updateReq}); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}
	return nil
}

// formatVectorizeFailures 生成完成态下向量缺失的说明，ID 列表截断避免撑爆 error_msg
func formatVectorizeFailures(failed []string) string {
	const maxListed = 20
	listed := failed
	if len(listed) > maxListed {
		listed = listed[:maxListed]
	}
	msg := fmt.Sprintf("vectorization failed for %d documents: %s", len(failed), strings.Join(listed, ","))
	if len(failed) > maxListed {
		msg += fmt.Sprintf(" ... and %d more", len(failed)-maxListed)
	}
	return msg
}
