// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_schedule

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func newTestDiscoverScheduleService(t *testing.T) (*discoverScheduleService, *vmock.MockDiscoverScheduleAccess, *vmock.MockDiscoverTaskService, *vmock.MockUserMgmtService) {
	t.Helper()

	ctrl := gomock.NewController(t)
	dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)

	return &discoverScheduleService{
		dsa: dsa,
		dts: dts,
		ums: ums,
	}, dsa, dts, ums
}

func TestCalculateScheduleNextRun(t *testing.T) {
	now := time.Date(2026, 7, 9, 9, 30, 0, 0, time.UTC)

	t.Run("uses next cron occurrence when start time is not in the future", func(t *testing.T) {
		next, err := calculateScheduleNextRun("0 * * * *", now, now.Add(-time.Hour).UnixMilli())

		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC), next)
	})

	t.Run("does not run immediately after an off-cron future start time", func(t *testing.T) {
		startTime := time.Date(2026, 7, 9, 10, 15, 0, 0, time.UTC)

		next, err := calculateScheduleNextRun("0 * * * *", now, startTime.UnixMilli())

		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 7, 9, 11, 0, 0, 0, time.UTC), next)
	})

	t.Run("includes a cron occurrence exactly at the future start time", func(t *testing.T) {
		startTime := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

		next, err := calculateScheduleNextRun("0 * * * *", now, startTime.UnixMilli())

		require.NoError(t, err)
		assert.Equal(t, startTime, next)
	})

	t.Run("rejects invalid or too frequent cron expressions", func(t *testing.T) {
		_, invalidErr := calculateScheduleNextRun("bad cron", now, 0)
		_, frequentErr := calculateScheduleNextRun("*/30 * * * *", now, 0)

		require.Error(t, invalidErr)
		require.ErrorContains(t, frequentErr, "minimum interval is 1 hour")
	})
}

