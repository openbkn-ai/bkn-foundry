// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/bkn_agent"
	"vega-backend/logics/catalog"
	"vega-backend/logics/resource"
	"vega-backend/logics/semantic_understanding_task"
)

var semanticUnderstandingSourceIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

const (
	semanticTaskPollInterval   = 30 * time.Second
	defaultSemanticWorkerCount = 1
)

// SemanticUnderstandingTaskWorker handles semantic-understanding execution tasks.
type SemanticUnderstandingTaskWorker struct {
	appSetting *common.AppSetting
	suts       interfaces.SemanticUnderstandingTaskService
	bas        interfaces.BknAgentService
	cs         interfaces.CatalogService
	rs         interfaces.ResourceService

	workerCount int
	queueSize   int
	queue       chan string
	mu          sync.Mutex
	inFlight    map[string]struct{}
}

// NewSemanticUnderstandingTaskWorker creates a semantic-understanding task worker.
func NewSemanticUnderstandingTaskWorker(appSetting *common.AppSetting) *SemanticUnderstandingTaskWorker {
	workerCount := defaultSemanticWorkerCount
	if appSetting != nil && appSetting.TaskWorker.SemanticWorkerCount > 0 {
		workerCount = appSetting.TaskWorker.SemanticWorkerCount
	}
	queueSize := workerCount * taskQueueSizeMultiplier
	return &SemanticUnderstandingTaskWorker{
		appSetting: appSetting,
		suts:       semantic_understanding_task.NewSemanticUnderstandingTaskService(appSetting),
		bas:        bkn_agent.NewBknAgentService(appSetting),
		cs:         catalog.NewCatalogService(appSetting),
		rs:         resource.NewResourceService(appSetting),

		workerCount: workerCount,
		queueSize:   queueSize,
		queue:       make(chan string, queueSize),
		inFlight:    make(map[string]struct{}),
	}
}

// startLoops starts the local worker pool and database producer after startup recovery succeeds.
func (sutw *SemanticUnderstandingTaskWorker) startLoops(ctx context.Context) {
	// A fixed local worker pool executes tasks instead of creating a goroutine per Task.
	for i := 0; i < sutw.workerCount; i++ {
		go sutw.runQueuedTasks(ctx)
	}
	// The producer listens for creation notifications and fallback ticks to fill the bounded queue.
	go sutw.pollTasks(ctx)
}

func (sutw *SemanticUnderstandingTaskWorker) recoverInterruptedTasks(ctx context.Context) error {
	const recoveryFailure = "semantic understanding task interrupted by service restart"
	for {
		// Always read running tasks from offset zero; the result set shrinks after each failure update.
		tasks, _, err := sutw.suts.InternalList(ctx, interfaces.SemanticUnderstandingTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{
				Limit:     sutw.queueSize,
				Sort:      interfaces.SemanticUnderstandingTaskSortCreateTime,
				Direction: interfaces.ASC_DIRECTION,
			},
			Statuses: []string{interfaces.SemanticUnderstandingTaskStatusRunning},
		})
		if err != nil {
			return fmt.Errorf("list interrupted semantic understanding tasks: %w", err)
		}
		if len(tasks) == 0 {
			return nil
		}
		for _, task := range tasks {
			if task == nil {
				return errors.New("list interrupted semantic understanding tasks returned a nil task")
			}
			changed, err := sutw.suts.MarkFailed(ctx, task.ID, recoveryFailure)
			if err != nil {
				return fmt.Errorf("mark interrupted semantic understanding task %s failed: %w", task.ID, err)
			}
			if !changed {
				return fmt.Errorf("interrupted semantic understanding task %s was not recovered", task.ID)
			}
		}
	}
}

func (sutw *SemanticUnderstandingTaskWorker) pollTasks(ctx context.Context) {
	// The producer performs an initial pending-task scan before waiting for signals.
	sutw.fillQueue(ctx)

	ticker := time.NewTicker(semanticTaskPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		// The 30-second poll is only a fallback for missed notifications and restart recovery.
		case <-ticker.C:
		// The service notifies after the create request has durably persisted the task.
		case <-sutw.suts.DispatchSignal():
		}
		sutw.fillQueue(ctx)
	}
}

