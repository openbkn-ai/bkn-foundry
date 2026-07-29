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
	return &CatalogHealthCheckWorker{
		appSetting: appSetting,
		cs:         cs,
		chcsa:      chcsa,
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
		cs.EXPECT().TestConnection(gomock.Any(), &interfaces.Catalog{ID: "catalog-1", ConnectorType: "scheduled"}).Do(
			func(context.Context, *interfaces.Catalog) { panic("connector panic") },
		),
		cs.EXPECT().TestConnection(gomock.Any(), &interfaces.Catalog{ID: "catalog-2", ConnectorType: "scheduled"}).Return(&interfaces.CatalogHealthCheckStatus{}, nil),
		sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-2", gomock.Any(), gomock.Any()).Return(nil),
	)

	assert.NotPanics(t, w.runDue)
}

func TestCatalogHealthCheckWorkerRunCatalogHealthCheck(t *testing.T) {
	t.Run("runs due schedule and advances runtime metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		cs := vmock.NewMockCatalogService(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{CatalogHealthCheck: common.CatalogHealthCheckConfig{CronExpr: "0 * * * *"}}, cs, sa)
		schedule := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}

		cs.EXPECT().TestConnection(gomock.Any(), &interfaces.Catalog{ID: "catalog-1", ConnectorType: "scheduled"}).Return(&interfaces.CatalogHealthCheckStatus{}, nil)
		sa.EXPECT().UpdateRunMetadata(gomock.Any(), "catalog-1", gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, lastRun, nextRun int64) error {
				assert.Greater(t, lastRun, int64(0))
				assert.Greater(t, nextRun, lastRun)
				return nil
			},
		)

		w.runCatalogHealthCheck(context.Background(), schedule)
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

		w.runCatalogHealthCheck(context.Background(), schedule)
	})

	t.Run("leaves schedule due when connection execution fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		cs := vmock.NewMockCatalogService(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa)
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}
		cs.EXPECT().TestConnection(gomock.Any(), gomock.Any()).Return(nil, errors.New("connection unavailable"))

		w.runCatalogHealthCheck(context.Background(), schedule)
	})
}

func TestCatalogHealthCheckWorkerStart(t *testing.T) {
	t.Run("does not start when disabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl))

		require.NoError(t, w.Start())
	})

	t.Run("rejects invalid platform cron", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		w := newCatalogHealthCheckWorker(&common.AppSetting{CatalogHealthCheck: common.CatalogHealthCheckConfig{
			WorkerEnabled: true,
			CronExpr:      "not a cron",
		}}, vmock.NewMockCatalogService(ctrl), vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl))

		require.Error(t, w.Start())
	})
}

func TestCatalogHealthCheckWorkerDefaultCronExpr(t *testing.T) {
	w := newCatalogHealthCheckWorker(nil, nil, nil)
	assert.Equal(t, catalogHealthCheckDefaultCronExpr, w.defaultCronExpr())
	assert.Equal(t, time.Hour, cronDuration(t, w.defaultCronExpr()))
}

func cronDuration(t *testing.T, expr string) time.Duration {
	t.Helper()
	schedule, err := cron.ParseStandard(expr)
	require.NoError(t, err)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return schedule.Next(now).Sub(now)
}
