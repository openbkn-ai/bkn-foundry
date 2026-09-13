// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_logs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"

	"ontology-query/common"
	"ontology-query/interfaces"
	"ontology-query/logics"
)

var (
	alsOnce    sync.Once
	alsService interfaces.ActionLogsService
)

type actionLogsService struct {
	appSetting *common.AppSetting
	osAccess   interfaces.OpenSearchAccess
}

// NewActionLogsService creates a singleton instance of ActionLogsService
func NewActionLogsService(appSetting *common.AppSetting) interfaces.ActionLogsService {
	alsOnce.Do(func() {
		alsService = &actionLogsService{
			appSetting: appSetting,
			osAccess:   logics.OSA,
		}
	})
	return alsService
}

// CreateExecution creates a new execution record in OpenSearch
func (s *actionLogsService) CreateExecution(ctx context.Context, exec *interfaces.ActionExecution) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateExecution")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(exec.ID),
		attr.Key("kn_id").String(exec.KNID),
		attr.Key("action_type_id").String(exec.ActionTypeID),
	)

	indexName := interfaces.GetActionExecutionIndex(exec.KNID)

	// Ensure index exists
	if err := s.ensureIndexExists(ctx, indexName); err != nil {
		logger.Errorf("Failed to ensure index exists: %v", err)
		return fmt.Errorf("failed to ensure index exists: %w", err)
	}

	// Insert the execution record
	if err := s.osAccess.InsertData(ctx, indexName, exec.ID, exec); err != nil {
		logger.Errorf("Failed to insert execution record: %v", err)
		return fmt.Errorf("failed to insert execution record: %w", err)
	}

	logger.Debugf("Created execution record: %s", exec.ID)
	return nil
}

// Painless sources for the conditional writes. Each one decides on the latest version of
// the document inside OpenSearch, so no writer acts on a status it read earlier.
const (
	// Only a pending execution may start running.
	markRunningScript = "if (ctx._source.status == params.from) { ctx._source.status = params.to } else { ctx.op = 'noop' }"

	// Only a pending or running execution may be cancelled. The results are left alone:
	// the executor keeps recording what it already ran.
	cancelScript = "if (params.cancellable.contains(ctx._source.status)) { " +
		"ctx._source.status = params.cancelled; ctx._source.end_time = params.end_time; " +
		"if (ctx._source.start_time != null && ctx._source.start_time > 0) { ctx._source.duration_ms = params.end_time - ctx._source.start_time } " +
		"} else { ctx.op = 'noop' }"

	// The terminal write. A cancel that landed while the execution was still running wins
	// over the status the executor computed; counters, results and timing still record
	// what actually ran.
	finishScript = "if (ctx._source.status != params.cancelled) { ctx._source.status = params.status } " +
		"ctx._source.success_count = params.success_count; ctx._source.failed_count = params.failed_count; " +
		"ctx._source.results = params.results; ctx._source.end_time = params.end_time; ctx._source.duration_ms = params.duration_ms;"
)

func painlessUpdate(source string, params map[string]any) map[string]any {
	return map[string]any{
		"script": map[string]any{
			"lang":   "painless",
			"source": source,
			"params": params,
		},
	}
}

// MarkExecutionRunning moves a pending execution to running and leaves any other status alone.
func (s *actionLogsService) MarkExecutionRunning(ctx context.Context, knID, execID string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "MarkExecutionRunning")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(execID),
		attr.Key("kn_id").String(knID),
	)

	result, err := s.osAccess.UpdateData(ctx, interfaces.GetActionExecutionIndex(knID), execID, painlessUpdate(markRunningScript, map[string]any{
		"from": interfaces.ExecutionStatusPending,
		"to":   interfaces.ExecutionStatusRunning,
	}))
	if err != nil {
		return fmt.Errorf("failed to mark execution %s running: %w", execID, err)
	}
	if result == interfaces.UpdateResultNoop {
		logger.Infof("Execution %s is no longer pending, status left unchanged", execID)
	}
	return nil
}

