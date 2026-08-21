// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_task

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

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
	cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
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
	cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

	task, err := svc.GetByID(context.Background(), "task-1")
	require.Nil(t, task)
	assert.Same(t, denied, err)
}

// TestDiscoverTaskListPushesTheVisibleCatalogsIntoTheQuery: 与构建任务同一口径
// ——可见目录集进查询,而不是对取回的页过滤,这样 total 与分页才成立。
func TestDiscoverTaskListPushesTheVisibleCatalogsIntoTheQuery(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*discoverTaskService,
		*vmock.MockDiscoverTaskAccess, *vmock.MockCatalogService) {
		cs := vmock.NewMockCatalogService(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		ums := vmock.NewMockUserMgmtService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		cs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		return &discoverTaskService{cs: cs, dta: dta, ums: ums}, dta, cs
	}

	t.Run("可见目录集下推进查询", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return([]string{"cat-1"}, false, nil, nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
				assert.Equal(t, []string{"cat-1"}, params.CatalogIDs)
				return []*interfaces.DiscoverTaskSummary{{ID: "t-1", CatalogID: "cat-1"}}, 1, nil
			})

		tasks, total, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
		assert.EqualValues(t, 1, total)
	})

	t.Run("一个目录都看不见就直接空,不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), gomock.Any()).
			Return(nil, false, nil, nil)
		_ = dta

		tasks, total, err := svc.List(context.Background(), interfaces.DiscoverTaskQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定看不见的 catalog_id,查都不查", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, dta, cs := newSvc(ctrl)

		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-other", interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))
		_ = dta

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
