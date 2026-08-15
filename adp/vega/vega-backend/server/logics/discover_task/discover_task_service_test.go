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

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
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

func TestDiscoverTaskServiceCreateRequestsDispatchAfterPersistence(t *testing.T) {
	service, dta, _ := newTestDiscoverTaskService(t)
	service.dispatchCh = make(chan struct{}, discoverTaskDispatchBuffer)
	dta.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.DiscoverTask{})).
		DoAndReturn(func(_ context.Context, task *interfaces.DiscoverTask) error {
			assert.Equal(t, "catalog-1", task.CatalogID)
			assert.Equal(t, interfaces.DiscoverTaskStatusPending, task.Status)
			return nil
		})

	id, err := service.Create(context.Background(), &interfaces.CreateDiscoverTaskRequest{
		CatalogID:   "catalog-1",
		TriggerType: interfaces.DiscoverTaskTriggerManual,
		Strategy:    interfaces.DiscoverStrategyFullSync,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, id)
	select {
	case <-service.DispatchSignal():
	default:
		t.Fatal("expected a dispatch signal after the task was persisted")
	}
}

func TestDiscoverTaskServiceInternalUpdateProgress(t *testing.T) {
	service, dta, _ := newTestDiscoverTaskService(t)
	dta.EXPECT().UpdateProgress(gomock.Any(), "task-1", 70, "resources reconciled", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ int, _ string, lastProgressTime int64) (bool, error) {
			assert.Positive(t, lastProgressTime)
			return true, nil
		})

	updated, err := service.InternalUpdateProgress(context.Background(), "task-1", 70, "resources reconciled")

	require.NoError(t, err)
	assert.True(t, updated)
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

	t.Run("get wraps access error", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "task-1").Return(nil, errors.New("database unavailable"))

		got, err := service.GetByID(context.Background(), "task-1")

		require.Nil(t, got)
		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, verrors.VegaBackend_DiscoverTask_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
	})

	t.Run("list enriches creators", func(t *testing.T) {
		service, dta, ums := newTestDiscoverTaskService(t)
		params := interfaces.DiscoverTaskQueryParams{CatalogID: "catalog-1"}
		tasks := []*interfaces.DiscoverTaskSummary{
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

	t.Run("list keeps tasks when account lookup fails", func(t *testing.T) {
		service, dta, ums := newTestDiscoverTaskService(t)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).
			Return([]*interfaces.DiscoverTaskSummary{{ID: "task-1"}}, int64(1), nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user service down"))

		got, total, err := service.List(context.Background(), interfaces.DiscoverTaskQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, "task-1", got[0].ID)
	})

	t.Run("list wraps access error", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("database unavailable"))

		got, total, err := service.List(context.Background(), interfaces.DiscoverTaskQueryParams{})

		require.Nil(t, got)
		assert.Zero(t, total)
		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, verrors.VegaBackend_DiscoverTask_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
	})
}

func TestDiscoverTaskServicePopulatesCatalogName(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dta := vmock.NewMockDiscoverTaskAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)
	service := &discoverTaskService{dta: dta, cs: cs, ums: ums}

	t.Run("list batches current page catalog ids", func(t *testing.T) {
		tasks := []*interfaces.DiscoverTaskSummary{
			{ID: "task-1", CatalogID: "catalog-1"},
			{ID: "task-2", CatalogID: "catalog-1"},
		}
		dta.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(2), nil)
		cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return([]*interfaces.Catalog{{ID: "catalog-1", Name: "目录一"}}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(2)).Return(nil)

		got, _, err := service.List(context.Background(), interfaces.DiscoverTaskQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, "目录一", got[0].CatalogName)
		assert.Equal(t, "目录一", got[1].CatalogName)
	})

	t.Run("get populates catalog name", func(t *testing.T) {
		task := &interfaces.DiscoverTask{ID: "task-3", CatalogID: "catalog-2"}
		dta.EXPECT().GetByID(gomock.Any(), "task-3").Return(task, nil)
		cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-2"}).Return([]*interfaces.Catalog{{ID: "catalog-2", Name: "目录二"}}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, err := service.GetByID(context.Background(), "task-3")

		require.NoError(t, err)
		assert.Equal(t, "目录二", got.CatalogName)
	})

	t.Run("list keeps tasks when reference lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		ums := vmock.NewMockUserMgmtService(ctrl)
		service := &discoverTaskService{dta: dta, cs: cs, ums: ums}
		tasks := []*interfaces.DiscoverTaskSummary{{ID: "task-4", CatalogID: "catalog-3"}}

		dta.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(1), nil)
		cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-3"}).Return(nil, errors.New("catalog service down"))
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, total, err := service.List(context.Background(), interfaces.DiscoverTaskQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, "task-4", got[0].ID)
		assert.Empty(t, got[0].CatalogName)
	})
}

func TestDiscoverTaskServiceInternalStatusUpdates(t *testing.T) {
	t.Run("delegates internal running update", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().MarkRunning(gomock.Any(), "task-1", gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkRunning(context.Background(), "task-1")

		require.NoError(t, err)
		assert.True(t, updated)
	})

	t.Run("delegates internal completed update", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		result := &interfaces.DiscoverResult{}
		dta.EXPECT().MarkCompleted(gomock.Any(), "task-1", result, gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkCompleted(context.Background(), "task-1", result)

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestDiscoverTaskServiceDeleteByIDs(t *testing.T) {
	t.Run("deduplicates ids and deletes completed tasks", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.DiscoverTask{ID: "task-1", Status: interfaces.DiscoverTaskStatusCompleted}, nil)
		dta.EXPECT().GetByID(gomock.Any(), "task-2").
			Return(&interfaces.DiscoverTask{ID: "task-2", Status: interfaces.DiscoverTaskStatusFailed}, nil)
		dta.EXPECT().DeleteByIDs(gomock.Any(), []string{"task-1", "task-2"}).Return(int64(2), nil)

		require.NoError(t, service.DeleteByIDs(context.Background(), []string{"task-1", "task-1", "task-2"}, false))
	})

	t.Run("rejects pending or running tasks", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.DiscoverTask{ID: "task-1", Status: interfaces.DiscoverTaskStatusRunning}, nil)

		err := service.DeleteByIDs(context.Background(), []string{"task-1"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "HasRunningExecution")
		assert.Contains(t, err.Error(), "task-1")
	})

	t.Run("missing ids fail unless ignored", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		err := service.DeleteByIDs(context.Background(), []string{"missing"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotFound")
		assert.Contains(t, err.Error(), "missing")
	})

	t.Run("ignore missing deletes existing ids only", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)
		dta.EXPECT().GetByID(gomock.Any(), "done").
			Return(&interfaces.DiscoverTask{ID: "done", Status: interfaces.DiscoverTaskStatusCompleted}, nil)
		dta.EXPECT().DeleteByIDs(gomock.Any(), []string{"done"}).Return(int64(1), nil)

		require.NoError(t, service.DeleteByIDs(context.Background(), []string{"missing", "done"}, true))
	})

	t.Run("wraps get failure", func(t *testing.T) {
		service, dta, _ := newTestDiscoverTaskService(t)
		dta.EXPECT().GetByID(gomock.Any(), "task-1").Return(nil, errors.New("db down"))

		err := service.DeleteByIDs(context.Background(), []string{"task-1"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}
