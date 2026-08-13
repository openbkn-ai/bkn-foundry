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
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/segmentio/kafka-go"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/build_task"
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
	model_factory "vega-backend/logics/model_factory"
	"vega-backend/logics/resource"
)

// embeddingWorker handles embedding tasks.
type embeddingWorker struct {
	appSetting  *common.AppSetting
	bts         interfaces.BuildTaskService
	cs          interfaces.CatalogService
	kafkaAccess interfaces.KafkaAccess
	lim         interfaces.LocalIndexManager
	mfs         interfaces.ModelFactoryService
	rs          interfaces.ResourceService
	sleep       func(time.Duration) // 重试等待，测试中注入空实现避免真实 sleep
}

// pause 等待指定时长；未注入时使用 time.Sleep
func (ew *embeddingWorker) pause(d time.Duration) {
	if ew.sleep != nil {
		ew.sleep(d)
		return
	}
	time.Sleep(d)
}

// NewEmbeddingBuildWorker creates a new embedding worker.
func NewEmbeddingBuildWorker(appSetting *common.AppSetting) *embeddingWorker {
	rs := resource.NewResourceService(appSetting)
	return &embeddingWorker{
		appSetting:  appSetting,
		bts:         build_task.NewBuildTaskService(appSetting, rs),
		cs:          catalog.NewCatalogService(appSetting),
		kafkaAccess: logics.KA,
		lim:         local_index.NewLocalIndexManager(appSetting),
		mfs:         model_factory.NewModelFactoryService(appSetting),
		rs:          rs,
	}
}

// Run executes the embedding phase for one persisted build task.
func (ew *embeddingWorker) Run(ctx context.Context, taskID string) error {
	buildTaskInfo, err := ew.bts.InternalGetByID(ctx, taskID)
	if err != nil {
		runErr := fmt.Errorf("get build task failed: %w", err)
		if _, updateErr := ew.bts.InternalMarkFailed(ctx, taskID, runErr.Error()); updateErr != nil {
			logger.Errorf("Mark build task failed after embedding task lookup error: id=%s, error=%v", taskID, updateErr)
		}
		return runErr
	}
	if buildTaskInfo == nil {
		// Task not found, return nil
		return nil
	}
	if isBuildTaskTerminal(buildTaskInfo.Status) {
		logger.Infof("Task %s is %s, skip embedding", taskID, buildTaskInfo.Status)
		return nil
	}
	// 异步任务无原始请求上下文，以任务创建者身份执行下游权限检查
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, buildTaskInfo.Creator)
	logger.Infof("Starting embedding for task: %s, resource: %s", taskID, buildTaskInfo.ResourceID)

	// Get resource info
	resource, err := ew.rs.InternalGetByID(ctx, buildTaskInfo.ResourceID)
	if err != nil {
		logger.Errorf("Failed to get resource for task %s: %v", taskID, err)
		if _, updateErr := ew.bts.InternalMarkFailed(ctx, taskID, err.Error()); updateErr != nil {
			logger.Errorf("Mark build task failed after resource lookup error: id=%s, error=%v", taskID, updateErr)
		}
		return err
	}
	if resource == nil {
		logger.Errorf("Resource not found for task %s, resourceID: %s", taskID, buildTaskInfo.ResourceID)
		if err := cancelBuildTaskForDeletedParent(ctx, ew.bts, taskID, "resource deleted"); err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}

	catalogInfo, err := ew.cs.InternalGetByID(ctx, resource.CatalogID, false)
	if err != nil {
		if isNotFoundError(err) {
			if updateErr := cancelBuildTaskForDeletedParent(ctx, ew.bts, taskID, "catalog deleted"); updateErr != nil {
				return fmt.Errorf("update build task status failed: %w", updateErr)
			}
			return nil
		}
		err = fmt.Errorf("get catalog failed: %w", err)
		if _, updateErr := ew.bts.InternalMarkFailed(ctx, taskID, err.Error()); updateErr != nil {
			logger.Errorf("Mark build task failed after catalog lookup error: id=%s, error=%v", taskID, updateErr)
		}
		return err
	}
	if !catalogInfo.Enabled {
		if _, err := ew.bts.InternalMarkFailed(ctx, taskID, "catalog is disabled"); err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return nil
	}

	// Execute embedding
	embed_err := ew.executeEmbedding(ctx, resource, buildTaskInfo)
	logger.Infof("executeEmbedding completed")
	if embed_err != nil {
		_, err = ew.bts.InternalMarkFailed(ctx, taskID, embed_err.Error())
		if err != nil {
			return fmt.Errorf("update build task status failed: %w", err)
		}
		return embed_err
	}

	logger.Infof("Embedding completed for task: %s, resource: %s", taskID, buildTaskInfo.ResourceID)
	return nil
}

