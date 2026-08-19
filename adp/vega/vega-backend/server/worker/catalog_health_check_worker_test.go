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

func newCatalogHealthCheckWorker(appSetting *common.AppSetting, cs interfaces.CatalogService,
	chcsa interfaces.CatalogHealthCheckScheduleAccess) *CatalogHealthCheckWorker {
	defaultCronExpr := catalogHealthCheckDefaultCronExpr
	if appSetting.CatalogHealthCheck.CronExpr != "" {
		defaultCronExpr = appSetting.CatalogHealthCheck.CronExpr
	}
	defaultCronSchedule, err := cron.ParseStandard(defaultCronExpr)
	if err != nil {
		panic(err)
	}
	return &CatalogHealthCheckWorker{
		appSetting:          appSetting,
		defaultCronSchedule: defaultCronSchedule,
		cs:                  cs,
		chcsa:               chcsa,
	}
}

func TestCatalogHealthCheckWorkerRunDue(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
	w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), sa)

	sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return([]*interfaces.CatalogHealthCheckSchedule{
		{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeDisabled},
	}, nil)

	w.runDue()
}

func TestCatalogHealthCheckWorkerRunDueRecoversAccessPanic(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
	w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), sa)
	sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Do(
		func(context.Context, int64) { panic("access panic") },
	)

	assert.NotPanics(t, w.runDue)
}

func TestCatalogHealthCheckWorkerRunDueContinuesAfterSchedulePanic(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	cs := vmock.NewMockCatalogService(ctrl)
	sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
	w := newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa)
	first := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}
	second := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-2", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}

	gomock.InOrder(
		sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return([]*interfaces.CatalogHealthCheckSchedule{first, second}, nil),
		sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-1", first.UpdateTime, first.NextRun, gomock.Any(), gomock.Any()).Return(int64(1), nil),
		cs.EXPECT().InternalTestConnection(gomock.Any(), "catalog-1").Do(
			func(context.Context, string) { panic("connector panic") },
		),
		sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-2", second.UpdateTime, second.NextRun, gomock.Any(), gomock.Any()).Return(int64(1), nil),
		cs.EXPECT().InternalTestConnection(gomock.Any(), "catalog-2").Return(&interfaces.CatalogHealthCheckStatus{}, nil),
	)

	assert.NotPanics(t, w.runDue)
}

func TestCatalogHealthCheckWorkerRunSchedule(t *testing.T) {
	t.Run("runs due schedule and advances runtime metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		cs := vmock.NewMockCatalogService(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{CatalogHealthCheck: common.CatalogHealthCheckConfig{CronExpr: "0 * * * *"}}, cs, sa)
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID:  "catalog-1",
			Mode:       interfaces.CatalogHealthCheckScheduleModeInherit,
			UpdateTime: 123,
		}

		gomock.InOrder(
			sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-1", int64(123), schedule.NextRun, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, _, _ int64, lastRun, nextRun int64) (int64, error) {
					assert.Greater(t, lastRun, int64(0))
					assert.Greater(t, nextRun, lastRun)
					return 1, nil
				},
			),
			cs.EXPECT().InternalTestConnection(gomock.Any(), "catalog-1").Return(&interfaces.CatalogHealthCheckStatus{}, nil),
		)

		w.runSchedule(context.Background(), schedule)
	})

	t.Run("does not run disabled schedule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), sa)
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeDisabled,
		}

		w.runSchedule(context.Background(), schedule)
	})

	t.Run("advances schedule before connection execution", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		cs := vmock.NewMockCatalogService(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa)
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}
		gomock.InOrder(
			sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-1", schedule.UpdateTime, schedule.NextRun, gomock.Any(), gomock.Any()).Return(int64(1), nil),
			cs.EXPECT().InternalTestConnection(gomock.Any(), "catalog-1").Return(nil, errors.New("connection unavailable")),
		)

		w.runSchedule(context.Background(), schedule)
	})

	t.Run("does not run when advancing schedule fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		cs := vmock.NewMockCatalogService(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa)
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}
		sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-1", schedule.UpdateTime, schedule.NextRun, gomock.Any(), gomock.Any()).
			Return(int64(0), errors.New("database unavailable"))

		w.runSchedule(context.Background(), schedule)
	})

	t.Run("does not run when schedule was already claimed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		cs := vmock.NewMockCatalogService(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa)
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}
		sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-1", schedule.UpdateTime, schedule.NextRun, gomock.Any(), gomock.Any()).
			Return(int64(0), nil)

		w.runSchedule(context.Background(), schedule)
	})
}

func TestCatalogHealthCheckWorkerStart(t *testing.T) {
	t.Run("does not start when disabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl))

		require.NoError(t, w.Start())
	})

	t.Run("reschedules future inherit checks before running", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(
			&common.AppSetting{CatalogHealthCheck: common.CatalogHealthCheckConfig{
				WorkerEnabled: true,
				CronExpr:      "0 * * * *",
			}},
			vmock.NewMockCatalogService(ctrl),
			sa,
		)
		runStarted := make(chan struct{})

		gomock.InOrder(
			sa.EXPECT().UpdateInheritedNextRun(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, now, nextRun int64) error {
					assert.Greater(t, nextRun, now)
					assert.Equal(t, 0, time.UnixMilli(nextRun).Minute())
					return nil
				}),
			sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, int64) ([]*interfaces.CatalogHealthCheckSchedule, error) {
					close(runStarted)
					return nil, errors.New("stop test run")
				}),
		)

		require.NoError(t, w.Start())
		select {
		case <-runStarted:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	})

	t.Run("does not start when rescheduling fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(
			&common.AppSetting{CatalogHealthCheck: common.CatalogHealthCheckConfig{WorkerEnabled: true}},
			vmock.NewMockCatalogService(ctrl),
			sa,
		)
		updateErr := errors.New("database unavailable")
		sa.EXPECT().UpdateInheritedNextRun(gomock.Any(), gomock.Any(), gomock.Any()).Return(updateErr)

		err := w.Start()

		require.ErrorIs(t, err, updateErr)
	})
}
