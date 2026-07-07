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
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/segmentio/kafka-go"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/local_index"
)

// embeddingHandler handles embedding tasks.
type embeddingHandler struct {
	appSetting  *common.AppSetting
	taskAccess  interfaces.BuildTaskAccess
	resAccess   interfaces.ResourceAccess
	lim         interfaces.LocalIndexManager
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
	return &embeddingHandler{
		appSetting:  appSetting,
		taskAccess:  logics.BTA,
		resAccess:   logics.RA,
		lim:         local_index.NewLocalIndexManager(appSetting),
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
	// 会话内已计数文档：位点倒拨/重复投递会让同一文档消息被处理多次，
	// 向量写入幂等无害，但计数会虚高出 vectorized > synced，按 docID 去重
	seenDocIDs := map[string]struct{}{}
	countProcessed := func(docID string) {
		if _, ok := seenDocIDs[docID]; !ok {
			seenDocIDs[docID] = struct{}{}
			totalProcessed++
		}
	}
	lastUpdateTime := time.Now()
	updateInterval := 30 * time.Second // embedding速度慢，至少每30秒更新一次
	consecutiveReadErrs := 0           // 连续非超时读错误计数，达到上限放弃本轮交给 asynq 重试
	consecutiveCommitErrs := 0         // 连续位点提交失败计数，达到上限放弃本轮交给 asynq 重试
	lastMessageTime := time.Now()
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
					// 批量模式空闲看门狗：同步侧早已发完（含哨兵），长时间一条消息都
					// 读不到说明消费组会话假死（分区被死实例占着/会话丢失但不报错）。
					// 重建会话从已提交位点续读；流式模式空闲是常态，不适用
					if buildTaskInfo.Mode == interfaces.BuildTaskModeBatch && time.Since(lastMessageTime) > embeddingIdleRebuildAfter {
						_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
						return fmt.Errorf("no message for %s on batch task, rebuilding consumer session", embeddingIdleRebuildAfter)
					}
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
					if consecutiveReadErrs >= embeddingKafkaMaxConsecutiveErrors {
						_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
						return fmt.Errorf("read message from kafka: %w", err)
					}
					eh.pause(retryInterval)
				}
				continue
			}
			consecutiveReadErrs = 0
			lastMessageTime = time.Now()

			// 解析文档 ID；畸形消息重试无意义：提交跳过，避免后续位点提交把它悄悄盖掉
			docID := extractDocID(msg.Value)
			if docID == "" {
				_ = eh.commitMessages(reader, msg)
				continue
			}

			// 结束哨兵。哨兵不可直接信任：上一轮哨兵 commit 失败会留在原位，新消费者
			// 一上来先读到旧哨兵，若立即收尾则本轮文档原封未动（线上复现：teams 重建后
			// LAG=89，向量一个没写）。先把队列排空——连续 N 次空轮询才认为干净，
			// 途中文档照常处理、多余哨兵只提交不重复收尾
			if docID == interfaces.EmptyDocumentID {
				// 触发哨兵立刻提交：Kafka 提交是绝对位点、后写覆盖，若留到收尾才提交，
				// 会把 drain 期间已推进的位点倒拨回哨兵处，下次启动整段重放、计数虚高
				if err := eh.commitMessages(reader, msg); err != nil {
					logger.Errorf("Failed to commit end sentinel for task %s: %v", buildTaskInfo.ID, err)
				}
				emptyPolls := 0
				for emptyPolls < embeddingDrainEmptyPolls {
					drainCtx, cancelDrain := context.WithTimeout(context.Background(), embeddingDrainPollTimeout)
					dmsg, derr := eh.kafkaAccess.ReadMessage(drainCtx, reader)
					cancelDrain()
					if derr != nil {
						if errors.Is(derr, context.DeadlineExceeded) {
							emptyPolls++
							continue
						}
						logger.Errorf("Drain read failed for task %s: %v", buildTaskInfo.ID, derr)
						_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
						return fmt.Errorf("drain read message from kafka: %w", derr)
					}
					emptyPolls = 0
					dDocID := extractDocID(dmsg.Value)
					if dDocID != "" && dDocID != interfaces.EmptyDocumentID {
						if err := eh.vectorizeDocWithRetry(ctx, indexName, dDocID, buildTaskInfo.EmbeddingModel, embeddingFields, retryInterval); err != nil {
							failedDocIDs = append(failedDocIDs, dDocID)
						} else {
							countProcessed(dDocID)
						}
					}
					_ = eh.commitMessages(reader, dmsg)
				}

				// 排空后补扫重试耗尽的失败文档；保留一个代表性错误作为根因。
				// 整批同一原因（如模型不存在/不可达）时，最后一条即可解释全部失败——
				// 仅记 docID 列表看不出"为什么"，failure_detail 必须带上这个 cause。
				stillFailed := []string{}
				var failureCause error
				for _, failedID := range failedDocIDs {
					if err := eh.vectorizeDoc(ctx, indexName, failedID, buildTaskInfo.EmbeddingModel, embeddingFields); err != nil {
						logger.Errorf("Vectorize document %s failed in final sweep: %v", failedID, err)
						stillFailed = append(stillFailed, failedID)
						failureCause = err
					} else {
						countProcessed(failedID)
					}
				}

				// 索引名落账持久失败则不提交哨兵，整个任务交给 asynq 重试：
				// 重启后从最后提交位点续读，哨兵会重新投递
				if err := updateResourceIndexName(ctx, resource, eh.resAccess, indexName); err != nil {
					logger.Errorf("Failed to update resource index name: %v", err)
					return fmt.Errorf("update resource index name: %w", err)
				}

				// 哨兵到达说明同步侧已发完、且组内已消费全部文档消息。
				// 同任务可能短暂存在两个消费者（asynq 重投的旧实例 + 新一轮入队的实例），
				// 单分区下旧实例抢走文档、新实例只读到哨兵，内存计数只覆盖自己的切片；
				// 以最新 synced - 已知失败 为下限、synced 为上限对齐：
				// 向量数不可能超过同步数，历史重放/恢复续跑造成的虚高一并封顶
				finalCount := totalProcessed
				if fresh, err := eh.taskAccess.GetByID(ctx, buildTaskInfo.ID); err == nil && fresh != nil {
					if c := fresh.SyncedCount - int64(len(stillFailed)); c > finalCount {
						logger.Infof("Embedding count for task %s aligned to synced: local=%d, final=%d (split consumers suspected)", buildTaskInfo.ID, totalProcessed, c)
						finalCount = c
					}
					if finalCount > fresh.SyncedCount {
						logger.Infof("Embedding count for task %s capped at synced: local=%d, synced=%d (replayed messages suspected)", buildTaskInfo.ID, finalCount, fresh.SyncedCount)
						finalCount = fresh.SyncedCount
					}
				}

				updates := map[string]interface{}{
					"status":          interfaces.BuildTaskStatusCompleted,
					"vectorizedCount": finalCount,
				}
				// 重试耗尽的文档如实记录到 failure_detail（与 error_msg 区分：completed 但向量不全时，
				// failure_detail 说明缺了哪些；error_msg 仅留给整任务硬失败）。显式置空以清除上一轮重建的陈旧明细。
				updates["failureDetail"] = ""
				if len(stillFailed) > 0 {
					updates["failureDetail"] = formatVectorizeFailures(stillFailed, failureCause)
				}
				// 必须同时回写最终计数：常规回写有 30 秒批量窗口，
				// 不在这里 flush 会丢最后一个窗口的进度（短任务界面会停在 0%）
				if err := eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, updates); err != nil {
					logger.Errorf("update build task status to completed failed: task %s, %v", buildTaskInfo.ID, err)
				}

				// 触发哨兵已在 drain 入口提交；这里不可再提交——会把位点倒拨回哨兵处
				logger.Infof("Embedding finished for task %s: %d processed, %d failed", buildTaskInfo.ID, finalCount, len(stillFailed))
				return nil
			}

			// 单文档带重试：嵌入服务限流等瞬时错误最常见。
			// 重试耗尽则记入失败清单并照常提交位点——原先的 sleep+continue 看似会重试，
			// 实际 reader 已前移，后续消息提交位点时把失败文档悄悄盖掉，向量永久缺失且无痕迹
			if err := eh.vectorizeDocWithRetry(ctx, indexName, docID, buildTaskInfo.EmbeddingModel, embeddingFields, retryInterval); err != nil {
				failedDocIDs = append(failedDocIDs, docID)
			} else {
				countProcessed(docID)
			}

			// 批量更新任务状态
			if time.Since(lastUpdateTime) > updateInterval {
				_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
				lastUpdateTime = time.Now()
			}

			// Commit the message to avoid reprocessing
			if err := eh.commitMessages(reader, msg); err != nil {
				logger.Errorf("Failed to commit message: %v", err)
				// 会话死亡后提交永远失败，位点不再推进：放弃本轮交给 asynq 重建会话，
				// 已处理未提交的文档重放时由 per-doc 去重计数兜底
				consecutiveCommitErrs++
				if consecutiveCommitErrs >= embeddingKafkaMaxConsecutiveErrors {
					_ = eh.taskAccess.UpdateStatus(ctx, buildTaskInfo.ID, map[string]interface{}{"vectorizedCount": totalProcessed})
					return fmt.Errorf("commit message to kafka: %w", err)
				}
			} else {
				consecutiveCommitErrs = 0
			}
		}
	}
}

