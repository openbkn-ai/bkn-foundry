// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package interfaces defines entities, DTOs, and service interfaces.
package interfaces

import (
	"context"
	"database/sql"
)

// DiscoverTaskAccess defines discover task data access interface.
//
//go:generate mockgen -source ../interfaces/discover_task_access.go -destination ../interfaces/mock/mock_discover_task_access.go
type DiscoverTaskAccess interface {
	// Create creates a new DiscoverTask.
	Create(ctx context.Context, task *DiscoverTask) error
	// GetByID retrieves a DiscoverTask by ID.
	GetByID(ctx context.Context, id string) (*DiscoverTask, error)
	// List lists DiscoverTask summaries with filters.
	List(ctx context.Context, params DiscoverTaskQueryParams) ([]*DiscoverTaskSummary, int64, error)
	// DeleteByIDs deletes DiscoverTasks by IDs.
	DeleteByIDs(ctx context.Context, ids []string) (int64, error)

	// MarkRunning transitions a pending DiscoverTask to running.
	MarkRunning(ctx context.Context, id string, startTime int64) (bool, error)
	// UpdateProgress stores observable execution progress for a running DiscoverTask.
	UpdateProgress(ctx context.Context, id string, progress int, message string, lastProgressTime int64) (bool, error)
	// MarkCompleted completes a running DiscoverTask and stores its result.
	MarkCompleted(ctx context.Context, id string, result *DiscoverResult, finishTime int64) (bool, error)
	// MarkFailed only fails pending or running DiscoverTasks.
	MarkFailed(ctx context.Context, id, message string, finishTime int64) (bool, error)
	// MarkCancelled only cancels pending or running DiscoverTasks.
	MarkCancelled(ctx context.Context, id, message string, finishTime int64) (bool, error)
	// MarkCancelledByCatalogID marks pending tasks as cancelled when their Catalog is deleted.
	MarkCancelledByCatalogID(ctx context.Context, tx *sql.Tx, catalogID, message string, finishTime int64) error

	// InternalList lists DiscoverTask summaries without a count query.
	InternalList(ctx context.Context, params DiscoverTaskQueryParams) ([]*DiscoverTaskSummary, error)
}
