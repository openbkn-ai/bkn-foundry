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

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestScheduleWorkerScheduleLifecycle(t *testing.T) {
	t.Run("schedules and unschedules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sw := newTestScheduleWorker(vmock.NewMockDiscoverScheduleService(ctrl))
		schedule := &interfaces.DiscoverSchedule{ID: "schedule-1", CronExpr: "* * * * *", Enabled: true}

		require.NoError(t, sw.schedule(schedule))
		assert.Len(t, sw.scheduleEntries, 1)

		require.NoError(t, sw.schedule(schedule))
		assert.Len(t, sw.scheduleEntries, 1)

		require.NoError(t, sw.unschedule("schedule-1"))
		assert.Empty(t, sw.scheduleEntries)
		require.NoError(t, sw.unschedule("missing"))
	})

	t.Run("invalid cron", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sw := newTestScheduleWorker(vmock.NewMockDiscoverScheduleService(ctrl))

		err := sw.schedule(&interfaces.DiscoverSchedule{ID: "bad", CronExpr: "bad cron", Enabled: true})

		require.Error(t, err)
		assert.Empty(t, sw.scheduleEntries)
	})
}

func TestScheduleWorkerStartReloadAndUpdate(t *testing.T) {
	t.Run("start loads enabled schedules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetEnabledSchedules(gomock.Any()).Return(
			[]*interfaces.DiscoverSchedule{{ID: "schedule-1", CronExpr: "* * * * *", Enabled: true}},
			nil,
		)
		sw := newTestScheduleWorker(svc)
		defer sw.Stop()

		require.NoError(t, sw.Start())
		assert.Len(t, sw.scheduleEntries, 1)
	})

	t.Run("reload replaces entries", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetEnabledSchedules(gomock.Any()).Return(
			[]*interfaces.DiscoverSchedule{{ID: "schedule-2", CronExpr: "* * * * *", Enabled: true}},
			nil,
		)
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["old"] = cron.EntryID(99)

		require.NoError(t, sw.Reload())
		assert.NotContains(t, sw.scheduleEntries, "old")
		assert.Contains(t, sw.scheduleEntries, "schedule-2")
	})

	t.Run("schedule skips disabled service result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(
			&interfaces.DiscoverSchedule{ID: "schedule-1", CronExpr: "* * * * *", Enabled: false},
			nil,
		)
		sw := newTestScheduleWorker(svc)

		require.NoError(t, sw.Schedule("schedule-1"))
		assert.Empty(t, sw.scheduleEntries)
	})

	t.Run("update schedule reschedules enabled result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(
			&interfaces.DiscoverSchedule{ID: "schedule-1", CronExpr: "* * * * *", Enabled: true},
			nil,
		)
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["schedule-1"] = cron.EntryID(99)

		require.NoError(t, sw.UpdateSchedule("schedule-1"))
		assert.Contains(t, sw.scheduleEntries, "schedule-1")
	})

	t.Run("service load error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetEnabledSchedules(gomock.Any()).Return(nil, errors.New("db down"))
		sw := newTestScheduleWorker(svc)

		err := sw.Start()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}

func TestScheduleWorkerExecuteSchedule(t *testing.T) {
	t.Run("disabled schedule is unscheduled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(
			&interfaces.DiscoverSchedule{ID: "schedule-1", Enabled: false},
			nil,
		)
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["schedule-1"] = cron.EntryID(1)

		sw.executeSchedule("schedule-1")

		assert.NotContains(t, sw.scheduleEntries, "schedule-1")
	})

	t.Run("future start time skips execution", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(
			&interfaces.DiscoverSchedule{ID: "schedule-1", Enabled: true, StartTime: time.Now().Add(time.Hour).UnixMilli()},
			nil,
		)
		sw := newTestScheduleWorker(svc)

		sw.executeSchedule("schedule-1")
	})

	t.Run("expired schedule disables and unschedules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		svc.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(
			&interfaces.DiscoverSchedule{ID: "schedule-1", Enabled: true, EndTime: time.Now().Add(-time.Hour).UnixMilli()},
			nil,
		)
		svc.EXPECT().Disable(gomock.Any(), "schedule-1").Return(nil)
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["schedule-1"] = cron.EntryID(1)

		sw.executeSchedule("schedule-1")

		assert.NotContains(t, sw.scheduleEntries, "schedule-1")
	})

	t.Run("enabled schedule executes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		svc := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := &interfaces.DiscoverSchedule{ID: "schedule-1", Enabled: true}
		svc.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(schedule, nil)
		svc.EXPECT().ExecuteSchedule(gomock.Any(), schedule).Return(nil)
		sw := newTestScheduleWorker(svc)

		sw.executeSchedule("schedule-1")
	})
}

func newTestScheduleWorker(dss interfaces.DiscoverScheduleService) *ScheduleWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &ScheduleWorker{
		appSetting:      &common.AppSetting{},
		cron:            cron.New(),
		dss:             dss,
		scheduleEntries: make(map[string]cron.EntryID),
		ctx:             ctx,
		cancel:          cancel,
	}
}