// UpdateExecutionProgress merges the counters and results into the execution without
// reading it first and without touching its status.
func (s *actionLogsService) UpdateExecutionProgress(ctx context.Context, knID, execID string, progress *interfaces.ExecutionProgress) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateExecutionProgress")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(execID),
		attr.Key("kn_id").String(knID),
	)

	_, err := s.osAccess.UpdateData(ctx, interfaces.GetActionExecutionIndex(knID), execID, map[string]any{
		"doc": map[string]any{
			"success_count": progress.SuccessCount,
			"failed_count":  progress.FailedCount,
			"results":       nonNilResults(progress.Results),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update execution %s progress: %w", execID, err)
	}
	return nil
}

// FinishExecution writes the terminal record; a cancelled execution stays cancelled.
func (s *actionLogsService) FinishExecution(ctx context.Context, knID, execID string, outcome *interfaces.ExecutionOutcome) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "FinishExecution")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(execID),
		attr.Key("kn_id").String(knID),
		attr.Key("status").String(outcome.Status),
	)

	_, err := s.osAccess.UpdateData(ctx, interfaces.GetActionExecutionIndex(knID), execID, painlessUpdate(finishScript, map[string]any{
		"cancelled":     interfaces.ExecutionStatusCancelled,
		"status":        outcome.Status,
		"success_count": outcome.SuccessCount,
		"failed_count":  outcome.FailedCount,
		"results":       nonNilResults(outcome.Results),
		"end_time":      outcome.EndTime,
		"duration_ms":   outcome.DurationMs,
	}))
	if err != nil {
		return fmt.Errorf("failed to finish execution %s: %w", execID, err)
	}
	return nil
}

// GetExecutionStatus reads only the status field of an execution.
func (s *actionLogsService) GetExecutionStatus(ctx context.Context, knID, execID string) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetExecutionStatus")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(execID),
		attr.Key("kn_id").String(knID),
	)

	exec, err := s.searchExecution(ctx, knID, execID, map[string]any{"includes": []string{"status"}})
	if err != nil {
		return "", err
	}
	return exec.Status, nil
}