// 单文档向量化的最大尝试次数（含首次）；超过后记入失败清单，完成前补扫一轮
const embeddingDocMaxAttempts = 3

// 哨兵后的排空参数：连续 N 次空轮询（每次最长等待 PollTimeout）认为队列已干净
const (
	embeddingDrainEmptyPolls  = 2
	embeddingDrainPollTimeout = 10 * time.Second
)

// extractDocID 解析嵌入消息中的 document_id；畸形消息返回空串（调用方提交跳过）
func extractDocID(value []byte) string {
	var messageData map[string]any
	if err := sonic.Unmarshal(value, &messageData); err != nil {
		logger.Errorf("Failed to unmarshal message value: %v", err)
		return ""
	}
	docID, _ := messageData["document_id"].(string)
	return docID
}

// vectorizeDocWithRetry 带有界重试的单文档向量化；返回错误表示重试已耗尽
func (eh *embeddingHandler) vectorizeDocWithRetry(ctx context.Context, indexName, docID, model string, embeddingFields []string, retryInterval time.Duration) error {
	var vErr error
	for attempt := 1; attempt <= embeddingDocMaxAttempts; attempt++ {
		if vErr = eh.vectorizeDoc(ctx, indexName, docID, model, embeddingFields); vErr == nil {
			return nil
		}
		logger.Errorf("Vectorize document %s attempt %d/%d failed: %v", docID, attempt, embeddingDocMaxAttempts, vErr)
		if attempt < embeddingDocMaxAttempts {
			eh.pause(retryInterval)
		}
	}
	return vErr
}