func TestDiscoverScheduleServiceCreateAndUpdate(t *testing.T) {
	t.Run("create rejects empty cron", func(t *testing.T) {
		service, _, _, _ := newTestDiscoverScheduleService(t)

		id, err := service.Create(context.Background(), &interfaces.DiscoverScheduleRequest{Name: "nightly"})

		require.Error(t, err)
		assert.Empty(t, id)
		assert.Contains(t, err.Error(), "cron_expr is required")
	})

	t.Run("create persists request and account info", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		account := interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, account)

		dsa.EXPECT().
			Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.DiscoverSchedule{})).
			DoAndReturn(func(_ context.Context, schedule *interfaces.DiscoverSchedule) error {
				assert.NotEmpty(t, schedule.ID)
				assert.Equal(t, "nightly", schedule.Name)
				assert.Equal(t, "catalog-1", schedule.CatalogID)
				assert.Equal(t, "0 0 * * *", schedule.CronExpr)
				assert.True(t, schedule.Enabled)
				assert.Equal(t, "full_sync", schedule.Strategy)
				assert.Equal(t, account, schedule.Creator)
				assert.Equal(t, account, schedule.Updater)
				assert.NotZero(t, schedule.CreateTime)
				assert.NotZero(t, schedule.UpdateTime)
				assert.Greater(t, schedule.NextRun, schedule.CreateTime)
				return nil
			})

		id, err := service.Create(ctx, &interfaces.DiscoverScheduleRequest{
			Name:      "nightly",
			CatalogID: "catalog-1",
			CronExpr:  "0 0 * * *",
			Enabled:   true,
			Strategy:  "full_sync",
		})

		require.NoError(t, err)
		assert.NotEmpty(t, id)
	})

	t.Run("update mutates existing schedule", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		account := interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER}
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, account)
		current := &interfaces.DiscoverSchedule{
			ID:        "schedule-1",
			Name:      "old",
			CatalogID: "catalog-1",
			CronExpr:  "0 0 * * *",
		}

		dsa.EXPECT().
			Update(gomock.Any(), current, int64(0)).
			DoAndReturn(func(_ context.Context, schedule *interfaces.DiscoverSchedule, _ int64) (int64, error) {
				assert.Equal(t, "new", schedule.Name)
				assert.Equal(t, "0 1 * * *", schedule.CronExpr)
				assert.Equal(t, int64(100), schedule.StartTime)
				assert.Equal(t, int64(200), schedule.EndTime)
				assert.Equal(t, "create_only", schedule.Strategy)
				assert.Equal(t, account, schedule.Updater)
				assert.NotZero(t, schedule.UpdateTime)
				assert.Greater(t, schedule.NextRun, schedule.UpdateTime)
				return 1, nil
			})

		err := service.Update(ctx, current, &interfaces.DiscoverScheduleRequest{
			Name:      "new",
			CronExpr:  "0 1 * * *",
			StartTime: 100,
			EndTime:   200,
			Strategy:  "create_only",
		})

		require.NoError(t, err)
	})

	t.Run("returns conflict for stale schedule", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		current := &interfaces.DiscoverSchedule{ID: "schedule-1", CatalogID: "catalog-1", CronExpr: "0 0 * * *"}
		expectedUpdateTime := int64(42)
		dsa.EXPECT().Update(gomock.Any(), current, expectedUpdateTime).
			DoAndReturn(func(_ context.Context, schedule *interfaces.DiscoverSchedule, expected int64) (int64, error) {
				assert.Equal(t, expectedUpdateTime, expected)
				assert.Greater(t, schedule.UpdateTime, expectedUpdateTime)
				return 0, nil
			})

		err := service.Update(context.Background(), current, &interfaces.DiscoverScheduleRequest{
			Name:               "nightly",
			CronExpr:           "0 1 * * *",
			Strategy:           "full_sync",
			ExpectedUpdateTime: expectedUpdateTime,
		})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_DiscoverSchedule_UpdateConflict, httpErr.BaseError.ErrorCode)
	})

	t.Run("returns conflict when no schedule is updated", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		current := &interfaces.DiscoverSchedule{ID: "schedule-1", CatalogID: "catalog-1", CronExpr: "0 0 * * *"}
		dsa.EXPECT().Update(gomock.Any(), current, int64(0)).Return(int64(0), nil)

		err := service.Update(context.Background(), current, &interfaces.DiscoverScheduleRequest{
			Name:     "nightly",
			CronExpr: "0 1 * * *",
			Strategy: "full_sync",
		})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_DiscoverSchedule_UpdateConflict, httpErr.BaseError.ErrorCode)
	})

	t.Run("update rejects nil schedule", func(t *testing.T) {
		service, _, _, _ := newTestDiscoverScheduleService(t)

		err := service.Update(context.Background(), nil, &interfaces.DiscoverScheduleRequest{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDiscoverScheduleServiceGetListAndSimpleDelegates(t *testing.T) {
	t.Run("get enriches creator and updater", func(t *testing.T) {
		service, dsa, _, ums := newTestDiscoverScheduleService(t)
		schedule := &interfaces.DiscoverSchedule{
			ID:      "schedule-1",
			Creator: interfaces.AccountInfo{ID: "u1"},
			Updater: interfaces.AccountInfo{ID: "u2"},
		}

		dsa.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(schedule, nil)
		ums.EXPECT().
			GetAccountNames(gomock.Any(), []*interfaces.AccountInfo{&schedule.Creator, &schedule.Updater}).
			Return(nil)

		got, err := service.GetByID(context.Background(), "schedule-1")

		require.NoError(t, err)
		assert.Same(t, schedule, got)
	})

	t.Run("get wraps access error", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		dsa.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(nil, errors.New("database unavailable"))

		got, err := service.GetByID(context.Background(), "schedule-1")

		require.Nil(t, got)
		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, verrors.VegaBackend_DiscoverSchedule_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
	})

	t.Run("list enriches all creator and updater accounts", func(t *testing.T) {
		service, dsa, _, ums := newTestDiscoverScheduleService(t)
		params := interfaces.DiscoverScheduleQueryParams{CatalogID: "catalog-1"}
		schedules := []*interfaces.DiscoverSchedule{
			{ID: "s1", Creator: interfaces.AccountInfo{ID: "u1"}, Updater: interfaces.AccountInfo{ID: "u2"}},
			{ID: "s2", Creator: interfaces.AccountInfo{ID: "u3"}, Updater: interfaces.AccountInfo{ID: "u4"}},
		}

		dsa.EXPECT().List(gomock.Any(), params).Return(schedules, int64(2), nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(4)).Return(nil)

		got, total, err := service.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		assert.Equal(t, schedules, got)
	})

	t.Run("list wraps access error", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("database unavailable"))

		got, total, err := service.List(context.Background(), interfaces.DiscoverScheduleQueryParams{})

		require.Nil(t, got)
		assert.Zero(t, total)
		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, verrors.VegaBackend_DiscoverSchedule_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
	})

	t.Run("delegates enable disable delete and run metadata", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		schedule := &interfaces.DiscoverSchedule{
			ID:         "schedule-1",
			CronExpr:   "0 * * * *",
			UpdateTime: 100,
		}
		dsa.EXPECT().UpdateEnabled(gomock.Any(), "schedule-1", true, gomock.Not(gomock.Nil()), int64(100), gomock.Any(), gomock.Any()).Return(int64(1), nil)
		dsa.EXPECT().UpdateEnabled(gomock.Any(), "schedule-1", false, nil, int64(100), gomock.Any(), gomock.Any()).Return(int64(1), nil)
		dsa.EXPECT().UpdateRunMetadata(gomock.Any(), "schedule-1", int64(100), int64(110), int64(123), int64(456)).Return(int64(1), nil)
		dsa.EXPECT().Delete(gomock.Any(), "schedule-1").Return(nil)

		require.NoError(t, service.UpdateEnabled(context.Background(), schedule, true))
		require.NoError(t, service.UpdateEnabled(context.Background(), schedule, false))
		rowsAffected, err := service.UpdateRunMetadata(context.Background(), "schedule-1", 100, 110, 123, 456)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, service.Delete(context.Background(), "schedule-1"))
	})

	t.Run("returns conflict when enabled state was based on a stale schedule", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		schedule := &interfaces.DiscoverSchedule{ID: "schedule-1", UpdateTime: 100}
		dsa.EXPECT().UpdateEnabled(gomock.Any(), "schedule-1", false, nil, int64(100), gomock.Any(), gomock.Any()).Return(int64(0), nil)

		err := service.UpdateEnabled(context.Background(), schedule, false)

		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_DiscoverSchedule_UpdateConflict, httpErr.BaseError.ErrorCode)
	})

	t.Run("rejects a nil schedule", func(t *testing.T) {
		service, _, _, _ := newTestDiscoverScheduleService(t)

		require.Error(t, service.UpdateEnabled(context.Background(), nil, false))
	})
}

