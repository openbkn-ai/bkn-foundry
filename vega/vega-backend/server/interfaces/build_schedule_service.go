// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/build_schedule_service.go -destination ../interfaces/mock/mock_build_schedule_service.go

type BuildScheduleService interface {
	Create(context.Context, *BuildScheduleRequest) (string, error)
	GetByID(context.Context, string) (*BuildSchedule, error)
	List(context.Context, BuildScheduleQueryParams) ([]*BuildSchedule, int64, error)
	Update(context.Context, *BuildSchedule, *BuildScheduleRequest) error
	Delete(context.Context, *BuildSchedule) error
	UpdateEnabled(context.Context, *BuildSchedule, bool) error
	ExecuteSchedule(context.Context, *BuildSchedule) (ScheduleRunResult, error)

	InternalUpdateEnabled(context.Context, *BuildSchedule, bool) error
}