func (sutw *SemanticUnderstandingTaskWorker) fillQueue(ctx context.Context) {
	// Refill in batches only after workers have drained the local waiting queue.
	if len(sutw.queue) != 0 {
		return
	}
	limit := cap(sutw.queue)
	// Normal execution prioritizes pending tasks in stable create_time, id order.
	tasks, _, err := sutw.suts.InternalList(ctx, interfaces.SemanticUnderstandingTaskQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{
			Limit:     limit,
			Sort:      interfaces.SemanticUnderstandingTaskSortCreateTime,
			Direction: interfaces.ASC_DIRECTION,
		},
		Statuses: []string{interfaces.SemanticUnderstandingTaskStatusPending},
	})
	if err != nil {
		logger.Errorf("List pending semantic understanding tasks failed: %v", err)
		return
	}

	for _, task := range tasks {
		// The database still shows pending between local enqueue and the running write.
		// inFlight prevents this process from enqueueing the same task twice in that window.
		if task == nil || !sutw.addInFlight(task.ID) {
			continue
		}
		select {
		case sutw.queue <- task.ID:
		case <-ctx.Done():
			sutw.removeInFlight(task.ID)
			return
		}
	}
}

func (sutw *SemanticUnderstandingTaskWorker) runQueuedTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case taskID := <-sutw.queue:
			sutw.runSafely(ctx, taskID)
			sutw.removeInFlight(taskID)
			// Wake the single producer after releasing a worker slot.
			sutw.suts.RequestDispatch()
		}
	}
}

func (sutw *SemanticUnderstandingTaskWorker) runSafely(ctx context.Context, taskID string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			detail := fmt.Sprintf("semantic understanding task panicked: %v", recovered)
			logger.Errorf("Run semantic understanding task panicked: id=%s, error=%v", taskID, recovered)
			if _, err := sutw.suts.MarkFailed(ctx, taskID, detail); err != nil {
				logger.Errorf("Mark semantic understanding task failed after panic: id=%s, error=%v", taskID, err)
			}
		}
	}()
	// Run reloads the task and lets the existing state guards decide whether to execute it.
	if err := sutw.Run(ctx, taskID); err != nil {
		logger.Errorf("Run semantic understanding task failed: id=%s, error=%v", taskID, err)
	}
}

func (sutw *SemanticUnderstandingTaskWorker) addInFlight(id string) bool {
	sutw.mu.Lock()
	defer sutw.mu.Unlock()
	if _, ok := sutw.inFlight[id]; ok {
		return false
	}
	sutw.inFlight[id] = struct{}{}
	return true
}

func (sutw *SemanticUnderstandingTaskWorker) removeInFlight(id string) {
	sutw.mu.Lock()
	defer sutw.mu.Unlock()
	delete(sutw.inFlight, id)
}

