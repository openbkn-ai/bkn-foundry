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

//go:generate mockgen -source ../interfaces/build_task_access.go -destination ../interfaces/mock/mock_build_task_access.go

// BuildTaskAccess defines build task data access interface.
type BuildTaskAccess interface {
	// Create creates a new build task.
	Create(ctx context.Context, buildTask *BuildTask) error
	// GetByID retrieves a build task by ID.
	GetByID(ctx context.Context, id string) (*BuildTask, error)
	// GetByCatalogID retrieves build tasks by catalog ID.
	GetByCatalogID(ctx context.Context, catalogID string) ([]*BuildTask, error)
	// List retrieves build task summaries with filters and pagination.
	List(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTaskSummary, int64, error)
	// DeleteByIDs deletes build tasks by IDs.
	DeleteByIDs(ctx context.Context, ids []string) (int64, error)

	// SetProgress persists execution progress for an active build task.
	SetProgress(ctx context.Context, tx *sql.Tx, id string, progress BuildTaskProgress, lastProgressTime int64) (bool, error)
	// MarkPending transitions a stopped or failed build task to pending.
	MarkPending(ctx context.Context, id string, reset bool) (bool, error)
	// MarkRunning transitions a pending build task to running.
	MarkRunning(ctx context.Context, id string, startTime int64) (bool, error)
	// MarkStopping transitions a running build task to stopping.
	MarkStopping(ctx context.Context, id string) (bool, error)
	// MarkStopped transitions a pending or stopping build task to stopped.
	MarkStopped(ctx context.Context, id string, finishTime int64) (bool, error)
	// MarkCompleted transitions a running build task to completed.
	MarkCompleted(ctx context.Context, tx *sql.Tx, id string, finishTime int64) (bool, error)
	// MarkFailed fails an active build task.
	MarkFailed(ctx context.Context, id, detail string, finishTime int64) (bool, error)
	// MarkCancelled cancels an active build task.
	MarkCancelled(ctx context.Context, id, detail string, finishTime int64) (bool, error)
	// MarkCancelledByCatalogID cancels pending build tasks for a deleted catalog.
	MarkCancelledByCatalogID(ctx context.Context, tx *sql.Tx, catalogID, message string, finishTime int64) error
	// GetStatus retrieves the status of a build task by ID.
	GetStatus(ctx context.Context, id string) (string, error)

	// InternalList retrieves build task summaries for internal callers without a count query.
	InternalList(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTaskSummary, error)
}
