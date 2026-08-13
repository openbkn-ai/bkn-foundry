// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source ../interfaces/catalog_health_check_schedule_access.go -destination ../interfaces/mock/mock_catalog_health_check_schedule_access.go
type CatalogHealthCheckScheduleAccess interface {
	Create(ctx context.Context, tx *sql.Tx, schedule *CatalogHealthCheckSchedule) error
	GetByCatalogID(ctx context.Context, catalogID string) (*CatalogHealthCheckSchedule, error)
	Update(ctx context.Context, schedule *CatalogHealthCheckSchedule) error
	DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error

	ListDue(ctx context.Context, now int64) ([]*CatalogHealthCheckSchedule, error)
	UpdateInheritedNextRun(ctx context.Context, now, nextRun int64) error
	UpdateRunMetadata(ctx context.Context, catalogID string, scheduleUpdateTime, lastRun, nextRun int64) error
}
