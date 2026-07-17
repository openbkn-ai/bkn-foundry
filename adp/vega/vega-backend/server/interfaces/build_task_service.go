// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"

	"github.com/hibiken/asynq"
)

//go:generate mockgen -source ../interfaces/build_task_service.go -destination ../interfaces/mock/mock_build_task_service.go

// BuildTaskService defines build task business logic interface.
type BuildTaskService interface {
	// Create creates a new build task. resource_id and mode come from req.
	Create(ctx context.Context, req *CreateBuildTaskRequest) (string, error)
	// GetByID retrieves a build task by ID.
	GetByID(ctx context.Context, id string) (*BuildTask, error)
	// GetByResourceID retrieves a build task by resource ID.
	GetByResourceID(ctx context.Context, resourceID string) (*BuildTask, error)
	// List retrieves build tasks with filters and pagination.
	List(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTask, int64, error)
	// Start transitions a task from {init, stopped} to running (asynchronous; status persisted by worker).
	Start(ctx context.Context, taskID string, reset bool) error
	// Stop transitions a task from running to stopping (asynchronous; status persisted by worker).
	Stop(ctx context.Context, taskID string) error
	// Delete atomically deletes build tasks by IDs.
	// Pre-validates: any missing id returns 404 unless ignoreMissing=true; any running/stopping id returns 409 (cannot be skipped).
	Delete(ctx context.Context, ids []string, ignoreMissing bool, deleteActiveIndex bool) error

	// DebugTaskQueue returns the in-process build task queue used in DEBUG_MODE.
	DebugTaskQueue() <-chan *asynq.Task

	// InternalGetByID retrieves a build task by ID for internal workers.
	InternalGetByID(ctx context.Context, id string) (*BuildTask, error)
	// InternalGetByCatalogID retrieves build tasks by catalog ID for internal workers.
	InternalGetByCatalogID(ctx context.Context, catalogID string) ([]*BuildTask, error)
	// InternalList retrieves build tasks for internal workers.
	InternalList(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTask, int64, error)
	// InternalUpdateStatus updates a build task status for internal workers.
	InternalUpdateStatus(ctx context.Context, tx *sql.Tx, id string, update BuildTaskUpdate, allowedStatuses ...string) (bool, error)
	// InternalGetStatus retrieves the status of a build task for internal workers.
	InternalGetStatus(ctx context.Context, id string) (string, error)
}
