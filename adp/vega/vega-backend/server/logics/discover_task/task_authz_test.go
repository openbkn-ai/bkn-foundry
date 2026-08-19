// Copyright openbkn.ai
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

// 探查任务此前没有任何权限判定（#269）。它挂在目录上而不是资源上，所以判的是
// 所属目录:写要 task_manage,读要 view_detail。

func TestDiscoverTaskCreateRequiresCatalogTaskManage(t *testing.T) {
	ctrl := gomock.NewController(t)
	cs := vmock.NewMockCatalogService(ctrl)
	dta := vmock.NewMockDiscoverTaskAccess(ctrl)
	svc := &discoverTaskService{cs: cs, dta: dta}

	denied := errors.New("forbidden")
	cs.EXPECT().CheckCatalogPermission(gomock.Any(), "cat-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
	// dta.Create 未被期望——拒了就不该落库。

	id, err := svc.Create(context.Background(), &interfaces.CreateDiscoverTaskRequest{CatalogID: "cat-1"})
	assert.Empty(t, id)
	assert.Same(t, denied, err)
}

func TestDiscoverTaskReadRequiresCatalogViewDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	cs := vmock.NewMockCatalogService(ctrl)
	dta := vmock.NewMockDiscoverTaskAccess(ctrl)
	svc := &discoverTaskService{cs: cs, dta: dta}

	denied := errors.New("forbidden")
	dta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "cat-1",
	}, nil)
	cs.EXPECT().CheckCatalogPermission(gomock.Any(), "cat-1",
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(denied)

	task, err := svc.GetByID(context.Background(), "task-1")
	require.Nil(t, task)
	assert.Same(t, denied, err)
}

// TestDiscoverTaskListFiltersByVisibleCatalogs 与构建任务列表同一口径:过滤下推
// 到 SQL,让 total 与调用方真正能看到的数量一致。
func TestDiscoverTaskListFiltersByVisibleCatalogs(t *testing.T) {
	t.Run("按可见目录集过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cs := vmock.NewMockCatalogService(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		ums := vmock.NewMockUserMgmtService(ctrl)
		svc := &discoverTaskService{cs: cs, dta: dta, ums: ums}

		cs.EXPECT().AuthorizedCatalogIDs(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return([]string{"cat-1"}, false, nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
				assert.Equal(t, []string{"cat-1"}, params.CatalogIDs, "可见目录集必须下推到查询里")
				return []*interfaces.DiscoverTaskSummary{}, 0, nil
			})
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		_, _, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
	})

	t.Run("一个都看不见就直接空，不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cs := vmock.NewMockCatalogService(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		svc := &discoverTaskService{cs: cs, dta: dta}

		cs.EXPECT().AuthorizedCatalogIDs(gomock.Any(), gomock.Any()).Return(nil, false, nil)

		tasks, total, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定的 catalog_id 是取交集，不是放宽", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cs := vmock.NewMockCatalogService(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		svc := &discoverTaskService{cs: cs, dta: dta}

		cs.EXPECT().AuthorizedCatalogIDs(gomock.Any(), gomock.Any()).
			Return([]string{"cat-1"}, false, nil)

		tasks, total, err := svc.List(context.Background(),
			interfaces.DiscoverTaskQueryParams{CatalogID: "cat-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})
}