// searchExecution fetches one execution document. source, when non-nil, is the _source
// filter, so callers that need no per-instance results never read them.
func (s *actionLogsService) searchExecution(ctx context.Context, knID, execID string, source map[string]any) (*interfaces.ActionExecution, error) {
	indexName := interfaces.GetActionExecutionIndex(knID)

	// A missing index must not hit SearchData (index_not_found_exception).
	exists, err := s.osAccess.IndexExists(ctx, indexName)
	if err != nil {
		logger.Errorf("Failed to check action execution index: %v", err)
		return nil, fmt.Errorf("failed to search execution: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("execution not found: %s，because the index[%s] does not exist", execID, indexName)
	}

	osQuery := map[string]any{
		"query": map[string]any{
			"term": map[string]any{
				"id": execID,
			},
		},
		"size": 1,
	}
	if source != nil {
		osQuery["_source"] = source
	}

	hits, err := s.osAccess.SearchData(ctx, indexName, osQuery)
	if err != nil {
		logger.Errorf("Failed to search execution: %v", err)
		return nil, fmt.Errorf("failed to search execution: %w", err)
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("execution not found: %s", execID)
	}

	exec, err := mapToActionExecution(hits[0].Source)
	if err != nil {
		return nil, fmt.Errorf("failed to parse execution: %w", err)
	}
	return exec, nil
}

// nonNilResults keeps an empty result list an empty array in the stored document.
func nonNilResults(results []interfaces.ObjectExecutionResult) []interfaces.ObjectExecutionResult {
	if results == nil {
		return []interfaces.ObjectExecutionResult{}
	}
	return results
}

// GetExecution retrieves a single execution by ID with optional results pagination
func (s *actionLogsService) GetExecution(ctx context.Context, query *interfaces.ActionLogDetailQuery) (*interfaces.ActionExecution, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetExecution")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(query.LogID),
		attr.Key("kn_id").String(query.KNID),
	)

	exec, err := s.searchExecution(ctx, query.KNID, query.LogID, nil)
	if err != nil {
		return nil, err
	}

	// Apply results pagination and filtering
	allResults := exec.Results
	resultsTotal := len(allResults)

	// Filter by status if specified
	if query.ResultsStatus != "" {
		filteredResults := make([]interfaces.ObjectExecutionResult, 0)
		for _, r := range allResults {
			if r.Status == query.ResultsStatus {
				filteredResults = append(filteredResults, r)
			}
		}
		allResults = filteredResults
		resultsTotal = len(allResults)
	}

	// Apply pagination
	resultsLimit := query.ResultsLimit
	if resultsLimit <= 0 {
		resultsLimit = 100
	}
	if resultsLimit > 1000 {
		resultsLimit = 1000
	}

	resultsOffset := query.ResultsOffset
	if resultsOffset < 0 {
		resultsOffset = 0
	}

	// Slice the results based on pagination
	startIdx := resultsOffset
	endIdx := resultsOffset + resultsLimit

	if startIdx >= len(allResults) {
		exec.Results = []interfaces.ObjectExecutionResult{}
	} else {
		if endIdx > len(allResults) {
			endIdx = len(allResults)
		}
		exec.Results = allResults[startIdx:endIdx]
	}

	// Set pagination metadata
	exec.ResultsTotal = resultsTotal
	exec.ResultsOffset = resultsOffset
	exec.ResultsLimit = resultsLimit

	return exec, nil
}

