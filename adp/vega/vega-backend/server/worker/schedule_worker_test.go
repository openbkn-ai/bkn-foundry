// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
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

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestScheduleWorkerScheduleLifecycle(t *testing.T) {
	t.Run("schedules and unschedules", func(t *testing.T) {
		sw := newTestScheduleWorker(&fakeDiscoverScheduleService{})
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
		sw := newTestScheduleWorker(&fakeDiscoverScheduleService{})

		err := sw.schedule(&interfaces.DiscoverSchedule{ID: "bad", CronExpr: "bad cron", Enabled: true})

		require.Error(t, err)
		assert.Empty(t, sw.scheduleEntries)
	})
}

func TestScheduleWorkerStartReloadAndUpdate(t *testing.T) {
	t.Run("start loads enabled schedules", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{
			enabledSchedules: []*interfaces.DiscoverSchedule{{ID: "schedule-1", CronExpr: "* * * * *", Enabled: true}},
		}
		sw := newTestScheduleWorker(svc)
		defer sw.Stop()

		require.NoError(t, sw.Start())
		assert.Len(t, sw.scheduleEntries, 1)
	})

	t.Run("reload replaces entries", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{
			enabledSchedules: []*interfaces.DiscoverSchedule{{ID: "schedule-2", CronExpr: "* * * * *", Enabled: true}},
		}
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["old"] = cron.EntryID(99)

		require.NoError(t, sw.Reload())
		assert.NotContains(t, sw.scheduleEntries, "old")
		assert.Contains(t, sw.scheduleEntries, "schedule-2")
	})

	t.Run("schedule skips disabled service result", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{byID: map[string]*interfaces.DiscoverSchedule{
			"schedule-1": {ID: "schedule-1", CronExpr: "* * * * *", Enabled: false},
		}}
		sw := newTestScheduleWorker(svc)

		require.NoError(t, sw.Schedule("schedule-1"))
		assert.Empty(t, sw.scheduleEntries)
	})

	t.Run("update schedule reschedules enabled result", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{byID: map[string]*interfaces.DiscoverSchedule{
			"schedule-1": {ID: "schedule-1", CronExpr: "* * * * *", Enabled: true},
		}}
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["schedule-1"] = cron.EntryID(99)

		require.NoError(t, sw.UpdateSchedule("schedule-1"))
		assert.Contains(t, sw.scheduleEntries, "schedule-1")
	})

	t.Run("service load error", func(t *testing.T) {
		sw := newTestScheduleWorker(&fakeDiscoverScheduleService{enabledErr: errors.New("db down")})

		err := sw.Start()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}

func TestScheduleWorkerExecuteSchedule(t *testing.T) {
	t.Run("disabled schedule is unscheduled", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{byID: map[string]*interfaces.DiscoverSchedule{
			"schedule-1": {ID: "schedule-1", Enabled: false},
		}}
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["schedule-1"] = cron.EntryID(1)

		sw.executeSchedule("schedule-1")

		assert.NotContains(t, sw.scheduleEntries, "schedule-1")
		assert.Zero(t, svc.executeCount)
	})

	t.Run("future start time skips execution", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{byID: map[string]*interfaces.DiscoverSchedule{
			"schedule-1": {ID: "schedule-1", Enabled: true, StartTime: time.Now().Add(time.Hour).UnixMilli()},
		}}
		sw := newTestScheduleWorker(svc)

		sw.executeSchedule("schedule-1")

		assert.Zero(t, svc.executeCount)
	})

	t.Run("expired schedule disables and unschedules", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{byID: map[string]*interfaces.DiscoverSchedule{
			"schedule-1": {ID: "schedule-1", Enabled: true, EndTime: time.Now().Add(-time.Hour).UnixMilli()},
		}}
		sw := newTestScheduleWorker(svc)
		sw.scheduleEntries["schedule-1"] = cron.EntryID(1)

		sw.executeSchedule("schedule-1")

		assert.Equal(t, []string{"schedule-1"}, svc.disabledIDs)
		assert.NotContains(t, sw.scheduleEntries, "schedule-1")
		assert.Zero(t, svc.executeCount)
	})

	t.Run("enabled schedule executes", func(t *testing.T) {
		svc := &fakeDiscoverScheduleService{byID: map[string]*interfaces.DiscoverSchedule{
			"schedule-1": {ID: "schedule-1", Enabled: true},
		}}
		sw := newTestScheduleWorker(svc)

		sw.executeSchedule("schedule-1")

		assert.Equal(t, 1, svc.executeCount)
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

type fakeDiscoverScheduleService struct {
	enabledSchedules []*interfaces.DiscoverSchedule
	enabledErr       error
	byID             map[string]*interfaces.DiscoverSchedule
	getErr           error
	disabledIDs      []string
	executeCount     int
	executeErr       error
}

func (f *fakeDiscoverScheduleService) Create(context.Context, *interfaces.DiscoverScheduleRequest) (string, error) {
	return "", nil
}

func (f *fakeDiscoverScheduleService) GetByID(_ context.Context, id string) (*interfaces.DiscoverSchedule, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.byID[id], nil
}

func (f *fakeDiscoverScheduleService) List(context.Context, interfaces.DiscoverScheduleQueryParams) ([]*interfaces.DiscoverSchedule, int64, error) {
	return nil, 0, nil
}

func (f *fakeDiscoverScheduleService) Update(context.Context, *interfaces.DiscoverSchedule, *interfaces.DiscoverScheduleRequest) error {
	return nil
}

func (f *fakeDiscoverScheduleService) Delete(context.Context, string) error {
	return nil
}

func (f *fakeDiscoverScheduleService) Enable(context.Context, string) error {
	return nil
}

func (f *fakeDiscoverScheduleService) Disable(_ context.Context, id string) error {
	f.disabledIDs = append(f.disabledIDs, id)
	return nil
}

func (f *fakeDiscoverScheduleService) GetEnabledSchedules(context.Context) ([]*interfaces.DiscoverSchedule, error) {
	return f.enabledSchedules, f.enabledErr
}

func (f *fakeDiscoverScheduleService) UpdateLastRun(context.Context, string, int64) error {
	return nil
}

func (f *fakeDiscoverScheduleService) ExecuteSchedule(context.Context, *interfaces.DiscoverSchedule) error {
	f.executeCount++
	return f.executeErr
}
