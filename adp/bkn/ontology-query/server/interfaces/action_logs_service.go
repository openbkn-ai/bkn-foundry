// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// ActionLogsService defines the interface for managing action execution logs
//
//go:generate mockgen -source ../interfaces/action_logs_service.go -destination ../interfaces/mock/mock_action_logs_service.go
type ActionLogsService interface {
	// CreateExecution creates a new execution record
	CreateExecution(ctx context.Context, exec *ActionExecution) error

	// MarkExecutionRunning moves a pending execution to running. An execution that is no
	// longer pending (cancelled before it started, in particular) is left untouched.
	MarkExecutionRunning(ctx context.Context, knID, execID string) error

	// UpdateExecutionProgress records the counters so far. It never touches the execution
	// status, so it cannot undo a concurrent cancel.
	UpdateExecutionProgress(ctx context.Context, knID, execID string, progress *ExecutionProgress) error

	// FinishExecution writes the terminal record of an execution. An execution that was
	// cancelled meanwhile stays cancelled; the counters still record what ran.
	FinishExecution(ctx context.Context, knID, execID string, outcome *ExecutionOutcome) error

	// GetExecutionStatus returns only the current status of an execution.
	GetExecutionStatus(ctx context.Context, knID, execID string) (string, error)

	// AppendResults stores results of an execution in the results index, one document per
	// result. firstSeq is the position of results[0] within the execution; a retried batch
	// overwrites the same documents instead of duplicating them.
	AppendResults(ctx context.Context, knID, execID string, firstSeq int, results []ObjectExecutionResult) error

	// QueryResults returns one page of an execution's results, filtered and paginated by
	// OpenSearch. Executions recorded before results moved to their own index are paged
	// from the results embedded in the execution document.
	QueryResults(ctx context.Context, query *ActionResultsQuery) (*ActionExecutionResultList, error)

	// GetExecution retrieves a single execution by ID with optional results pagination
	GetExecution(ctx context.Context, query *ActionLogDetailQuery) (*ActionExecution, error)

	// QueryExecutions queries executions based on filter criteria
	QueryExecutions(ctx context.Context, query *ActionLogQuery) (*ActionExecutionList, error)

	// CancelExecution cancels a running or pending execution
	CancelExecution(ctx context.Context, knID, execID, reason string) (*CancelExecutionResponse, error)
}

// OpenSearch index name pattern for action executions
const ActionExecutionIndexPrefix = "ontology_action_executions_"

// ActionExecutionResultsIndex holds one document per instance result, for every knowledge
// network. A single index keeps the shard count flat as knowledge networks are added.
const ActionExecutionResultsIndex = "ontology_action_execution_results"

// GetActionExecutionIndex returns the OpenSearch index name for a knowledge network
func GetActionExecutionIndex(knID string) string {
	return ActionExecutionIndexPrefix + knID
}