func TestDiscoverScheduleServicePopulatesCatalogName(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)
	service := &discoverScheduleService{dsa: dsa, cs: cs, ums: ums}

	t.Run("list batches current page catalog ids", func(t *testing.T) {
		schedules := []*interfaces.DiscoverSchedule{
			{ID: "schedule-1", CatalogID: "catalog-1"},
			{ID: "schedule-2", CatalogID: "catalog-1"},
		}
		dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(schedules, int64(2), nil)
		cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return([]*interfaces.Catalog{{ID: "catalog-1", Name: "目录一"}}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(4)).Return(nil)

		got, _, err := service.List(context.Background(), interfaces.DiscoverScheduleQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, "目录一", got[0].CatalogName)
		assert.Equal(t, "目录一", got[1].CatalogName)
	})

	t.Run("get populates catalog name", func(t *testing.T) {
		schedule := &interfaces.DiscoverSchedule{ID: "schedule-3", CatalogID: "catalog-2"}
		dsa.EXPECT().GetByID(gomock.Any(), "schedule-3").Return(schedule, nil)
		cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-2"}).Return([]*interfaces.Catalog{{ID: "catalog-2", Name: "目录二"}}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, err := service.GetByID(context.Background(), "schedule-3")

		require.NoError(t, err)
		assert.Equal(t, "目录二", got.CatalogName)
	})

	t.Run("list keeps schedules when reference lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		ums := vmock.NewMockUserMgmtService(ctrl)
		service := &discoverScheduleService{dsa: dsa, cs: cs, ums: ums}
		schedules := []*interfaces.DiscoverSchedule{{ID: "schedule-4", CatalogID: "catalog-3"}}

		dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(schedules, int64(1), nil)
		cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-3"}).Return(nil, errors.New("catalog service down"))
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, total, err := service.List(context.Background(), interfaces.DiscoverScheduleQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, "schedule-4", got[0].ID)
		assert.Empty(t, got[0].CatalogName)
	})

	t.Run("get keeps schedule when account lookup fails", func(t *testing.T) {
		schedule := &interfaces.DiscoverSchedule{ID: "schedule-5"}
		dsa.EXPECT().GetByID(gomock.Any(), "schedule-5").Return(schedule, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user service down"))

		got, err := service.GetByID(context.Background(), "schedule-5")

		require.NoError(t, err)
		assert.Equal(t, "schedule-5", got.ID)
	})
}

