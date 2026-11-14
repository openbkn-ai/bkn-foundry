// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
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
		stopCh:              make(chan struct{}),
	}
}

func TestCatalogHealthCheckWorkerRunDue(t *testing.T) {
	t.Run("continues after a schedule call panics", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa)
		first := &interfaces.CatalogHealthCheckSchedule{CatalogID: "first", Mode: "inherit", NextRun: 1}
		second := &interfaces.CatalogHealthCheckSchedule{CatalogID: "second", Mode: "inherit", NextRun: 1}
		gomock.InOrder(
			sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return([]*interfaces.CatalogHealthCheckSchedule{first, second}, nil),
			sa.EXPECT().ClaimRun(gomock.Any(), "first", first.UpdateTime, first.NextRun, gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil),
			cs.EXPECT().InternalTestConnection(gomock.Any(), "first").Do(func(context.Context, string) { panic("interrupted") }),
			sa.EXPECT().ClaimRun(gomock.Any(), "second", second.UpdateTime, second.NextRun, gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil),
			cs.EXPECT().InternalTestConnection(gomock.Any(), "second").Return(&interfaces.CatalogHealthCheckStatus{}, nil),
			sa.EXPECT().CompleteRun(gomock.Any(), "second", gomock.Any(), interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}).Return(int64(1), nil),
		)
		assert.NotPanics(t, func() { w.runDue(context.Background()) })
	})
	t.Run("recovers listing panic", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Do(func(context.Context, int64) { panic("interrupted") })
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), sa)
		assert.NotPanics(t, func() { w.runDue(context.Background()) })
	})
	t.Run("returns after listing fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return(nil, errors.New("unavailable"))
		newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), sa).runDue(context.Background())
	})
}

func TestCatalogHealthCheckWorkerRunScheduleAt(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 15, 0, 0, time.UTC)
	next := time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC).UnixMilli()
	newSchedule := func() *interfaces.CatalogHealthCheckSchedule {
		return &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog", Mode: "inherit", NextRun: now.Add(-3 * time.Hour).UnixMilli(), UpdateTime: 25, LastRun: 20, LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "previous failure"}
	}
	t.Run("initializes zero next run preserving last run and result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		schedule := newSchedule()
		schedule.NextRun = 0
		sa.EXPECT().InitializeNextRun(gomock.Any(), "catalog", schedule.UpdateTime, int64(0), next).Return(int64(1), nil)
		newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa).runScheduleAt(context.Background(), schedule, now)
		assert.Equal(t, int64(20), schedule.LastRun)
		assert.Equal(t, interfaces.ScheduleStatusFailed, schedule.LastStatus)
		assert.Equal(t, "previous failure", schedule.LastMessage)
	})
	for _, tc := range []struct {
		name     string
		status   string
		probeErr error
		health   string
	}{
		{name: "healthy", status: interfaces.ScheduleStatusSuccess, health: "healthy"},
		{name: "unhealthy is completed check", status: interfaces.ScheduleStatusSuccess, health: "unhealthy"},
		{name: "invocation error", status: interfaces.ScheduleStatusFailed, probeErr: errors.New("private connector details")},
		{name: "catalog disabled after due scan", status: interfaces.ScheduleStatusSkipped, probeErr: httperrors.NewHTTPError(context.Background(), http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).WithErrorDetails("private detail")},
		{name: "catalog deleted after due scan", status: interfaces.ScheduleStatusSkipped, probeErr: httperrors.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound).WithErrorDetails("private detail")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			schedule := newSchedule()
			var runID string
			gomock.InOrder(
				sa.EXPECT().ClaimRun(gomock.Any(), "catalog", schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _, _, _, _ int64, token string) (int64, error) {
					assert.NotEmpty(t, token)
					runID = token
					return 1, nil
				}),
				cs.EXPECT().InternalTestConnection(gomock.Any(), "catalog").Return(&interfaces.CatalogHealthCheckStatus{HealthCheckStatus: tc.health}, tc.probeErr),
				sa.EXPECT().CompleteRun(gomock.Any(), "catalog", gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _, token string, result interfaces.ScheduleRunResult) (int64, error) {
					assert.Equal(t, runID, token)
					assert.Equal(t, tc.status, result.Status)
					if tc.probeErr != nil {
						assert.NotEmpty(t, result.Message)
						assert.NotContains(t, result.Message, "private")
					} else {
						assert.Empty(t, result.Message)
					}
					return 1, nil
				}),
			)
			newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa).runScheduleAt(context.Background(), schedule, now)
		})
	}
	for _, tc := range []struct {
		name string
		err  error
	}{{"claim already taken", nil}, {"claim failed", errors.New("unavailable")}} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
			schedule := newSchedule()
			sa.EXPECT().ClaimRun(gomock.Any(), "catalog", schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).Return(int64(0), tc.err)
			newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), sa).runScheduleAt(context.Background(), schedule, now)
		})
	}
	for _, expr := range []string{"invalid", "*/30 * * * *", "0 0 31 2 *"} {
		t.Run("invalid persisted cron "+expr, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
			schedule := newSchedule()
			schedule.Mode = "enabled"
			schedule.CronExpr = expr
			sa.EXPECT().DisableInvalid(gomock.Any(), "catalog", schedule.UpdateTime, schedule.NextRun, gomock.Any()).Return(int64(1), nil)
			newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), sa).runScheduleAt(context.Background(), schedule, now)
		})
	}
	t.Run("completion write failure does not invoke check again", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		schedule := newSchedule()
		sa.EXPECT().ClaimRun(gomock.Any(), "catalog", schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).Return(int64(1), nil)
		cs.EXPECT().InternalTestConnection(gomock.Any(), "catalog").Return(&interfaces.CatalogHealthCheckStatus{}, nil)
		sa.EXPECT().CompleteRun(gomock.Any(), "catalog", gomock.Any(), gomock.Any()).Return(int64(0), errors.New("unavailable"))
		newCatalogHealthCheckWorker(&common.AppSetting{}, cs, sa).runScheduleAt(context.Background(), schedule, now)
	})
	t.Run("disabled and future schedules do not run", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl))
		disabled := newSchedule()
		disabled.Mode = "disabled"
		future := newSchedule()
		future.NextRun = next
		w.runScheduleAt(context.Background(), disabled, now)
		w.runScheduleAt(context.Background(), future, now)
	})
}

func TestCatalogHealthCheckWorkerStart(t *testing.T) {
	t.Run("does not start when disabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		w := newCatalogHealthCheckWorker(&common.AppSetting{}, vmock.NewMockCatalogService(ctrl), vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl))

		w.Start()
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

		w.Start()
		select {
		case <-runStarted:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
		w.Stop()
	})

	t.Run("starts when rescheduling fails", func(t *testing.T) {
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
		runStarted := make(chan struct{})
		sa.EXPECT().ListDue(gomock.Any(), gomock.Any()).
			DoAndReturn(func(context.Context, int64) ([]*interfaces.CatalogHealthCheckSchedule, error) {
				close(runStarted)
				return nil, errors.New("stop test run")
			})

		w.Start()
		select {
		case <-runStarted:
		case <-time.After(time.Second):
			t.Fatal("worker did not start after rescheduling failed")
		}
		w.Stop()
	})
}
