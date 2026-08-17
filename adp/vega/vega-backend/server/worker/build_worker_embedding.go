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
	sleep       func(time.Duration) // Retry and wait. Inject an empty implementation during the test to avoid real sleep
}

// pause waits for the specified duration and falls back to time.Sleep when no hook is injected.
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
	// Asynchronous tasks have no original request context and perform downstream permission checks as the task creator
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
	// Retry exhausted documents that still fail: Scan once before completion. If it still fails, write to error_msg
	// (Only recorded within the session; when the worker crashes in the middle, the sites of these documents have been committed and are restored by a full restart.
	failedDocIDs := []string{}
	// Counted documents within the session: Site backtracking/repeated delivery will cause the same document message to be processed multiple times
	// Vector writing to idempotence is harmless, but the count will be higher than vectorized > synced, and the docID is used to remove duplicates
	seenDocIDs := map[string]struct{}{}
	countProcessed := func(docID string) {
		if _, ok := seenDocIDs[docID]; !ok {
			seenDocIDs[docID] = struct{}{}
			totalProcessed++
		}
	}
	lastUpdateTime := time.Now()
	updateInterval := 30 * time.Second // The embedding speed is slow, updated at least once every 30 seconds
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
			// The last update of the task status
			progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
			_, _ = ew.bts.InternalSetProgress(context.Background(), nil, buildTaskInfo.ID, progress)
			// Return the cancellation so the local worker does not treat an interrupted phase as successful.
			return ctx.Err()
		default:
			// Create a context with a timeout to prevent ReadMessage from constantly blocking
			timeoutCtx, cancel := context.WithTimeout(context.Background(), updateInterval)

			// Read message from Kafka
			msg, err := ew.kafkaAccess.ReadMessage(timeoutCtx, reader)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					consecutiveReadErrs = 0
					// Batch mode idle watchdog: The synchronization side has already sent out all the messages (including sentries), and there has been a single message for a long time
					// It cannot be read. The consumer group session is falsely dead (the partition is occupied by a dead instance/the session is lost but no error is reported).
					// Rebuild the session and continue reading from the submitted site; Idle flow mode is the norm and is not applicable
					if buildTaskInfo.Mode == interfaces.BuildTaskModeBatch && time.Since(lastMessageTime) > embeddingIdleRebuildAfter {
						progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
						_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
						return fmt.Errorf("no message for %s on batch task, rebuilding consumer session", embeddingIdleRebuildAfter)
					}
					// If it times out, check if the task status needs to be updated
					if totalProcessed > buildTaskInfo.VectorizedCount && time.Since(lastUpdateTime) > updateInterval {
						progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
						_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
						buildTaskInfo.VectorizedCount = totalProcessed
						lastUpdateTime = time.Now()
					}
				} else {
					logger.Errorf("Embedding task Failed to read message from Kafka: %v", err)
					// After the consumer group coordination connection dies (broker restart /rebalance), the read always fails.
					// Repeated read failures indicate that this local execution can no longer make progress.
					// reader converses with the consumer group and continues reading from the submitted sites
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

			// Parse the document ID; Retrying abnormal messages is meaningless: Skip the submission to prevent subsequent site submissions from quietly covering it
			docID := extractDocID(msg.Value)
			if docID == "" {
				_ = ew.commitMessages(reader, msg)
				continue
			}

			// End the Sentinel. The sentinel cannot be trusted directly: If the sentinel commit fails in the previous round, it will remain in its original position and become a new consumer
			// At the beginning, I first read the old Sentinel. If I immediately wrap it up, this round of the document will remain unchanged (online reproduction: After teams was rebuilt)
			// LAG=89, none of the vectors are written. First, empty the queue - it is only considered clean after N consecutive empty polls.
			// Documents during the journey will be processed as usual, and additional sentries will only be submitted without repeated closing
			if docID == interfaces.EmptyDocumentID {
				// Trigger the sentinel to commit immediately: Kafka commits are absolute loci and postwrite overwrites. If you wait until the end to commit,
				// The points that have been advanced during the drain period will be reversed back to the sentry. The next time the entire replay is initiated, the count will be falsely high
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

				// Empty, then scan and retry the exhausted failed documents. Retain a representative error as the root cause.
				// When the entire batch has the same cause (such as the model not existing/unreachable), the last one can explain all the failures
				// Just remembering the docID list won't show "why", but failure_detail must include this "cause".
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
				// After restarting, continue reading from the last submission point, and the sentinel will resubmit
				if err := updateResourceIndexName(ctx, resource, ew.rs, indexName); err != nil {
					logger.Errorf("Failed to update resource index name: %v", err)
					return fmt.Errorf("update resource index name: %w", err)
				}

				// The arrival of the sentinel indicates that the synchronization side has completed sending and all document messages within the group have been consumed.
				// total_count is the authoritative completion quantity for batch builds; synced_count might be due to the last time
				// The progress written in has not been recorded and is lagging behind. It cannot be used to cap the progress in the completed state.
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
				// Retry exhausted documents and record them truthfully to failure_detail (distinguished from error_msg: completed but the vector is incomplete)
				// failure_detail identifies missing data; error_msg is reserved for failures of the entire task.
				// Set it explicitly to null to clear stale details from a previous rebuild.
				failureDetail := ""
				if len(stillFailed) > 0 {
					failureDetail = formatVectorizeFailures(stillFailed, failureCause)
				}
				progress.FailureDetail = &failureDetail
				// The final count must be written back simultaneously: Regular write-backs have a 30-second batch window.
				// If you don't flush here, the progress of the last window will be lost (the short task interface will stop at 0%)
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

				// The trigger sentinel has been submitted at the drain inlet. No further submission is allowed here - the site will be reversed back to the sentry
				logger.Infof("Embedding finished for task %s: %d processed, %d failed", buildTaskInfo.ID, finalCount, len(stillFailed))
				return nil
			}

			// Single document with retry: Transient errors such as rate limiting of embedded services are the most common.
			// If the retry is exhausted, it will be recorded in the failure list and the site will be submitted as usual - the original sleep+continue seems to retry.
			// The actual reader has been moved forward. When subsequent messages are submitted to the site, the failed document is quietly covered, and the vector is permanently missing without any trace
			if err := ew.vectorizeDocWithRetry(ctx, indexName, docID, embeddingConfig, retryInterval); err != nil {
				failedDocIDs = append(failedDocIDs, docID)
			} else {
				countProcessed(docID)
			}

			// Batch update the status of tasks
			if time.Since(lastUpdateTime) > updateInterval {
				progress := interfaces.BuildTaskProgress{VectorizedCount: &totalProcessed}
				_, _ = ew.bts.InternalSetProgress(ctx, nil, buildTaskInfo.ID, progress)
				lastUpdateTime = time.Now()
			}

			// Commit the message to avoid reprocessing
			if err := ew.commitMessages(reader, msg); err != nil {
				logger.Errorf("Failed to commit message: %v", err)
				// A dead consumer session cannot advance offsets; fail this local execution.
				// When replaying processed but unsubmitted documents, the per-doc deduplication count will be used as a fallback
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

// The maximum number of attempts for vectorization of a single document (including the first attempt); If exceeded, it will be recorded in the failure list. Before completion, a supplementary scan will be conducted
const embeddingDocMaxAttempts = 3

// The emptying parameter after the sentinel: N consecutive empty polls (with the longest waiting time each PollTimeout) consider the queue to be clean
const (
	embeddingDrainEmptyPolls  = 2
	embeddingDrainPollTimeout = 10 * time.Second
)

// extractDocID parses the document_id in the embedded message; The malformed message returns an empty string (the caller's submission skips)
func extractDocID(value []byte) string {
	var messageData map[string]any
	if err := sonic.Unmarshal(value, &messageData); err != nil {
		logger.Errorf("Failed to unmarshal message value: %v", err)
		return ""
	}
	docID, _ := messageData["document_id"].(string)
	return docID
}

// vectorizeDocWithRetry is a single-document vectorization with bounded retry. Returning an error indicates that retries have been exhausted
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

// The reconstruction threshold for batch tasks that cannot continuously read any messages (see the watchdog note within the loop)
const embeddingIdleRebuildAfter = 10 * time.Minute

// commitMessages has a bound timeout commit site
func (ew *embeddingWorker) commitMessages(reader *kafka.Reader, msgs ...kafka.Message) error {
	cctx, cancel := context.WithTimeout(context.Background(), embeddingCommitTimeout)
	defer cancel()
	return ew.kafkaAccess.CommitMessages(cctx, reader, msgs...)
}

// vectorizeDoc performs data retrieval → embedding → writing back on a single document. If an error is returned, it indicates that the entire attempt failed and can be retried
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
	// A document with all source fields empty and no embeddable text is considered successful
	// The denominator (synced_count) includes them. Without counting, the progress will never reach 100%
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

// Explanation of vector missing in the completed state of formatVectorizeFailures: First provide the root cause, then list the document ID.
// cause enables the consumer (UI/SDK) to immediately understand "why" - when the entire batch of the same cause fails (the model does not exist/is unreachable)
// Only the ID list cannot determine why the index is unavailable. Both the ID list and cause are truncated to prevent failure_detail from being overhauled.
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
