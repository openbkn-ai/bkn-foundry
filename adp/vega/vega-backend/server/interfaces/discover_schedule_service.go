// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// DiscoverScheduleService defines discover schedule business logic interface.
//
//go:generate mockgen -source ../interfaces/discover_schedule_service.go -destination ../interfaces/mock/mock_discover_schedule_service.go
type DiscoverScheduleService interface {
	// Create creates a new discover schedule.
	Create(ctx context.Context, req *DiscoverScheduleRequest) (string, error)
	// GetByID retrieves a discover schedule by ID.
	GetByID(ctx context.Context, id string) (*DiscoverSchedule, error)
	// List lists discover schedules.
	List(ctx context.Context, params DiscoverScheduleQueryParams) ([]*DiscoverSchedule, int64, error)
	// Update updates a discover schedule.
	Update(ctx context.Context, current *DiscoverSchedule, req *DiscoverScheduleRequest) error
	// Delete deletes a discover schedule that has already been resolved by the caller.
	Delete(ctx context.Context, schedule *DiscoverSchedule) error
	// UpdateEnabled updates the enabled state of a discover schedule.
	UpdateEnabled(ctx context.Context, schedule *DiscoverSchedule, enabled bool) error
	// UpdateRunMetadata atomically advances run metadata when the schedule has not changed.
	UpdateRunMetadata(ctx context.Context, id string, expectedUpdateTime, expectedNextRun, lastRun, nextRun int64) (int64, error)
	// ExecuteSchedule executes a discover schedule.
	ExecuteSchedule(ctx context.Context, schedule *DiscoverSchedule) error

	// InternalUpdateEnabled updates a schedule state for the internal scheduler without user authorization.
	InternalUpdateEnabled(ctx context.Context, schedule *DiscoverSchedule, enabled bool) error
}
