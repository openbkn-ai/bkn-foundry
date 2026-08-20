// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_task

import (
	"context"
	"errors"
	"fmt"
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

// TestDiscoverTaskListFiltersByVisibleCatalogs: 探查任务挂在目录上,列表就按目录
// 判,与构建任务列表同一分流口径——小可见集下推进 SQL,大的改在取回的页上过滤。
func TestDiscoverTaskListFiltersByVisibleCatalogs(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*discoverTaskService,
		*vmock.MockDiscoverTaskAccess, *vmock.MockCatalogService) {
		cs := vmock.NewMockCatalogService(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		ums := vmock.NewMockUserMgmtService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		cs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		return &discoverTaskService{cs: cs, dta: dta, ums: ums}, dta, cs
	}
	page := func(catalogIDs ...string) []*interfaces.DiscoverTaskSummary {
		out := make([]*interfaces.DiscoverTaskSummary, 0, len(catalogIDs))
		for i, id := range catalogIDs {
			out = append(out, &interfaces.DiscoverTaskSummary{ID: fmt.Sprintf("task-%d", i), CatalogID: id})
		}
		return out
	}
	manyIDs := func(n int) []string {
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf("cat-%d", i))
		}
		return out
	}

	t.Run("小可见集下推进查询", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogs(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return(interfaces.AuthorizedScope{IDs: []string{"cat-1"}}, nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
				assert.Equal(t, []string{"cat-1"}, params.CatalogIDs)
				return page("cat-1"), 1, nil
			})

		tasks, total, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.EqualValues(t, 1, total, "下推之后 total 是过滤后的计数")
	})

	t.Run("大可见集改为在取回的页上过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogs(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{IDs: manyIDs(600)}, nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
				assert.Empty(t, params.CatalogIDs, "大集合不该塞进 IN 列表")
				return page("cat-1", "cat-2"), 2, nil
			})
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), []string{"cat-1", "cat-2"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowOnlyIDs("cat-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "cat-1", tasks[0].CatalogID)
	})

	t.Run("通配放行带排除集时,排除集下推", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogs(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{All: true, Excluded: []string{"internal-cat"}}, nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
				assert.Equal(t, []string{"internal-cat"}, params.ExcludeCatalogIDs)
				return nil, 0, nil
			})

		_, _, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
	})

	t.Run("一个都看不见就直接空,不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogs(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{}, nil)
		_ = dta // dta.List 不该被调用

		tasks, total, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定看不见的 catalog_id,查都不查", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogs(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{IDs: []string{"cat-1"}}, nil)
		_ = dta // dta.List 不该被调用

		tasks, total, err := svc.List(context.Background(),
			interfaces.DiscoverTaskQueryParams{CatalogID: "cat-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})
}

// allowAllIDs 让批量鉴权对传进来的每个 id 都放行——给那些不以授权为主题的用例
// 用,免得每条都去铺一遍权限桩。
func allowAllIDs(_ context.Context, ids []string, _ string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// allowOnlyIDs 只放行指定的 id,其余一律拒。
func allowOnlyIDs(allowed ...string) func(context.Context, []string, string) (map[string]bool, error) {
	set := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		set[id] = true
	}
	return func(_ context.Context, ids []string, _ string) (map[string]bool, error) {
		out := make(map[string]bool, len(ids))
		for _, id := range ids {
			if set[id] {
				out[id] = true
			}
		}
		return out, nil
	}
}

// denyAllIDs 一个都不放行。
func denyAllIDs(_ context.Context, _ []string, _ string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
