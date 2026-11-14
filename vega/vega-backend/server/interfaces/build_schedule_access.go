// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source ../interfaces/build_schedule_access.go -destination ../interfaces/mock/mock_build_schedule_access.go

type BuildScheduleAccess interface {
	Create(context.Context, *BuildSchedule) error
	GetByID(context.Context, string) (*BuildSchedule, error)
	List(context.Context, BuildScheduleQueryParams) ([]*BuildSchedule, int64, error)
	Update(context.Context, *BuildSchedule, int64) (int64, error)
	UpdateEnabled(context.Context, string, bool, *int64, int64, int64, AccountInfo) (int64, error)
	Delete(context.Context, string) error
	DeleteByCatalogID(context.Context, *sql.Tx, string) error
	ListDue(context.Context, int64) ([]*BuildSchedule, error)
	InitializeNextRun(ctx context.Context, id string, expectedUpdateTime, expectedNextRun, nextRun int64) (int64, error)
	ClaimRun(ctx context.Context, id string, expectedUpdateTime, expectedNextRun, lastRun, nextRun int64, runID string) (int64, error)
	CompleteRun(ctx context.Context, id, runID string, result ScheduleRunResult) (int64, error)
	DisableInvalid(ctx context.Context, id string, expectedUpdateTime, expectedNextRun int64, message string) (int64, error)
}
