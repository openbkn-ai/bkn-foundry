// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source ../interfaces/catalog_health_check_schedule_service.go -destination ../interfaces/mock/mock_catalog_health_check_schedule_service.go
type CatalogHealthCheckScheduleService interface {
	Create(ctx context.Context, tx *sql.Tx, catalog *Catalog, req *CatalogHealthCheckScheduleRequest) (*CatalogHealthCheckSchedule, error)
	GetByCatalogID(ctx context.Context, catalogID string) (*CatalogHealthCheckSchedule, error)
	Update(ctx context.Context, catalogID string, req *CatalogHealthCheckScheduleRequest) (*CatalogHealthCheckSchedule, error)
	DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error
}
