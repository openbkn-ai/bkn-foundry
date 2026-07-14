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
	// GetByResourceID retrieves a build task by resource ID.
	GetByResourceID(ctx context.Context, resourceID string) (*BuildTask, error)
	// GetByCatalogID retrieves build tasks by catalog ID.
	GetByCatalogID(ctx context.Context, catalogID string) ([]*BuildTask, error)
	// List retrieves build tasks with filters and pagination.
	List(ctx context.Context, params BuildTasksQueryParams) ([]*BuildTask, int64, error)
	// UpdateStatus updates a build task's status and progress fields. When allowedStatuses is not empty,
	// the update is applied only if the current status matches one of them.
	UpdateStatus(ctx context.Context, tx *sql.Tx, id string, update BuildTaskUpdate, allowedStatuses ...string) (bool, error)
	// GetStatus retrieves the status of a build task by ID.
	GetStatus(ctx context.Context, id string) (string, error)
	// Delete deletes a build task by ID.
	Delete(ctx context.Context, id string) error
}
