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
	Update(ctx context.Context, schedule *CatalogHealthCheckSchedule, expectedUpdateTime int64) (int64, error)
	DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error

	ListDue(ctx context.Context, now int64) ([]*CatalogHealthCheckSchedule, error)
	UpdateInheritedNextRun(ctx context.Context, now, nextRun int64) error
	// InitializeNextRun 初始化未来时间，不修改最近结果与 last_run。
	InitializeNextRun(ctx context.Context, id string, expectedUpdateTime, expectedNextRun, nextRun int64) (int64, error)
	// ClaimRun 原子抢占本次触发并推进时间。
	ClaimRun(ctx context.Context, id string, expectedUpdateTime, expectedNextRun, lastRun, nextRun int64, runID string) (int64, error)
	// CompleteRun 仅回写本次尚未完成的触发结果。
	CompleteRun(ctx context.Context, id, runID string, result ScheduleRunResult) (int64, error)
	// DisableInvalid 按快照版本禁用非法计划并保存失败说明。
	DisableInvalid(ctx context.Context, id string, expectedUpdateTime, expectedNextRun int64, message string) (int64, error)
}
