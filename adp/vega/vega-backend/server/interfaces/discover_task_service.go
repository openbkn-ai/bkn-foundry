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
	// Create creates a new DiscoverTask and sends message to Kafka.
	Create(ctx context.Context, req *CreateDiscoverTaskRequest) (string, error)
	// GetByID retrieves a DiscoverTask by ID.
	GetByID(ctx context.Context, id string) (*DiscoverTask, error)
	// List lists DiscoverTask summaries for a catalog.
	List(ctx context.Context, params DiscoverTaskQueryParams) ([]*DiscoverTaskSummary, int64, error)
	// UpdateStatus updates a DiscoverTask's status.
	UpdateStatus(ctx context.Context, id string, status string, message string, stime int64) error
	// UpdateResult updates a DiscoverTask's result.
	UpdateResult(ctx context.Context, id string, result *DiscoverResult, stime int64) error

	// CheckExistByStatuses  checks if DiscoverTasks exists by catalog ID and statuses.
	CheckExistByStatuses(ctx context.Context, catalogID string, statuses []string) (bool, error)

	// Delete atomically deletes discover tasks by IDs.
	// Pre-validates: any pending/running id returns 409 (cannot be skipped); any missing id returns 404
	// unless ignoreMissing=true. Duplicate ids in the input are de-duplicated.
	Delete(ctx context.Context, ids []string, ignoreMissing bool) error

	// InternalGetByID retrieves a DiscoverTask by ID for internal workers.
	InternalGetByID(ctx context.Context, id string) (*DiscoverTask, error)
	// InternalList returns task summaries for the local database-backed worker.
	InternalList(ctx context.Context, params DiscoverTaskQueryParams) ([]*DiscoverTaskSummary, int64, error)
	// InternalUpdateStatus updates a DiscoverTask's status for internal workers.
	InternalUpdateStatus(ctx context.Context, id string, status string, message string, stime int64) error
	// InternalMarkRunning transitions a pending DiscoverTask to running.
	InternalMarkRunning(ctx context.Context, id string) (bool, error)
	// InternalMarkCancelled 仅取消活动状态的 DiscoverTask。
	InternalMarkCancelled(ctx context.Context, id string, message string, finishTime int64) (bool, error)
	// InternalMarkFailed only fails active DiscoverTasks.
	InternalMarkFailed(ctx context.Context, id string, message string, finishTime int64) (bool, error)
	// InternalUpdateResult updates a DiscoverTask's result for internal workers.
	InternalUpdateResult(ctx context.Context, id string, result *DiscoverResult, stime int64) error

	// DispatchSignal exposes task creation and worker-capacity notifications to the local producer.
	DispatchSignal() <-chan struct{}
	// RequestDispatch asks the local producer to scan the database for pending tasks.
	RequestDispatch()
}