// QueryExecutions queries executions based on filter criteria
func (s *actionLogsService) QueryExecutions(ctx context.Context, query *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "QueryExecutions")
	defer span.End()

	span.SetAttributes(attr.Key("kn_id").String(query.KNID))

	indexName := interfaces.GetActionExecutionIndex(query.KNID)

	// When no execution has ever been written for this KN, the index does not exist.
	// OpenSearch search returns index_not_found_exception; treat as an empty list (same as Count with ignore_unavailable).
	exists, err := s.osAccess.IndexExists(ctx, indexName)
	if err != nil {
		logger.Errorf("Failed to check action execution index: %v", err)
		return nil, fmt.Errorf("failed to query executions: %w", err)
	}
	if !exists {
		return &interfaces.ActionExecutionList{
			Entries: []interfaces.ActionExecution{},
		}, nil
	}

	// Build the must conditions
	mustConditions := []map[string]any{}

	if query.ActionTypeID != "" {
		mustConditions = append(mustConditions, map[string]any{
			"term": map[string]any{
				"action_type_id": query.ActionTypeID,
			},
		})
	}

	if len(query.Statuses) > 0 {
		mustConditions = append(mustConditions, map[string]any{
			"terms": map[string]any{
				"status": query.Statuses,
			},
		})
	} else if query.Status != "" {
		mustConditions = append(mustConditions, map[string]any{
			"term": map[string]any{
				"status": query.Status,
			},
		})
	}

	if query.TriggerType != "" {
		mustConditions = append(mustConditions, map[string]any{
			"term": map[string]any{
				"trigger_type": query.TriggerType,
			},
		})
	}

	if query.InstanceIdentityHash != "" {
		mustConditions = append(mustConditions, map[string]any{
			"term": map[string]any{
				"instance_identity_hash": query.InstanceIdentityHash,
			},
		})
	}

	if len(query.StartTimeRange) == 2 {
		mustConditions = append(mustConditions, map[string]any{
			"range": map[string]any{
				"start_time": map[string]any{
					"gte": query.StartTimeRange[0],
					"lte": query.StartTimeRange[1],
				},
			},
		})
	}

	// Build the query
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 1000 {
		limit = 1000
	}

	osQuery := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": mustConditions,
			},
		},
		"from": offset,
		"size": limit,
		// The list only carries execution summaries. Per-instance results live in the same
		// document and grow with the instance count; reading them here made a single page
		// drag every historical task's full execution detail across the wire.
		// action_type_snapshot and action_source are per-execution config copies that no list
		// consumer reads (the studio list mapper ignores them, context-loader strips them).
		// Excluded at the OpenSearch level so old documents carrying these fields are covered too.
		// dynamic_params stays: context-loader keeps it in the slimmed list for the agent.
		"_source": map[string]any{
			"excludes": []string{"results", "action_type_snapshot", "action_source"},
		},
		"sort": []map[string]any{
			{"start_time": map[string]any{"order": "desc"}},
			{"id": map[string]any{"order": "asc"}},
		},
	}

	if len(query.SearchAfter) > 0 {
		osQuery["search_after"] = query.SearchAfter
	}

	hits, err := s.osAccess.SearchData(ctx, indexName, osQuery)
	if err != nil {
		logger.Errorf("Failed to query executions: %v", err)
		return nil, fmt.Errorf("failed to query executions: %w", err)
	}

	// Convert hits to executions
	executions := make([]interfaces.ActionExecution, 0, len(hits))
	var lastSort []any

	for _, hit := range hits {
		exec, err := mapToActionExecution(hit.Source)
		if err != nil {
			logger.Warnf("Failed to parse execution, skipping: %v", err)
			continue
		}
		executions = append(executions, *exec)
		lastSort = hit.Sort
	}

	result := &interfaces.ActionExecutionList{
		Entries:     executions,
		SearchAfter: lastSort,
	}

	// Get total count if needed
	if query.NeedTotal {
		countQuery := map[string]any{
			"query": map[string]any{
				"bool": map[string]any{
					"must": mustConditions,
				},
			},
		}
		countBytes, err := s.osAccess.Count(ctx, indexName, countQuery)
		if err == nil {
			var countResult struct {
				Count int `json:"count"`
			}
			if sonic.Unmarshal(countBytes, &countResult) == nil {
				result.TotalCount = countResult.Count
			}
		}
	}

	return result, nil
}

// ensureIndexExists creates the index if it doesn't exist
// This function is safe for concurrent calls - if multiple requests try to create
// the same index simultaneously, only one will succeed and others will detect the
// index already exists.
func (s *actionLogsService) ensureIndexExists(ctx context.Context, indexName string) error {
	exists, err := s.osAccess.IndexExists(ctx, indexName)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	// Create the index with mappings
	indexBody := map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"id":                 map[string]any{"type": "keyword"},
				"kn_id":              map[string]any{"type": "keyword"},
				"action_type_id":     map[string]any{"type": "keyword"},
				"action_type_name":   map[string]any{"type": "keyword"},
				"action_source_type": map[string]any{"type": "keyword"},
				"object_type_id":     map[string]any{"type": "keyword"},
				"trigger_type":       map[string]any{"type": "keyword"},
				"status":             map[string]any{"type": "keyword"},
				"total_count":        map[string]any{"type": "integer"},
				"success_count":      map[string]any{"type": "integer"},
				"failed_count":       map[string]any{"type": "integer"},
				"executor_id":        map[string]any{"type": "keyword"},
				"executor": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":   map[string]any{"type": "keyword"},
						"type": map[string]any{"type": "keyword"},
						"name": map[string]any{"type": "keyword"},
					},
				},
				"start_time":  map[string]any{"type": "long"},
				"end_time":    map[string]any{"type": "long"},
				"duration_ms": map[string]any{"type": "long"},
				// New indexes only. Existing indexes rely on dynamic mapping (text + .keyword);
				// term queries still match because the fingerprint is a single [0-9a-f] token.
				"instance_identity_hash": map[string]any{"type": "keyword"},
				"results":                map[string]any{"type": "nested"},
				"dynamic_params":         map[string]any{"type": "object", "enabled": false},
				"action_source":          map[string]any{"type": "object", "enabled": false},
				"action_type_snapshot":   map[string]any{"type": "object", "enabled": false},
			},
		},
	}

	if err := s.osAccess.CreateIndex(ctx, indexName, indexBody); err != nil {
		// Handle concurrent creation: if creation fails, check if another request created it
		existsAfter, checkErr := s.osAccess.IndexExists(ctx, indexName)
		if checkErr == nil && existsAfter {
			logger.Debugf("Index %s was created by another request", indexName)
			return nil
		}
		return fmt.Errorf("failed to create index: %w", err)
	}

	logger.Infof("Created index: %s", indexName)

	// Wait a bit for the index to be ready
	time.Sleep(100 * time.Millisecond)

	return nil
}

