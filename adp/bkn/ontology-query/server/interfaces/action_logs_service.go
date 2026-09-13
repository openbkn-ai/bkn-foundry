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

	// UpdateExecutionProgress records the counters and results produced so far. It never
	// touches the execution status, so it cannot undo a concurrent cancel.
	UpdateExecutionProgress(ctx context.Context, knID, execID string, progress *ExecutionProgress) error

	// FinishExecution writes the terminal record of an execution. An execution that was
	// cancelled meanwhile stays cancelled; the counters and results still record what ran.
	FinishExecution(ctx context.Context, knID, execID string, outcome *ExecutionOutcome) error

	// GetExecutionStatus returns only the current status of an execution.
	GetExecutionStatus(ctx context.Context, knID, execID string) (string, error)

	// GetExecution retrieves a single execution by ID with optional results pagination
	GetExecution(ctx context.Context, query *ActionLogDetailQuery) (*ActionExecution, error)

	// QueryExecutions queries executions based on filter criteria
	QueryExecutions(ctx context.Context, query *ActionLogQuery) (*ActionExecutionList, error)

	// CancelExecution cancels a running or pending execution
	CancelExecution(ctx context.Context, knID, execID, reason string) (*CancelExecutionResponse, error)
}

// OpenSearch index name pattern for action executions
const ActionExecutionIndexPrefix = "ontology_action_executions_"

// GetActionExecutionIndex returns the OpenSearch index name for a knowledge network
func GetActionExecutionIndex(knID string) string {
	return ActionExecutionIndexPrefix + knID
}