func TestDiscoverScheduleServiceExecuteSchedule(t *testing.T) {
	t.Run("rejects nil discover task service", func(t *testing.T) {
		service := &discoverScheduleService{}

		err := service.ExecuteSchedule(context.Background(), &interfaces.DiscoverSchedule{ID: "schedule-1"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "DiscoverTaskService not set")
	})

	t.Run("skips when scheduled task is already running", func(t *testing.T) {
		service, cs, dts := newTestDiscoverScheduleExecutionService(t)
		schedule := &interfaces.DiscoverSchedule{ID: "schedule-1", CatalogID: "catalog-1"}
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		dts.EXPECT().
			List(gomock.Any(), interfaces.DiscoverTaskQueryParams{
				CatalogID:   "catalog-1",
				Statuses:    []string{interfaces.DiscoverTaskStatusRunning},
				TriggerType: interfaces.DiscoverTaskTriggerScheduled,
			}).
			Return(nil, int64(1), nil)

		require.NoError(t, service.ExecuteSchedule(context.Background(), schedule))
	})

	t.Run("creates scheduled task", func(t *testing.T) {
		service, cs, dts := newTestDiscoverScheduleExecutionService(t)
		schedule := &interfaces.DiscoverSchedule{
			ID:        "schedule-1",
			CatalogID: "catalog-1",
			Strategy:  "full_sync",
			Creator:   interfaces.AccountInfo{ID: "u1"},
		}

		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		dts.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(nil, int64(0), nil)
		dts.EXPECT().
			Create(gomock.Any(), &interfaces.CreateDiscoverTaskRequest{
				CatalogID:   "catalog-1",
				TriggerType: interfaces.DiscoverTaskTriggerScheduled,
				ScheduleID:  "schedule-1",
				Strategy:    "full_sync",
			}).
			Return("task-1", nil)
		require.NoError(t, service.ExecuteSchedule(context.Background(), schedule))
	})

	t.Run("returns list error", func(t *testing.T) {
		service, cs, dts := newTestDiscoverScheduleExecutionService(t)
		cs.EXPECT().InternalGetByID(gomock.Any(), "", false).Return(&interfaces.Catalog{Enabled: true}, nil)
		dts.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("list failed"))

		err := service.ExecuteSchedule(context.Background(), &interfaces.DiscoverSchedule{ID: "schedule-1"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list failed")
	})

	t.Run("skips task creation for disabled catalog", func(t *testing.T) {
		service, cs, _ := newTestDiscoverScheduleExecutionService(t)
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		require.NoError(t, service.ExecuteSchedule(context.Background(), &interfaces.DiscoverSchedule{
			ID: "schedule-1", CatalogID: "catalog-1",
		}))
	})
}

func newTestDiscoverScheduleExecutionService(t *testing.T) (*discoverScheduleService, *vmock.MockCatalogService, *vmock.MockDiscoverTaskService) {
	t.Helper()

	ctrl := gomock.NewController(t)
	cs := vmock.NewMockCatalogService(ctrl)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	return &discoverScheduleService{cs: cs, dts: dts}, cs, dts
}