// Run executes a semantic-understanding task selected from the task table.
func (sutw *SemanticUnderstandingTaskWorker) Run(ctx context.Context, taskID string) error {
	logger.Infof("Starting semantic understanding task: %s", taskID)

	taskInfo, err := sutw.suts.InternalGetByID(ctx, taskID)
	if err != nil {
		logger.Errorf("Failed to get semantic understanding task %s: %v", taskID, err)
		return err
	}
	if taskInfo == nil {
		return fmt.Errorf("semantic understanding task %s not found", taskID)
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, taskInfo.Creator)

	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusFailed ||
		taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusCancelled {
		logger.Infof("Semantic understanding task already finished: id=%s, status=%s", taskInfo.ID, taskInfo.Status)
		return nil
	}
	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusCompleted && taskInfo.AppliedTime != 0 {
		logger.Infof("Semantic understanding task already applied: id=%s", taskInfo.ID)
		return nil
	}
	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusPending ||
		taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusRunning ||
		taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusCompleted {
		parentExists, err := sutw.taskParentExists(ctx, taskInfo)
		if err != nil {
			if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
				logger.Errorf("Mark semantic understanding task failed after parent lookup error: id=%s, error=%v", taskInfo.ID, updateErr)
			}
			return err
		}
		if !parentExists {
			if taskInfo.Status != interfaces.SemanticUnderstandingTaskStatusCompleted {
				if _, err := sutw.suts.MarkCancelled(ctx, taskInfo.ID, "catalog or resource deleted"); err != nil {
					return fmt.Errorf("cancel semantic understanding task after parent deletion: %w", err)
				}
			}
			logger.Infof("Semantic understanding task stopped because its parent was deleted: id=%s", taskInfo.ID)
			return nil
		}
	}
	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusCompleted {
		return sutw.applyAndMark(ctx, taskInfo)
	}

	agentTaskID := taskInfo.AgentTaskID
	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusPending {
		claimed, err := sutw.suts.InternalMarkRunning(ctx, taskInfo.ID)
		if err != nil {
			if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
				logger.Errorf("Mark semantic understanding task failed after claim error: id=%s, error=%v", taskInfo.ID, updateErr)
			}
			return err
		}
		if !claimed {
			logger.Infof("Semantic understanding task was not claimed for running: id=%s", taskInfo.ID)
			return nil
		}
	}
	if agentTaskID == "" {
		agentTaskID, err = sutw.bas.Run(ctx, taskInfo)
		if err != nil {
			if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
				logger.Errorf("Mark semantic understanding task failed after agent start error: id=%s, error=%v", taskInfo.ID, updateErr)
			}
			return err
		}

		running, err := sutw.suts.SetAgentTaskID(ctx, taskInfo.ID, agentTaskID)
		if err != nil {
			if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
				logger.Errorf("Mark semantic understanding task failed after agent task update error: id=%s, error=%v", taskInfo.ID, updateErr)
			}
			return err
		}
		if !running {
			logger.Infof("Semantic understanding task was not claimed for running: id=%s", taskInfo.ID)
			return nil
		}
	}

	agentTask, err := sutw.bas.WaitResult(ctx, agentTaskID)
	if err != nil {
		if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
			logger.Errorf("Mark semantic understanding task failed after agent wait error: id=%s, error=%v", taskInfo.ID, updateErr)
		}
		return err
	}
	if agentTask.Status == interfaces.BknAgentTaskStatusFailed {
		if _, err := sutw.suts.MarkFailed(ctx, taskInfo.ID, bknAgentFailureDetail(agentTask)); err != nil {
			return fmt.Errorf("mark semantic understanding task failed after agent failure: %w", err)
		}
		return nil
	}

	resultJSON, confidence, confidenceDetailJSON, err := parseBknAgentResult(agentTask)
	if err != nil {
		if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
			return fmt.Errorf("mark semantic understanding task failed after result parsing error: %w", updateErr)
		}
		return nil
	}
	if taskInfo.Scope == interfaces.SemanticUnderstandingTaskScopeResource {
		resultJSON, confidence, confidenceDetailJSON, err = assessResourceSemanticResultQuality(
			resultJSON, taskInfo.Input, confidenceDetailJSON, confidence,
		)
		if err != nil {
			if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
				return fmt.Errorf("mark semantic understanding task failed after quality assessment error: %w", updateErr)
			}
			return nil
		}
	}

	completed, err := sutw.suts.MarkCompleted(ctx, taskInfo.ID, resultJSON, confidence, confidenceDetailJSON)
	if err != nil {
		if _, updateErr := sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error()); updateErr != nil {
			logger.Errorf("Mark semantic understanding task failed after completion error: id=%s, error=%v", taskInfo.ID, updateErr)
		}
		return err
	}
	if !completed {
		return nil
	}
	taskInfo.ResultJSON = resultJSON
	taskInfo.Confidence = confidence
	if err := sutw.applyAndMark(ctx, taskInfo); err != nil {
		return err
	}

	logger.Infof("Semantic understanding completed for task: %s", taskID)
	return nil
}

func (sutw *SemanticUnderstandingTaskWorker) taskParentExists(
	ctx context.Context, task *interfaces.SemanticUnderstandingTask,
) (bool, error) {
	if task.Scope == interfaces.SemanticUnderstandingTaskScopeResource {
		resourceInfo, err := sutw.rs.InternalGetByID(ctx, task.ResourceID)
		if err != nil {
			return false, fmt.Errorf("get semantic understanding task resource: %w", err)
		}
		return resourceInfo != nil, nil
	}
	if task.Scope != interfaces.SemanticUnderstandingTaskScopeCatalog {
		return true, nil
	}

	_, err := sutw.cs.InternalGetByID(ctx, task.CatalogID, false)
	if err == nil {
		return true, nil
	}
	if isNotFoundError(err) {
		return false, nil
	}
	return false, fmt.Errorf("get semantic understanding task catalog: %w", err)
}

