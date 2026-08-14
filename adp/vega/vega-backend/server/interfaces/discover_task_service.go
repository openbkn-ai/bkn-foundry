// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package interfaces defines entities, DTOs, and service interfaces.
package interfaces

import "context"

// DiscoverTaskService defines discover task business logic interface.
//
//go:generate mockgen -source ../interfaces/discover_task_service.go -destination ../interfaces/mock/mock_discover_task_service.go
type DiscoverTaskService interface {
	// Create creates a new DiscoverTask and requests local dispatch.
	Create(ctx context.Context, req *CreateDiscoverTaskRequest) (string, error)
	// GetByID retrieves a DiscoverTask by ID.
	GetByID(ctx context.Context, id string) (*DiscoverTask, error)
	// List lists DiscoverTask summaries for a catalog.
	List(ctx context.Context, params DiscoverTaskQueryParams) ([]*DiscoverTaskSummary, int64, error)
	// DeleteByIDs atomically deletes discover tasks by IDs.
	// Pre-validates: any pending/running id returns 409 (cannot be skipped); any missing id returns 404
	// unless ignoreMissing=true. Duplicate ids in the input are de-duplicated.
	DeleteByIDs(ctx context.Context, ids []string, ignoreMissing bool) error

	// InternalGetByID retrieves a DiscoverTask by ID for internal workers.
	InternalGetByID(ctx context.Context, id string) (*DiscoverTask, error)
	// InternalList returns task summaries for the local database-backed worker.
	InternalList(ctx context.Context, params DiscoverTaskQueryParams) ([]*DiscoverTaskSummary, error)
	// InternalMarkRunning transitions a pending DiscoverTask to running.
	InternalMarkRunning(ctx context.Context, id string) (bool, error)
	// InternalUpdateProgress stores observable execution progress for a running DiscoverTask.
	InternalUpdateProgress(ctx context.Context, id string, progress int, message string) (bool, error)
	// InternalMarkCancelled only cancels active DiscoverTasks.
	InternalMarkCancelled(ctx context.Context, id string, message string) (bool, error)
	// InternalMarkFailed only fails active DiscoverTasks.
	InternalMarkFailed(ctx context.Context, id string, message string) (bool, error)
	// InternalMarkCompleted stores the result and completes a running DiscoverTask.
	InternalMarkCompleted(ctx context.Context, id string, result *DiscoverResult) (bool, error)

	// DispatchSignal exposes task creation and worker-capacity notifications to the local producer.
	DispatchSignal() <-chan struct{}
	// RequestDispatch asks the local producer to scan the database for pending tasks.
	RequestDispatch()
}