// CancelExecution cancels a running or pending execution
func (s *actionLogsService) CancelExecution(ctx context.Context, knID, execID, reason string) (*interfaces.CancelExecutionResponse, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CancelExecution")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(execID),
		attr.Key("kn_id").String(knID),
		attr.Key("reason").String(reason),
	)

	// Read the summary only: the cancel never rewrites per-instance results.
	exec, err := s.searchExecution(ctx, knID, execID, map[string]any{"excludes": []string{"results"}})
	if err != nil {
		return nil, err
	}
	if !isCancellable(exec.Status) {
		return nil, fmt.Errorf("execution %s cannot be cancelled, current status: %s", execID, exec.Status)
	}

	endTime := time.Now().UnixMilli()
	result, err := s.osAccess.UpdateData(ctx, interfaces.GetActionExecutionIndex(knID), execID, painlessUpdate(cancelScript, map[string]any{
		"cancellable": []string{interfaces.ExecutionStatusPending, interfaces.ExecutionStatusRunning},
		"cancelled":   interfaces.ExecutionStatusCancelled,
		"end_time":    endTime,
	}))
	if err != nil {
		if errors.Is(err, interfaces.ErrDocumentNotFound) {
			return nil, fmt.Errorf("execution not found: %s", execID)
		}
		logger.Errorf("Failed to update cancelled execution: %v", err)
		return nil, fmt.Errorf("failed to update cancelled execution: %w", err)
	}
	if result == interfaces.UpdateResultNoop {
		// The execution reached a terminal status between the read above and this write.
		status := exec.Status
		if current, statusErr := s.GetExecutionStatus(ctx, knID, execID); statusErr == nil {
			status = current
		}
		return nil, fmt.Errorf("execution %s cannot be cancelled, current status: %s", execID, status)
	}

	// Instances that had not run yet as of the last progress update; the executor records
	// them as cancelled when it notices the cancel.
	notRun := exec.TotalCount - exec.SuccessCount - exec.FailedCount
	if notRun < 0 {
		notRun = 0
	}

	logger.Infof("Cancelled execution %s, reason=%q, not_run=%d, succeeded=%d", execID, reason, notRun, exec.SuccessCount)

	return &interfaces.CancelExecutionResponse{
		ExecutionID:    execID,
		Status:         interfaces.ExecutionStatusCancelled,
		Message:        "任务已取消",
		CancelledCount: notRun,
		CompletedCount: exec.SuccessCount,
	}, nil
}

func isCancellable(status string) bool {
	return status == interfaces.ExecutionStatusPending || status == interfaces.ExecutionStatusRunning
}

// mapToActionExecution converts a map to ActionExecution
func mapToActionExecution(m map[string]any) (*interfaces.ActionExecution, error) {
	data, err := sonic.Marshal(m)
	if err != nil {
		return nil, err
	}

	var exec interfaces.ActionExecution
	if err := sonic.Unmarshal(data, &exec); err != nil {
		return nil, err
	}

	return &exec, nil
}
