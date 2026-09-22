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

// DiscoverScheduleAccess defines data access interface for scheduled discover schedules.
//
//go:generate mockgen -source ../interfaces/discover_schedule_access.go -destination ../interfaces/mock/mock_discover_schedule_access.go
type DiscoverScheduleAccess interface {
	// Create creates a new discover schedule in database.
	Create(ctx context.Context, schedule *DiscoverSchedule) error
	// GetByID retrieves a discover schedule by ID.
	GetByID(ctx context.Context, id string) (*DiscoverSchedule, error)
	// List lists discover schedules with filters.
	List(ctx context.Context, params DiscoverScheduleQueryParams) ([]*DiscoverSchedule, int64, error)
	// Update updates a discover schedule.
	Update(ctx context.Context, schedule *DiscoverSchedule, expectedUpdateTime int64) (int64, error)
	// UpdateEnabled updates the enabled state and updates the next run time only when enabling a discover schedule.
	UpdateEnabled(ctx context.Context, id string, enabled bool, nextRun *int64,
		expectedUpdateTime, updateTime int64, updater AccountInfo) (int64, error)
	// Delete deletes a discover schedule by ID.
	Delete(ctx context.Context, id string) error
	// DeleteByCatalogID deletes discover schedules belonging to a Catalog.
	DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error
	// ListDue retrieves enabled discover schedules whose next run is due.
	ListDue(ctx context.Context, now int64) ([]*DiscoverSchedule, error)
	// UpdateRunMetadata atomically advances run metadata when the schedule has not changed.
	UpdateRunMetadata(ctx context.Context, id string, expectedUpdateTime, expectedNextRun, lastRun, nextRun int64) (int64, error)
}