// 连续非超时读错误/提交失败达到该次数即放弃本轮执行：消费组协调连接一旦死亡，
// 旧 reader 上的读写永远失败，必须由 asynq 重试重建会话
const embeddingKafkaMaxConsecutiveErrors = 3

// 位点提交的有界超时：asynq 任务 ctx 无截止时间，消费组会话死亡后 kafka-go 的
// CommitMessages 会在无界 ctx 上永久阻塞，消费循环静默冻结且不响应 stop
const embeddingCommitTimeout = 30 * time.Second

// 批量任务连续读不到任何消息的重建阈值（见循环内看门狗注释）
const embeddingIdleRebuildAfter = 10 * time.Minute

// commitMessages 带有界超时提交位点
func (eh *embeddingHandler) commitMessages(reader *kafka.Reader, msgs ...kafka.Message) error {
	cctx, cancel := context.WithTimeout(context.Background(), embeddingCommitTimeout)
	defer cancel()
	return eh.kafkaAccess.CommitMessages(cctx, reader, msgs...)
}

// vectorizeDoc 对单个文档执行取数→嵌入→写回，返回错误表示本次尝试整体失败、可重试
func (eh *embeddingHandler) vectorizeDoc(ctx context.Context, indexName, docID, model string, embeddingFields []string) error {
	document, err := eh.lim.GetDocument(ctx, indexName, docID)
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
	if _, err := eh.lim.UpsertDocuments(ctx, indexName, []map[string]any{updateReq}); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}
	return nil
}

// formatVectorizeFailures 生成完成态下向量缺失的说明：先给根因（cause），再列文档 ID。
// cause 让消费方（UI/SDK）一眼看出"为什么"——整批同因失败时（模型不存在/不可达）
// 只有 ID 列表无从判断索引为何不可用。ID 列表与 cause 均截断，避免撑爆 failure_detail。
func formatVectorizeFailures(failed []string, cause error) string {
	const maxListed = 20
	const maxCauseLen = 300
	listed := failed
	if len(listed) > maxListed {
		listed = listed[:maxListed]
	}
	msg := fmt.Sprintf("vectorization failed for %d documents", len(failed))
	if cause != nil {
		causeStr := cause.Error()
		if len(causeStr) > maxCauseLen {
			causeStr = causeStr[:maxCauseLen] + "..."
		}
		msg += fmt.Sprintf(" (cause: %s)", causeStr)
	}
	msg += ": " + strings.Join(listed, ",")
	if len(failed) > maxListed {
		msg += fmt.Sprintf(" ... and %d more", len(failed)-maxListed)
	}
	return msg
}