func (sutw *SemanticUnderstandingTaskWorker) applyAndMark(ctx context.Context, task *interfaces.SemanticUnderstandingTask) error {
	if logics.DB == nil {
		applyResult, err := sutw.applyResult(ctx, task, task.ResultJSON, task.Confidence, nil)
		if err != nil {
			return err
		}
		_, err = sutw.suts.MarkApplied(ctx, task.ID, applyResult.Applied, applyResult.DetailJSON)
		return err
	}

	tx, err := logics.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin semantic understanding apply transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	applyResult, err := sutw.applyResult(ctx, task, task.ResultJSON, task.Confidence, tx)
	if err != nil {
		return err
	}
	if _, err := sutw.suts.InternalMarkApplied(ctx, tx, task.ID, applyResult.Applied, applyResult.DetailJSON); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func bknAgentFailureDetail(agentTask *interfaces.BknAgentTask) string {
	if agentTask == nil {
		return "agent task failed"
	}
	if agentTask.FailureDetail != "" {
		return agentTask.FailureDetail
	}
	if agentTask.Error != "" {
		return agentTask.Error
	}
	return fmt.Sprintf("agent task %s failed", agentTask.TaskID)
}

func parseBknAgentResult(agentTask *interfaces.BknAgentTask) (string, float64, string, error) {
	if agentTask == nil {
		return "", 0, "", fmt.Errorf("agent task result is required")
	}
	result := agentTask.Result
	if len(result) == 0 {
		result = agentTask.ResultJSON
	}
	if len(result) == 0 {
		return "", 0, "", fmt.Errorf("agent task result is empty")
	}
	result, err := extractBknAgentResultJSON(result)
	if err != nil {
		return "", 0, "", err
	}

	resultObject := map[string]sonic.NoCopyRawMessage{}
	if err := sonic.Unmarshal(result, &resultObject); err != nil {
		return "", 0, "", fmt.Errorf("unmarshal agent task result failed: %w", err)
	}

	var confidence float64
	confidenceRaw, ok := resultObject["confidence"]
	if !ok {
		return "", 0, "", fmt.Errorf("agent task result missing confidence")
	}
	if err := sonic.Unmarshal(confidenceRaw, &confidence); err != nil {
		return "", 0, "", fmt.Errorf("unmarshal agent task confidence failed: %w", err)
	}
	if confidence < 0 || confidence > 1 {
		return "", 0, "", fmt.Errorf("agent task confidence must be between 0 and 1")
	}

	detail := make(map[string]sonic.NoCopyRawMessage)
	for _, key := range []string{
		"resource",
		"fields",
		"logic_views",
		"obsolete_logic_views",
		"warnings",
		"confidence_detail",
		"confidence_details",
	} {
		if value, ok := resultObject[key]; ok {
			detail[key] = value
		}
	}
	detailJSON, err := sonic.Marshal(detail)
	if err != nil {
		return "", 0, "", fmt.Errorf("marshal confidence detail failed: %w", err)
	}

	return string(result), confidence, string(detailJSON), nil
}

// assessResourceSemanticResultQuality records when an otherwise valid agent
// response has no usable field-level enhancement. It only lowers confidence
// when neither the resource nor any field contains an effective change; this
// preserves valid resource-only updates while making no-op output ineligible
// for automatic application.
func assessResourceSemanticResultQuality(resultJSON, inputJSON, confidenceDetailJSON string, confidence float64) (string, float64, string, error) {
	if strings.TrimSpace(inputJSON) == "" {
		// Tasks created before input snapshots were persisted cannot be assessed
		// reliably. Preserve their existing execution semantics.
		return resultJSON, confidence, confidenceDetailJSON, nil
	}
	var input interfaces.SemanticUnderstandingResourceAgentInput
	if unmarshalErr := sonic.Unmarshal([]byte(inputJSON), &input); unmarshalErr != nil {
		// Input is an audit snapshot, not an agent response. A malformed legacy
		// snapshot must not turn an otherwise successful agent task into failure.
		return resultJSON, confidence, confidenceDetailJSON, nil //nolint:nilerr // Malformed legacy snapshots must not fail completed tasks.
	}

	var result interfaces.SemanticUnderstandingResourceResult
	if err := sonic.Unmarshal([]byte(resultJSON), &result); err != nil {
		return "", 0, "", fmt.Errorf("unmarshal resource semantic understanding result quality input failed: %w", err)
	}

	inputFields := make(map[string]struct {
		DisplayName string
		Description string
	}, len(input.Resource.SchemaDefinition))
	for _, field := range input.Resource.SchemaDefinition {
		inputFields[field.Name] = struct {
			DisplayName string
			Description string
		}{DisplayName: field.DisplayName, Description: field.Description}
	}

	quality := interfaces.SemanticUnderstandingResourceQuality{
		ResourceEffective: strings.TrimSpace(result.Resource.DisplayName) != "" && result.Resource.DisplayName != input.Resource.Name ||
			strings.TrimSpace(result.Resource.Description) != "" && result.Resource.Description != input.Resource.Description,
		FieldTotal: len(inputFields),
	}
	for _, field := range result.Fields {
		inputField, ok := inputFields[field.Name]
		if !ok {
			continue
		}
		displayNameEffective := !isTechnicalFieldName(field.Name, field.DisplayName) && field.DisplayName != inputField.DisplayName
		descriptionEffective := strings.TrimSpace(field.Description) != "" && field.Description != inputField.Description
		if displayNameEffective || descriptionEffective {
			quality.FieldEffective++
		}
	}

	if quality.FieldTotal == 0 || quality.FieldEffective > 0 {
		return resultJSON, confidence, confidenceDetailJSON, nil
	}

	warning := "no effective field semantic enhancements: all field display names/descriptions are unchanged or invalid"
	confidenceDetailJSON, err := appendResourceSemanticQuality(confidenceDetailJSON, quality, warning)
	if err != nil {
		return "", 0, "", err
	}
	if !quality.ResourceEffective {
		confidence = 0
	}
	return resultJSON, confidence, confidenceDetailJSON, nil
}

func appendResourceSemanticQuality(payload string, quality interfaces.SemanticUnderstandingResourceQuality, warning string) (string, error) {
	object := map[string]sonic.NoCopyRawMessage{}
	if err := sonic.Unmarshal([]byte(payload), &object); err != nil {
		return "", fmt.Errorf("unmarshal resource semantic understanding quality payload failed: %w", err)
	}

	warnings := []string{}
	if rawWarnings, ok := object["warnings"]; ok {
		if err := sonic.Unmarshal(rawWarnings, &warnings); err != nil {
			return "", fmt.Errorf("unmarshal resource semantic understanding warnings failed: %w", err)
		}
	}
	warnings = append(warnings, warning)
	warningsJSON, err := sonic.Marshal(warnings)
	if err != nil {
		return "", fmt.Errorf("marshal resource semantic understanding warnings failed: %w", err)
	}
	qualityJSON, err := sonic.Marshal(quality)
	if err != nil {
		return "", fmt.Errorf("marshal resource semantic understanding quality failed: %w", err)
	}
	object["warnings"] = warningsJSON
	object["quality"] = qualityJSON
	result, err := sonic.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("marshal resource semantic understanding quality payload failed: %w", err)
	}
	return string(result), nil
}

