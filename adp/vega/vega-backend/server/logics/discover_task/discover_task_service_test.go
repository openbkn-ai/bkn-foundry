// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_task

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

func newTestDiscoverTaskService(t *testing.T) (*discoverTaskService, *vmock.MockDiscoverTaskAccess, *vmock.MockUserMgmtService) {
	t.Helper()

	ctrl := gomock.NewController(t)
	dta := vmock.NewMockDiscoverTaskAccess(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)

	return &discoverTaskService{
		dta: dta,
		ums: ums,
	}, dta, ums
}

func TestDiscoverTaskServiceGetAndList(t *testing.T) {
	t.Run("get enriches creator name", func(t *testing.T) {
		service, dta, ums := newTestDiscoverTaskService(t)
		task := &interfaces.DiscoverTask{
			ID:      "task-1",
			Creator: interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		}

		dta.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		ums.EXPECT().
			GetAccountNames(gomock.Any(), []*interfaces.AccountInfo{&task.Creator}).
			DoAndReturn(func(_ context.Context, accountInfos []*interfaces.AccountInfo) error {
				accountInfos[0].Name = "Alice"
				return nil
			})

		got, err := service.GetByID(context.Background(), "task-1")

		require.NoError(t, err)
		require.Same(t, task, got)
		assert.Equal(t, "Alice", got.Creator.Name)
	})

	t.Run("get returns not found without account lookup", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		got, err := service.GetByID(context.Background(), "missing")

		assert.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotFound")
	})

	t.Run("list enriches creators", func(t *testing.T) {
		service, dta, ums := newTestDiscoverTaskService(t)
		params := interfaces.DiscoverTaskQueryParams{CatalogID: "catalog-1"}
		tasks := []*interfaces.DiscoverTask{
			{ID: "task-1", Creator: interfaces.AccountInfo{ID: "u1"}},
			{ID: "task-2", Creator: interfaces.AccountInfo{ID: "u2"}},
		}

		dta.EXPECT().List(gomock.Any(), params).Return(tasks, int64(2), nil)
		ums.EXPECT().
			GetAccountNames(gomock.Any(), gomock.Len(2)).
			DoAndReturn(func(_ context.Context, accountInfos []*interfaces.AccountInfo) error {
				accountInfos[0].Name = "Alice"
				accountInfos[1].Name = "Bob"
				return nil
			})

		got, total, err := service.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		assert.Equal(t, "Alice", got[0].Creator.Name)
		assert.Equal(t, "Bob", got[1].Creator.Name)
	})

	t.Run("list wraps account lookup error", func(t *testing.T) {
		service, dta, ums := newTestDiscoverTaskService(t)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).
			Return([]*interfaces.DiscoverTask{{ID: "task-1"}}, int64(1), nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user service down"))

		got, total, err := service.List(context.Background(), interfaces.DiscoverTaskQueryParams{})

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Zero(t, total)
		assert.Contains(t, err.Error(), "user service down")
	})
}

func TestDiscoverTaskServiceUpdateAndExistence(t *testing.T) {
	t.Run("delegates status update", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().
			UpdateStatus(gomock.Any(), "task-1", interfaces.DiscoverTaskStatusRunning, "started", int64(100)).
			Return(nil)

		require.NoError(t, service.UpdateStatus(context.Background(), "task-1", interfaces.DiscoverTaskStatusRunning, "started", 100))
	})

	t.Run("delegates result update", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		result := &interfaces.DiscoverResult{}
		dta.EXPECT().UpdateResult(gomock.Any(), "task-1", result, int64(200)).Return(nil)

		require.NoError(t, service.UpdateResult(context.Background(), "task-1", result, 200))
	})

	t.Run("checks existence by statuses", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		statuses := []string{interfaces.DiscoverTaskStatusPending, interfaces.DiscoverTaskStatusRunning}
		dta.EXPECT().CheckExistByStatuses(gomock.Any(), "catalog-1", statuses).Return(true, nil)

		exists, err := service.CheckExistByStatuses(context.Background(), "catalog-1", statuses)

		require.NoError(t, err)
		assert.True(t, exists)
	})
}

func TestDiscoverTaskServiceDelete(t *testing.T) {
	t.Run("deduplicates ids and deletes completed tasks", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.DiscoverTask{ID: "task-1", Status: interfaces.DiscoverTaskStatusCompleted}, nil)
		dta.EXPECT().GetByID(gomock.Any(), "task-2").
			Return(&interfaces.DiscoverTask{ID: "task-2", Status: interfaces.DiscoverTaskStatusFailed}, nil)
		dta.EXPECT().Delete(gomock.Any(), "task-1").Return(nil)
		dta.EXPECT().Delete(gomock.Any(), "task-2").Return(nil)

		require.NoError(t, service.Delete(context.Background(), []string{"task-1", "task-1", "task-2"}, false))
	})

	t.Run("rejects pending or running tasks", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.DiscoverTask{ID: "task-1", Status: interfaces.DiscoverTaskStatusRunning}, nil)

		err := service.Delete(context.Background(), []string{"task-1"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "HasRunningExecution")
		assert.Contains(t, err.Error(), "task-1")
	})

	t.Run("missing ids fail unless ignored", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		err := service.Delete(context.Background(), []string{"missing"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotFound")
		assert.Contains(t, err.Error(), "missing")
	})

	t.Run("ignore missing deletes existing ids only", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)
		dta.EXPECT().GetByID(gomock.Any(), "done").
			Return(&interfaces.DiscoverTask{ID: "done", Status: interfaces.DiscoverTaskStatusCompleted}, nil)
		dta.EXPECT().Delete(gomock.Any(), "done").Return(nil)

		require.NoError(t, service.Delete(context.Background(), []string{"missing", "done"}, true))
	})

	t.Run("wraps get failure", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "task-1").Return(nil, errors.New("db down"))

		err := service.Delete(context.Background(), []string{"task-1"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}
