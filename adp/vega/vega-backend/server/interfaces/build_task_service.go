// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
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
	// List retrieves build task summaries with filters and pagination.
	List(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTaskSummary, int64, error)
	// Start transitions a stopped or failed task to pending; the worker later persists running.
	Start(ctx context.Context, taskID string, reset bool) error
	// Stop transitions pending to stopped, or running to stopping (then asynchronously stopped by the worker).
	Stop(ctx context.Context, taskID string) error
	// Delete atomically deletes build tasks by IDs.
	// Pre-validates: any missing id returns 404 unless ignoreMissing=true; any running/stopping id returns 409 (cannot be skipped).
	Delete(ctx context.Context, ids []string, ignoreMissing bool, deleteActiveIndex bool) error

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

	// DispatchSignal exposes task creation and worker-capacity notifications to the local producer.
	DispatchSignal() <-chan struct{}
	// RequestDispatch asks the local producer to scan the database for pending tasks.
	RequestDispatch()
}