func extractBknAgentResultJSON(result []byte) ([]byte, error) {
	start := -1
	for i, b := range result {
		if b == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("agent task result missing json object")
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(result); i++ {
		b := result[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return result[start : i+1], nil
			}
		}
	}

	return nil, fmt.Errorf("agent task result json object is incomplete")
}

func (sutw *SemanticUnderstandingTaskWorker) applyResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string, confidence float64, tx *sql.Tx) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if confidence < task.ConfidenceThreshold {
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:              "confidence_below_threshold",
			Confidence:          confidence,
			ConfidenceThreshold: task.ConfidenceThreshold,
			Scope:               task.Scope,
		})
	}
	if task.ApplyMode == interfaces.SemanticUnderstandingApplyModeDryRun {
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "dry_run",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	switch task.Scope {
	case interfaces.SemanticUnderstandingTaskScopeResource:
		return sutw.applyResourceResult(ctx, task, resultJSON, tx)
	case interfaces.SemanticUnderstandingTaskScopeCatalog:
		return sutw.applyCatalogResult(ctx, task, resultJSON, tx)
	default:
		return nil, fmt.Errorf("unsupported semantic understanding task scope: %s", task.Scope)
	}
}

func skippedApplyResult(detail interfaces.SemanticUnderstandingSkippedApplyDetail) (*interfaces.SemanticUnderstandingApplyResult, error) {
	detailBytes, err := sonic.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal semantic understanding skipped apply detail failed: %w", err)
	}
	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    false,
		DetailJSON: string(detailBytes),
	}, nil
}