// executeEmbedding executes the embedding logic
func (ew *embeddingWorker) executeEmbedding(ctx context.Context, resource *interfaces.Resource, buildTaskInfo *interfaces.BuildTask) error {
	embeddingConfig := buildTaskEmbeddingConfig(buildTaskInfo)

	// Use the connector name as the Kafka topic prefix
	topic := getEmbeddingTopic(resource.ID, buildTaskInfo.ID)
	groupID := fmt.Sprintf("%s-embedding-%s", interfaces.BUILD_PREFIX, resource.ID)

	// Create Kafka topic if it doesn't exist
	if err := ew.kafkaAccess.CreateTopic(ctx, topic); err != nil {
		return fmt.Errorf("failed to create Kafka topic %s: %w", topic, err)
	}

	// Create Kafka reader
	reader, err := ew.kafkaAccess.NewReader(ctx, topic, groupID)
	if err != nil {
		return fmt.Errorf("failed to create Kafka reader for topic %s: %w", topic, err)
	}
	defer ew.kafkaAccess.CloseReader(reader)

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
	consecutiveReadErrs := 0           // Consecutive read errors are bounded before the local worker fails the task.
	consecutiveCommitErrs := 0         // Consecutive commit errors are bounded before the local worker fails the task.
	lastMessageTime := time.Now()
	for {
		// Check task status before each iteration
		taskStatus, err := ew.bts.InternalGetStatus(ctx, buildTaskInfo.ID)
		if err != nil {
			logger.Errorf("Failed to get task status: %v", err)
			ew.pause(retryInterval)
			continue
		}
		if isBuildTaskTerminal(taskStatus) {
			logger.Infof("Task %s is %s, stop embedding", buildTaskInfo.ID, taskStatus)
			return nil
		}

		// Handle stopping status
		if taskStatus == interfaces.BuildTaskStatusStopping {
			// Task is stopping, exit the loop
			logger.Infof("Task %s is stopping, exiting...", buildTaskInfo.ID)
			// Update task status to stopped
			progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
			if _, err := ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress); err != nil {
				return fmt.Errorf("update build task progress failed: %w", err)
			}
			_, err := ew.bts.InternalMarkStopped(ctx, buildTaskInfo.ID)
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
			progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
			_, _ = ew.bts.InternalSetProgress(context.Background(), nil, buildTaskInfo.ID, progress)
			// Return the cancellation so the local worker does not treat an interrupted phase as successful.
			return ctx.Err()
		default:
			// 创建带超时的上下文，避免ReadMessage一直阻塞
			timeoutCtx, cancel := context.WithTimeout(context.Background(), updateInterval)

			// Read message from Kafka
			msg, err := ew.kafkaAccess.ReadMessage(timeoutCtx, reader)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					consecutiveReadErrs = 0
					// 批量模式空闲看门狗：同步侧早已发完（含哨兵），长时间一条消息都
					// 读不到说明消费组会话假死（分区被死实例占着/会话丢失但不报错）。
					// 重建会话从已提交位点续读；流式模式空闲是常态，不适用
					if buildTaskInfo.Mode == interfaces.BuildTaskModeBatch && time.Since(lastMessageTime) > embeddingIdleRebuildAfter {
						progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
						_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
						return fmt.Errorf("no message for %s on batch task, rebuilding consumer session", embeddingIdleRebuildAfter)
					}
					// 超时，检查是否需要更新任务状态
					if totalProcessed > buildTaskInfo.VectorizedCount && time.Since(lastUpdateTime) > updateInterval {
						progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
						_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
						buildTaskInfo.VectorizedCount = totalProcessed
						lastUpdateTime = time.Now()
					}
				} else {
					logger.Errorf("Embedding task Failed to read message from Kafka: %v", err)
					// 消费组协调连接死亡（broker 重启/rebalance）后读取永远失败，
					// Repeated read failures indicate that this local execution can no longer make progress.
					// reader 与消费组会话，从已提交位点续读
					consecutiveReadErrs++
					if consecutiveReadErrs >= embeddingKafkaMaxConsecutiveErrors {
						progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
						_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
						return fmt.Errorf("read message from kafka: %w", err)
					}
					ew.pause(retryInterval)
				}
				continue
			}
			consecutiveReadErrs = 0
			lastMessageTime = time.Now()

			// 解析文档 ID；畸形消息重试无意义：提交跳过，避免后续位点提交把它悄悄盖掉
			docID := extractDocID(msg.Value)
			if docID == "" {
				_ = ew.commitMessages(reader, msg)
				continue
			}

			// 结束哨兵。哨兵不可直接信任：上一轮哨兵 commit 失败会留在原位，新消费者
			// 一上来先读到旧哨兵，若立即收尾则本轮文档原封未动（线上复现：teams 重建后
			// LAG=89，向量一个没写）。先把队列排空——连续 N 次空轮询才认为干净，
			// 途中文档照常处理、多余哨兵只提交不重复收尾
			if docID == interfaces.EmptyDocumentID {
				// 触发哨兵立刻提交：Kafka 提交是绝对位点、后写覆盖，若留到收尾才提交，
				// 会把 drain 期间已推进的位点倒拨回哨兵处，下次启动整段重放、计数虚高
				if err := ew.commitMessages(reader, msg); err != nil {
					logger.Errorf("Failed to commit end sentinel for task %s: %v", buildTaskInfo.ID, err)
				}
				emptyPolls := 0
				for emptyPolls < embeddingDrainEmptyPolls {
					drainCtx, cancelDrain := context.WithTimeout(context.Background(), embeddingDrainPollTimeout)
					dmsg, derr := ew.kafkaAccess.ReadMessage(drainCtx, reader)
					cancelDrain()
					if derr != nil {
						if errors.Is(derr, context.DeadlineExceeded) {
							emptyPolls++
							continue
						}
						logger.Errorf("Drain read failed for task %s: %v", buildTaskInfo.ID, derr)
						progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
						_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
						return fmt.Errorf("drain read message from kafka: %w", derr)
					}
					emptyPolls = 0
					dDocID := extractDocID(dmsg.Value)
					if dDocID != "" && dDocID != interfaces.EmptyDocumentID {
						if err := ew.vectorizeDocWithRetry(ctx, indexName, dDocID, embeddingConfig, retryInterval); err != nil {
							failedDocIDs = append(failedDocIDs, dDocID)
						} else {
							countProcessed(dDocID)
						}
					}
					_ = ew.commitMessages(reader, dmsg)
				}

				// 排空后补扫重试耗尽的失败文档；保留一个代表性错误作为根因。
				// 整批同一原因（如模型不存在/不可达）时，最后一条即可解释全部失败——
				// 仅记 docID 列表看不出"为什么"，failure_detail 必须带上这个 cause。
				stillFailed := []string{}
				var failureCause error
				for _, failedID := range failedDocIDs {
					if err := ew.vectorizeDoc(ctx, indexName, failedID, embeddingConfig); err != nil {
						logger.Errorf("Vectorize document %s failed in final sweep: %v", failedID, err)
						stillFailed = append(stillFailed, failedID)
						failureCause = err
					} else {
						countProcessed(failedID)
					}
				}

				// Do not commit the sentinel when persisting the index name fails.
				// 重启后从最后提交位点续读，哨兵会重新投递
				if err := updateResourceIndexName(ctx, resource, ew.rs, indexName); err != nil {
					logger.Errorf("Failed to update resource index name: %v", err)
					return fmt.Errorf("update resource index name: %w", err)
				}

				// 哨兵到达说明同步侧已发完、且组内已消费全部文档消息。
				// total_count 是批量构建的权威完成数量；synced_count 可能因最后一次
				// 进度写入未落账而滞后，不能用它封顶完成态进度。
				finalSyncedCount := buildTaskInfo.SyncedCount
				finalCount := totalProcessed
				hasFreshProgress := false
				if fresh, err := ew.bts.InternalGetByID(ctx, buildTaskInfo.ID); err == nil && fresh != nil {
					hasFreshProgress = true
					finalSyncedCount = fresh.SyncedCount
					if fresh.TotalCount > 0 {
						finalSyncedCount = fresh.TotalCount
					}
				}
				if hasFreshProgress {
					targetVectorizedCount := finalSyncedCount - int64(len(stillFailed))
					if targetVectorizedCount < 0 {
						targetVectorizedCount = 0
					}
					if targetVectorizedCount > finalCount {
						logger.Infof("Embedding count for task %s aligned to completed total: local=%d, final=%d", buildTaskInfo.ID, totalProcessed, targetVectorizedCount)
						finalCount = targetVectorizedCount
					}
					if finalCount > finalSyncedCount {
						logger.Infof("Embedding count for task %s capped at completed total: local=%d, total=%d", buildTaskInfo.ID, finalCount, finalSyncedCount)
						finalCount = finalSyncedCount
					}
				}

				progress := interfaces.BuildTaskProgress{VectorizedCount: &finalCount}
				if hasFreshProgress {
					progress.SyncedCount = &finalSyncedCount
				}
				// 重试耗尽的文档如实记录到 failure_detail（与 error_msg 区分：completed 但向量不全时，
				// failure_detail 说明缺了哪些；error_msg 仅留给整任务硬失败）。显式置空以清除上一轮重建的陈旧明细。
				failureDetail := ""
				if len(stillFailed) > 0 {
					failureDetail = formatVectorizeFailures(stillFailed, failureCause)
				}
				progress.FailureDetail = &failureDetail
				// 必须同时回写最终计数：常规回写有 30 秒批量窗口，
				// 不在这里 flush 会丢最后一个窗口的进度（短任务界面会停在 0%）
				progressUpdated, err := ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
				if err != nil {
					return fmt.Errorf("update final build task progress: %w", err)
				}
				if !progressUpdated {
					return nil
				}
				completed, err := ew.bts.InternalMarkCompleted(ctx, nil, buildTaskInfo.ID)
				if err != nil {
					return fmt.Errorf("mark build task completed: %w", err)
				}
				if !completed {
					// Stop may race with the final progress flush. SetProgress accepts
					// stopping tasks so that progress is not lost, but only a running task
					// can complete. Finish the accepted stop request before returning.
					if _, err := ew.bts.InternalMarkStopped(ctx, buildTaskInfo.ID); err != nil {
						return fmt.Errorf("mark build task stopped: %w", err)
					}
					return nil
				}

				// 触发哨兵已在 drain 入口提交；这里不可再提交——会把位点倒拨回哨兵处
				logger.Infof("Embedding finished for task %s: %d processed, %d failed", buildTaskInfo.ID, finalCount, len(stillFailed))
				return nil
			}

			// 单文档带重试：嵌入服务限流等瞬时错误最常见。
			// 重试耗尽则记入失败清单并照常提交位点——原先的 sleep+continue 看似会重试，
			// 实际 reader 已前移，后续消息提交位点时把失败文档悄悄盖掉，向量永久缺失且无痕迹
			if err := ew.vectorizeDocWithRetry(ctx, indexName, docID, embeddingConfig, retryInterval); err != nil {
				failedDocIDs = append(failedDocIDs, docID)
			} else {
				countProcessed(docID)
			}

			// 批量更新任务状态
			if time.Since(lastUpdateTime) > updateInterval {
				progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
				_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
				lastUpdateTime = time.Now()
			}

			// Commit the message to avoid reprocessing
			if err := ew.commitMessages(reader, msg); err != nil {
				logger.Errorf("Failed to commit message: %v", err)
				// A dead consumer session cannot advance offsets; fail this local execution.
				// 已处理未提交的文档重放时由 per-doc 去重计数兜底
				consecutiveCommitErrs++
				if consecutiveCommitErrs >= embeddingKafkaMaxConsecutiveErrors {
					progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
					_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
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
func (ew *embeddingWorker) vectorizeDocWithRetry(ctx context.Context, indexName, docID string, embeddingConfig map[string]interfaces.BuildTaskEmbeddingConfig, retryInterval time.Duration) error {
	var vErr error
	for attempt := 1; attempt <= embeddingDocMaxAttempts; attempt++ {
		if vErr = ew.vectorizeDoc(ctx, indexName, docID, embeddingConfig); vErr == nil {
			return nil
		}
		logger.Errorf("Vectorize document %s attempt %d/%d failed: %v", docID, attempt, embeddingDocMaxAttempts, vErr)
		if attempt < embeddingDocMaxAttempts {
			ew.pause(retryInterval)
		}
	}
	return vErr
}

// A dead reader cannot recover in place. Bound consecutive read and commit failures
// so the local execution terminates instead of freezing permanently.
const embeddingKafkaMaxConsecutiveErrors = 3

// Offset commits use a bounded timeout because kafka-go may otherwise block after
// the consumer-group session dies and prevent the loop from responding to stop.
const embeddingCommitTimeout = 30 * time.Second

// 批量任务连续读不到任何消息的重建阈值（见循环内看门狗注释）
const embeddingIdleRebuildAfter = 10 * time.Minute

// commitMessages 带有界超时提交位点
func (ew *embeddingWorker) commitMessages(reader *kafka.Reader, msgs ...kafka.Message) error {
	cctx, cancel := context.WithTimeout(context.Background(), embeddingCommitTimeout)
	defer cancel()
	return ew.kafkaAccess.CommitMessages(cctx, reader, msgs...)
}

// vectorizeDoc 对单个文档执行取数→嵌入→写回，返回错误表示本次尝试整体失败、可重试
func (ew *embeddingWorker) vectorizeDoc(ctx context.Context, indexName, docID string, embeddingConfig map[string]interfaces.BuildTaskEmbeddingConfig) error {
	document, err := ew.lim.GetDocument(ctx, indexName, docID)
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}

	fieldsByModel := map[string][]string{}
	wordsByModel := map[string][]string{}
	for field, cfg := range embeddingConfig {
		if value, exists := document[field]; exists {
			if text, ok := value.(string); ok && text != "" {
				fieldsByModel[cfg.ModelID] = append(fieldsByModel[cfg.ModelID], field)
				wordsByModel[cfg.ModelID] = append(wordsByModel[cfg.ModelID], text)
			}
		}
	}
	// 源字段全为空的文档没有可嵌入文本，视为成功：
	// 分母（synced_count）包含它们，不计数则进度永远到不了 100%
	if len(wordsByModel) == 0 {
		return nil
	}

	updateDoc := make(map[string]any)
	for model, words := range wordsByModel {
		vectorResp, err := ew.mfs.GetVector(ctx, model, words)
		if err != nil {
			return fmt.Errorf("get vector: %w", err)
		}
		if len(vectorResp) != len(words) {
			return fmt.Errorf("get vector: got %d vectors for %d texts", len(vectorResp), len(words))
		}

		fields := fieldsByModel[model]
		for i, field := range fields {
			if resp := vectorResp[i]; resp.Vector != nil {
				updateDoc[field+"_vector"] = resp.Vector
			}
		}
	}
	if len(updateDoc) == 0 {
		return nil
	}

	updateReq := map[string]any{
		"id":       docID,
		"document": updateDoc,
	}
	if _, err := ew.lim.UpsertDocuments(ctx, indexName, []map[string]any{updateReq}); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}
	return nil
}

func buildTaskEmbeddingConfig(buildTask *interfaces.BuildTask) map[string]interfaces.BuildTaskEmbeddingConfig {
	config := map[string]interfaces.BuildTaskEmbeddingConfig{}
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector != nil {
			config[field] = *feature.Vector
		}
	}
	return config
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
