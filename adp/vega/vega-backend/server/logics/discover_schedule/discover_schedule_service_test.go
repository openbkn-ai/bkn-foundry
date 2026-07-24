// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_schedule

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

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
			Update(gomock.Any(), current).
			DoAndReturn(func(_ context.Context, schedule *interfaces.DiscoverSchedule) error {
				assert.Equal(t, "new", schedule.Name)
				assert.Equal(t, "0 1 * * *", schedule.CronExpr)
				assert.Equal(t, int64(100), schedule.StartTime)
				assert.Equal(t, int64(200), schedule.EndTime)
				assert.Equal(t, "create_only", schedule.Strategy)
				assert.Equal(t, account, schedule.Updater)
				assert.NotZero(t, schedule.UpdateTime)
				return nil
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

	t.Run("delegates enable disable delete and last run", func(t *testing.T) {
		service, dsa, _, _ := newTestDiscoverScheduleService(t)
		dsa.EXPECT().Enable(gomock.Any(), "schedule-1").Return(nil)
		dsa.EXPECT().Disable(gomock.Any(), "schedule-1").Return(nil)
		dsa.EXPECT().UpdateLastRun(gomock.Any(), "schedule-1", int64(123)).Return(nil)
		dsa.EXPECT().Delete(gomock.Any(), "schedule-1").Return(nil)

		require.NoError(t, service.Enable(context.Background(), "schedule-1"))
		require.NoError(t, service.Disable(context.Background(), "schedule-1"))
		require.NoError(t, service.UpdateLastRun(context.Background(), "schedule-1", 123))
		require.NoError(t, service.Delete(context.Background(), "schedule-1"))
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
}

func TestDiscoverScheduleServiceExecuteSchedule(t *testing.T) {
	t.Run("rejects nil discover task service", func(t *testing.T) {
		service := &discoverScheduleService{}

		err := service.ExecuteSchedule(context.Background(), &interfaces.DiscoverSchedule{ID: "schedule-1"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "DiscoverTaskService not set")
	})

	t.Run("skips when scheduled task is already running", func(t *testing.T) {
		service, _, dts, _ := newTestDiscoverScheduleService(t)
		schedule := &interfaces.DiscoverSchedule{ID: "schedule-1", CatalogID: "catalog-1"}
		dts.EXPECT().
			List(gomock.Any(), interfaces.DiscoverTaskQueryParams{
				CatalogID:   "catalog-1",
				Status:      interfaces.DiscoverTaskStatusRunning,
				TriggerType: interfaces.DiscoverTaskTriggerScheduled,
			}).
			Return(nil, int64(1), nil)

		require.NoError(t, service.ExecuteSchedule(context.Background(), schedule))
	})

	t.Run("creates scheduled task and updates last run", func(t *testing.T) {
		service, dsa, dts, _ := newTestDiscoverScheduleService(t)
		schedule := &interfaces.DiscoverSchedule{
			ID:        "schedule-1",
			CatalogID: "catalog-1",
			Strategy:  "full_sync",
			Creator:   interfaces.AccountInfo{ID: "u1"},
		}

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
		dsa.EXPECT().
			UpdateLastRun(gomock.Any(), "schedule-1", gomock.Any()).
			Return(nil)

		require.NoError(t, service.ExecuteSchedule(context.Background(), schedule))
	})

	t.Run("returns list error", func(t *testing.T) {
		service, _, dts, _ := newTestDiscoverScheduleService(t)
		dts.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("list failed"))

		err := service.ExecuteSchedule(context.Background(), &interfaces.DiscoverSchedule{ID: "schedule-1"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list failed")
	})
}