func (sutw *SemanticUnderstandingTaskWorker) applyResourceResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string, tx *sql.Tx) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if task.ResourceID == "" {
		return nil, fmt.Errorf("resource_id is required for resource semantic understanding task")
	}

	var result interfaces.SemanticUnderstandingResourceResult
	if err := sonic.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("unmarshal resource semantic understanding result failed: %w", err)
	}
	if err := validateConfidence(result.Resource.Confidence, "resource.confidence"); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(result.Resource.DisplayName) > interfaces.NAME_MAX_LENGTH {
		return nil, fmt.Errorf("resource display_name exceeds max length")
	}

	resourceInfo, err := sutw.rs.GetByID(ctx, task.ResourceID)
	if err != nil {
		return nil, err
	}
	if resourceInfo == nil {
		return nil, fmt.Errorf("resource %s not found", task.ResourceID)
	}

	fieldByName := make(map[string]*interfaces.Property, len(resourceInfo.SchemaDefinition))
	for _, property := range resourceInfo.SchemaDefinition {
		if property != nil {
			fieldByName[property.Name] = property
		}
	}

	seenFields := make(map[string]struct{}, len(result.Fields))
	updatedFields := make([]string, 0)
	skippedFields := make([]string, 0)
	fieldDetails := make([]interfaces.SemanticUnderstandingFieldApplyDetail, 0, len(result.Fields))
	for _, field := range result.Fields {
		if field.Name == "" {
			skippedFields = append(skippedFields, "<empty>: missing name")
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"missing name"}})
			continue
		}
		if _, ok := seenFields[field.Name]; ok {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: duplicate", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"duplicate"}})
			continue
		}
		seenFields[field.Name] = struct{}{}

		property, ok := fieldByName[field.Name]
		if !ok {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: not found", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"not found"}})
			continue
		}
		if utf8.RuneCountInString(field.DisplayName) > interfaces.MaxLength_PropertyDisplayName {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: display_name exceeds max length", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"display_name exceeds max length"}})
			continue
		}
		// Only a missing display name is absent. Whitespace-only and
		// punctuation-only values are present but not meaningful semantic names,
		// so they must be rejected even in force mode rather than being allowed
		// to overwrite an existing display name.
		missingDisplayName := field.DisplayName == ""
		invalidDisplayName := !missingDisplayName && isTechnicalFieldName(field.Name, field.DisplayName)
		reasons := make([]string, 0, 1)
		if invalidDisplayName {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: display_name equals technical field name", field.Name))
			reasons = append(reasons, "display_name equals technical field name")
		}
		if utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: description exceeds max length", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"description exceeds max length"}})
			continue
		}
		if err := validateConfidence(field.Confidence, fmt.Sprintf("fields[%s].confidence", field.Name)); err != nil {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: invalid confidence", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"invalid confidence"}})
			continue
		}
		if field.Confidence != nil && *field.Confidence < task.ConfidenceThreshold {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: confidence below threshold", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"confidence below threshold"}})
			continue
		}

		updated := make([]string, 0, 2)
		if !invalidDisplayName && applyStringByMode(task.ApplyMode, &property.DisplayName, field.DisplayName, property.DisplayName == property.Name) {
			updated = append(updated, "display_name")
		}
		if applyStringByMode(task.ApplyMode, &property.Description, field.Description, property.Description == property.OriginalDescription) {
			updated = append(updated, "description")
		}
		status := "unchanged"
		if len(updated) > 0 {
			status = "updated"
			if len(reasons) > 0 {
				status = "partial"
			}
			updatedFields = append(updatedFields, field.Name)
		}
		fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: status, Updated: updated, Reasons: reasons})
	}

	updatedResource := make([]string, 0, 2)
	if applyStringByMode(task.ApplyMode, &resourceInfo.Name, result.Resource.DisplayName,
		resourceInfo.Name == resourceInfo.SourceIdentifier) {
		updatedResource = append(updatedResource, "name")
	}
	if applyStringByMode(task.ApplyMode, &resourceInfo.Description, result.Resource.Description,
		resourceInfo.Description == sourceOriginalDescription(resourceInfo.SourceMetadata)) {
		updatedResource = append(updatedResource, "description")
	}
	resourceUpdated := len(updatedResource) > 0
	if !resourceUpdated && len(updatedFields) == 0 {
		if len(skippedFields) > 0 {
			detailBytes, err := sonic.Marshal(interfaces.SemanticUnderstandingResourceApplyDetail{SkippedFields: skippedFields, FieldDetails: fieldDetails})
			if err != nil {
				return nil, fmt.Errorf("marshal resource semantic understanding apply detail failed: %w", err)
			}
			return &interfaces.SemanticUnderstandingApplyResult{Applied: false, DetailJSON: string(detailBytes)}, nil
		}
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "no_resource_changes",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	resourceInfo.Updater = task.Creator
	resourceInfo.UpdateTime = time.Now().UnixMilli()
	if tx != nil {
		err = sutw.rs.InternalUpdate(ctx, tx, resourceInfo)
	} else {
		err = sutw.rs.UpdateResource(ctx, resourceInfo)
	}
	if err != nil {
		return nil, err
	}

	detailBytes, err := sonic.Marshal(interfaces.SemanticUnderstandingResourceApplyDetail{
		ResourceUpdated: resourceUpdated,
		UpdatedResource: updatedResource,
		UpdatedFields:   updatedFields,
		SkippedFields:   skippedFields,
		FieldDetails:    fieldDetails,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal resource semantic understanding apply detail failed: %w", err)
	}

	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    true,
		DetailJSON: string(detailBytes),
	}, nil
}

