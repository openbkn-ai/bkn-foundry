// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// ActionSchedulerService defines the interface for action execution scheduling
//
//go:generate mockgen -source ../interfaces/action_scheduler_service.go -destination ../interfaces/mock/mock_action_scheduler_service.go
type ActionSchedulerService interface {
	// CheckActionExecution resolves trusted dependencies and verifies the current subject without executing the action.
	CheckActionExecution(ctx context.Context, req *ActionExecutionRequest) error

	// ExecuteAction starts async action execution, returns execution_id immediately
	ExecuteAction(ctx context.Context, req *ActionExecutionRequest) (*ActionExecutionResponse, error)

	// GetExecution retrieves execution status and results
	GetExecution(ctx context.Context, knID, executionID string) (*ActionExecution, error)
}

// DuplicateCheckHook is a reserved extension point for duplicate execution strategy
// This hook will be called before execution to check if the action should be executed
// Returns true if execution should proceed, false if it should be skipped
type DuplicateCheckHook func(ctx context.Context, req *ActionExecutionRequest) (bool, error)
