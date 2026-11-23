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
	// GetByIDs retrieves build tasks keyed by ID.
	GetByIDs(ctx context.Context, ids []string) (map[string]*BuildTask, error)
	// GetByCatalogID retrieves build tasks by catalog ID.
	GetByCatalogID(ctx context.Context, catalogID string) ([]*BuildTask, error)
	// List retrieves build task summaries with filters and pagination.
	List(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTaskSummary, int64, error)
	// DeleteByIDs deletes build tasks by IDs.
	DeleteByIDs(ctx context.Context, ids []string) (int64, error)

	// SetProgress persists execution progress if the task status is in allowedStatuses.
	SetProgress(ctx context.Context, tx *sql.Tx, id string, progress BuildTaskProgress, allowedStatuses []string, lastProgressTime int64) (bool, error)
	// MarkPending transitions a stopped or failed build task to pending.
	MarkPending(ctx context.Context, tx *sql.Tx, id string, reset bool) (bool, error)
	// MarkRunning transitions a pending build task to running.
	MarkRunning(ctx context.Context, tx *sql.Tx, id string, indexName *string, startTime int64) (bool, error)
	// MarkStopping transitions a running build task to stopping.
	MarkStopping(ctx context.Context, tx *sql.Tx, id string) (bool, error)
	// MarkStopped transitions a build task in an allowed status to stopped.
	MarkStopped(ctx context.Context, tx *sql.Tx, id string, allowedStatuses []string, finishTime int64) (bool, error)
	// MarkCompleted transitions a running build task to completed.
	MarkCompleted(ctx context.Context, tx *sql.Tx, id string, finishTime int64) (bool, error)
	// MarkFailed transitions a build task in an allowed status to failed.
	MarkFailed(ctx context.Context, tx *sql.Tx, id, detail string, allowedStatuses []string, finishTime int64) (bool, error)
	// MarkCancelled cancels an active build task.
	MarkCancelled(ctx context.Context, tx *sql.Tx, id, detail string, finishTime int64) (bool, error)
	// DeleteByCatalogID deletes build tasks belonging to a Catalog.
	DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error
	// GetStatusByID retrieves the status of a build task by ID.
	GetStatusByID(ctx context.Context, id string) (string, error)

	// InternalList retrieves build task summaries for internal callers without a count query.
	InternalList(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTaskSummary, error)
}