func applyStringByMode(mode string, current *string, next string, treatCurrentAsEmpty bool) bool {
	if next == "" {
		return false
	}
	switch mode {
	case interfaces.SemanticUnderstandingApplyModeFillEmpty:
		if *current != "" && !treatCurrentAsEmpty {
			return false
		}
	case interfaces.SemanticUnderstandingApplyModeForce:
	default:
		return false
	}
	if *current == next {
		return false
	}
	*current = next
	return true
}

// isTechnicalFieldName reports whether displayName is empty or merely a formatting
// variation of the technical field name. Such a value is not a semantic enhancement
// and must never overwrite a business display name, including in force mode.
func isTechnicalFieldName(name, displayName string) bool {
	normalizedDisplayName := normalizeSemanticFieldName(displayName)
	return normalizedDisplayName == "" || normalizedDisplayName == normalizeSemanticFieldName(name)
}

func normalizeSemanticFieldName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func sourceOriginalDescription(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	description, _ := metadata["original_description"].(string)
	return description
}

func validateConfidence(confidence *float64, path string) error {
	if confidence == nil {
		return nil
	}
	if *confidence < 0 || *confidence > 1 {
		return fmt.Errorf("%s must be between 0 and 1", path)
	}
	return nil
}

func (sutw *SemanticUnderstandingTaskWorker) applyCatalogResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string, tx *sql.Tx) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if task.CatalogID == "" {
		return nil, fmt.Errorf("catalog_id is required for catalog semantic understanding task")
	}

	var result interfaces.SemanticUnderstandingCatalogResult
	if err := sonic.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("unmarshal catalog semantic understanding result failed: %w", err)
	}

	resources, err := sutw.rs.GetByCatalogID(ctx, task.CatalogID)
	if err != nil {
		return nil, err
	}
	resourceByID := make(map[string]*interfaces.Resource, len(resources))
	logicViewByID := make(map[string]*interfaces.Resource)
	sourceIdentifiers := make(map[string]struct{}, len(resources))
	for _, res := range resources {
		if res == nil {
			continue
		}
		resourceByID[res.ID] = res
		if res.SourceIdentifier != "" {
			sourceIdentifiers[res.SourceIdentifier] = struct{}{}
		}
		if res.Category == interfaces.ResourceCategoryLogicView {
			logicViewByID[res.ID] = res
		}
	}

	detail := interfaces.SemanticUnderstandingCatalogApplyDetail{}
	for i, view := range result.LogicViews {
		if err := validateConfidence(view.Confidence, fmt.Sprintf("logic_views[%d].confidence", i)); err != nil {
			return nil, err
		}
		if err := validateCatalogLogicViewOutput(view, resourceByID, logicViewByID, sourceIdentifiers); err != nil {
			return nil, err
		}

		switch view.Action {
		case "create":
			sourceIdentifiers[view.SourceIdentifier] = struct{}{}
			req := &interfaces.ResourceRequest{
				CatalogID:        task.CatalogID,
				Name:             view.Name,
				SourceIdentifier: view.SourceIdentifier,
				Description:      view.Description,
				Category:         interfaces.ResourceCategoryLogicView,
				Status:           interfaces.ResourceStatusActive,
				LogicDefinition:  view.LogicDefinition,
			}
			var created *interfaces.Resource
			if tx != nil {
				created, err = sutw.rs.InternalCreate(ctx, tx, req)
			} else {
				created, err = sutw.rs.Create(ctx, req)
			}
			if err != nil {
				return nil, err
			}
			if created != nil {
				detail.CreatedResourceIDs = append(detail.CreatedResourceIDs, created.ID)
			}
		case "update":
			current := logicViewByID[view.TargetResourceID]
			nextDescription := current.Description
			applyStringByMode(task.ApplyMode, &nextDescription, view.Description, false)
			nextLogicDefinition := current.LogicDefinition
			if task.ApplyMode == interfaces.SemanticUnderstandingApplyModeForce {
				nextLogicDefinition = view.LogicDefinition
			}
			next := &interfaces.ResourceRequest{
				ID:              current.ID,
				CatalogID:       current.CatalogID,
				Name:            current.Name,
				Tags:            current.Tags,
				Description:     nextDescription,
				Category:        current.Category,
				Status:          current.Status,
				Schema:          current.Schema,
				SourceMetadata:  current.SourceMetadata,
				IndexConfig:     current.IndexConfig,
				LogicDefinition: nextLogicDefinition,
			}
			if tx != nil {
				current.Description = nextDescription
				current.LogicDefinition = nextLogicDefinition
				current.Updater = task.Creator
				current.UpdateTime = time.Now().UnixMilli()
				err = sutw.rs.InternalUpdate(ctx, tx, current)
			} else {
				err = sutw.rs.Update(ctx, current, next)
			}
			if err != nil {
				return nil, err
			}
			detail.UpdatedResourceIDs = append(detail.UpdatedResourceIDs, current.ID)
		}
	}

	for i, obsolete := range result.ObsoleteLogicViews {
		if err := validateConfidence(obsolete.Confidence, fmt.Sprintf("obsolete_logic_views[%d].confidence", i)); err != nil {
			return nil, err
		}
		if obsolete.TargetResourceID == "" {
			return nil, fmt.Errorf("obsolete_logic_views[%d].target_resource_id is required", i)
		}
		if _, ok := logicViewByID[obsolete.TargetResourceID]; !ok {
			return nil, fmt.Errorf("obsolete logic view %s does not exist in catalog input", obsolete.TargetResourceID)
		}
		if tx != nil {
			err = sutw.rs.InternalUpdateStatus(ctx, tx, obsolete.TargetResourceID, interfaces.ResourceStatusStale, obsolete.Reason)
		} else {
			err = sutw.rs.UpdateStatus(ctx, obsolete.TargetResourceID, interfaces.ResourceStatusStale, obsolete.Reason)
		}
		if err != nil {
			return nil, err
		}
		detail.StaledResourceIDs = append(detail.StaledResourceIDs, obsolete.TargetResourceID)
	}

	if len(detail.CreatedResourceIDs) == 0 && len(detail.UpdatedResourceIDs) == 0 && len(detail.StaledResourceIDs) == 0 {
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "no_catalog_changes",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	detailBytes, err := sonic.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal catalog semantic understanding apply detail failed: %w", err)
	}
	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    true,
		DetailJSON: string(detailBytes),
	}, nil
}

