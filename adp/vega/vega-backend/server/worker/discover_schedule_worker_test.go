// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestDiscoverScheduleWorkerRunDue(t *testing.T) {
	t.Run("executes all due schedules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		first := dueDiscoverSchedule("schedule-1")
		second := dueDiscoverSchedule("schedule-2")
		dsa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return([]*interfaces.DiscoverSchedule{first, second}, nil)
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), first.ID, first.UpdateTime, first.NextRun, gomock.Any(), gomock.Any()).Return(int64(1), nil)
		dss.EXPECT().ExecuteSchedule(gomock.Any(), first).Return(nil)
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), second.ID, second.UpdateTime, second.NextRun, gomock.Any(), gomock.Any()).Return(int64(1), nil)
		dss.EXPECT().ExecuteSchedule(gomock.Any(), second).Return(nil)

		newTestDiscoverScheduleWorker(dsa, dss).runDue()
	})

	t.Run("returns when due query fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
		dsa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

		newTestDiscoverScheduleWorker(dsa, vmock.NewMockDiscoverScheduleService(ctrl)).runDue()
	})

	t.Run("continues after one schedule panics", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		first := dueDiscoverSchedule("schedule-1")
		second := dueDiscoverSchedule("schedule-2")
		dsa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return([]*interfaces.DiscoverSchedule{first, second}, nil)
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), first.ID, first.UpdateTime, first.NextRun, gomock.Any(), gomock.Any()).Do(func(context.Context, string, int64, int64, int64, int64) {
			panic("update panic")
		})
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), second.ID, second.UpdateTime, second.NextRun, gomock.Any(), gomock.Any()).Return(int64(1), nil)
		dss.EXPECT().ExecuteSchedule(gomock.Any(), second).Return(nil)

		newTestDiscoverScheduleWorker(dsa, dss).runDue()
	})
}

func TestDiscoverScheduleWorkerRunSchedule(t *testing.T) {
	t.Run("initializes legacy schedule without executing it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := dueDiscoverSchedule("schedule-1")
		schedule.NextRun = 0
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), schedule.ID, schedule.UpdateTime, int64(0), schedule.LastRun, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, _, _, _ int64, nextRun int64) (int64, error) {
				require.Greater(t, nextRun, time.Now().UnixMilli())
				return 1, nil
			},
		)

		newTestDiscoverScheduleWorker(nil, dss).runSchedule(context.Background(), schedule)
	})

	t.Run("advances legacy schedule to future start time without executing it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := dueDiscoverSchedule("schedule-1")
		schedule.NextRun = time.Now().Add(-time.Hour).UnixMilli()
		schedule.StartTime = time.Now().Add(2 * time.Hour).UnixMilli()
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, schedule.LastRun, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, _, _, _ int64, nextRun int64) (int64, error) {
				require.GreaterOrEqual(t, nextRun, schedule.StartTime)
				return 1, nil
			},
		)

		newTestDiscoverScheduleWorker(nil, dss).runSchedule(context.Background(), schedule)
	})

	t.Run("advances schedule before creating task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := dueDiscoverSchedule("schedule-1")
		schedule.NextRun = time.Now().Add(-time.Minute).UnixMilli()
		gomock.InOrder(
			dss.EXPECT().UpdateRunMetadata(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, _, _ int64, lastRun, nextRun int64) (int64, error) {
					require.WithinDuration(t, time.Now(), time.UnixMilli(lastRun), time.Second)
					require.Greater(t, nextRun, lastRun)
					return 1, nil
				},
			),
			dss.EXPECT().ExecuteSchedule(gomock.Any(), schedule).Return(nil),
		)

		newTestDiscoverScheduleWorker(nil, dss).runSchedule(context.Background(), schedule)
	})

	t.Run("does not create task when advancing fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := dueDiscoverSchedule("schedule-1")
		schedule.NextRun = time.Now().Add(-time.Minute).UnixMilli()
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, gomock.Any(), gomock.Any()).Return(int64(0), errors.New("db down"))

		newTestDiscoverScheduleWorker(nil, dss).runSchedule(context.Background(), schedule)
	})

	t.Run("does not create task when schedule was already claimed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := dueDiscoverSchedule("schedule-1")
		schedule.NextRun = time.Now().Add(-time.Minute).UnixMilli()
		dss.EXPECT().UpdateRunMetadata(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, gomock.Any(), gomock.Any()).Return(int64(0), nil)

		newTestDiscoverScheduleWorker(nil, dss).runSchedule(context.Background(), schedule)
	})

	t.Run("disables expired schedule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := dueDiscoverSchedule("schedule-1")
		schedule.NextRun = time.Now().Add(-time.Minute).UnixMilli()
		schedule.EndTime = time.Now().Add(-time.Second).UnixMilli()
		schedule.Creator = interfaces.AccountInfo{ID: "schedule-creator", Type: "user"}
		dss.EXPECT().UpdateEnabled(gomock.Any(), schedule, false).DoAndReturn(func(ctx context.Context, _ *interfaces.DiscoverSchedule, _ bool) error {
			assert.Equal(t, schedule.Creator, ctx.Value(interfaces.ACCOUNT_INFO_KEY))
			return nil
		})

		newTestDiscoverScheduleWorker(nil, dss).runSchedule(context.Background(), schedule)
	})

	t.Run("disables legacy schedule with sub-hour cron expression", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dss := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := dueDiscoverSchedule("schedule-1")
		schedule.NextRun = time.Now().Add(-time.Minute).UnixMilli()
		schedule.CronExpr = "*/30 * * * *"
		dss.EXPECT().UpdateEnabled(gomock.Any(), schedule, false).Return(nil)

		newTestDiscoverScheduleWorker(nil, dss).runSchedule(context.Background(), schedule)
	})

	t.Run("skips disabled and not due schedules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dsw := newTestDiscoverScheduleWorker(
			nil,
			vmock.NewMockDiscoverScheduleService(ctrl),
		)

		disabled := dueDiscoverSchedule("disabled")
		disabled.Enabled = false
		notDue := dueDiscoverSchedule("not-due")
		notDue.NextRun = time.Now().Add(time.Hour).UnixMilli()

		dsw.runSchedule(context.Background(), disabled)
		dsw.runSchedule(context.Background(), notDue)
	})

}

func dueDiscoverSchedule(id string) *interfaces.DiscoverSchedule {
	return &interfaces.DiscoverSchedule{
		ID:       id,
		Enabled:  true,
		CronExpr: "0 * * * *",
		NextRun:  time.Now().Add(-time.Minute).UnixMilli(),
	}
}

func newTestDiscoverScheduleWorker(
	dsa interfaces.DiscoverScheduleAccess,
	dss interfaces.DiscoverScheduleService,
) *DiscoverScheduleWorker {
	return &DiscoverScheduleWorker{
		dsa: dsa,
		dss: dss,
	}
}