func validateCatalogLogicViewOutput(view interfaces.SemanticUnderstandingCatalogLogicView, resourceByID map[string]*interfaces.Resource, logicViewByID map[string]*interfaces.Resource, sourceIdentifiers map[string]struct{}) error {
	switch view.Action {
	case "create":
		if view.TargetResourceID != "" {
			return fmt.Errorf("target_resource_id must be empty when creating logic view")
		}
		if view.Name == "" {
			return fmt.Errorf("logic view name is required when creating logic view")
		}
		if !semanticUnderstandingSourceIdentifierPattern.MatchString(view.SourceIdentifier) {
			return fmt.Errorf("source_identifier must be lower snake_case when creating logic view")
		}
		if _, exists := sourceIdentifiers[view.SourceIdentifier]; exists {
			return fmt.Errorf("source_identifier %s already exists in catalog input", view.SourceIdentifier)
		}
	case "update":
		if view.TargetResourceID == "" {
			return fmt.Errorf("target_resource_id is required when updating logic view")
		}
		if _, ok := logicViewByID[view.TargetResourceID]; !ok {
			return fmt.Errorf("logic view %s does not exist in catalog input", view.TargetResourceID)
		}
	default:
		return fmt.Errorf("unsupported logic view action: %s", view.Action)
	}
	if len(view.LogicDefinition) == 0 {
		return fmt.Errorf("logic_definition is required for logic view action %s", view.Action)
	}
	for _, sourceResourceID := range view.SourceResources {
		if _, ok := resourceByID[sourceResourceID]; !ok {
			return fmt.Errorf("source resource %s does not exist in catalog input", sourceResourceID)
		}
	}
	return nil
}
