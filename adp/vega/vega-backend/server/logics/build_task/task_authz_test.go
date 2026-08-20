// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

// 构建任务此前全线没有任何权限判定：列表返回全量，详情/启停/删除对任意已登录
// 账号开放（#472）。这组用例钉住「拒绝时到此为止」——不是少返回几个字段，是
// 根本不执行。

func TestBuildTaskWritesRequireTaskManage(t *testing.T) {
	denied := errors.New("forbidden")

	t.Run("start", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", CatalogID: "cat-1",
			Status: interfaces.BuildTaskStatusStopped,
		}, nil)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
		// 状态流转与落库一次都不该发生。

		assert.Same(t, denied, svc.Start(context.Background(), "task-1", false))
	})

	t.Run("stop", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", Status: interfaces.BuildTaskStatusRunning,
		}, nil)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

		assert.Same(t, denied, svc.Stop(context.Background(), "task-1"))
	})

	t.Run("delete 整批停下，不删已通过的那些", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", Status: interfaces.BuildTaskStatusStopped,
		}, nil)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
		// DeleteByIDs 未被期望——一条没权限就该整批不删。

		assert.Same(t, denied, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false, false))
	})
}

// TestBuildTaskReadRequiresViewDetail：读一个任务等于读它构建的那张表。
func TestBuildTaskReadRequiresViewDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	svc := &buildTaskService{bta: bta, rs: rs}

	denied := errors.New("forbidden")
	bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
		ID: "task-1", ResourceID: "res-1",
	}, nil)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(denied)

	task, err := svc.GetByID(context.Background(), "task-1")
	require.Nil(t, task)
	assert.Same(t, denied, err)
}

// TestBuildTaskCreateRequiresTaskManage: 建任务是对那张表的写操作，看得见不等于
// 能建——GetByID 只证明了前者。
func TestBuildTaskCreateRequiresTaskManage(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	svc := &buildTaskService{rs: rs}

	denied := errors.New("forbidden")
	rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(&interfaces.Resource{
		ID: "res-1", CatalogID: "cat-1", Category: interfaces.ResourceCategoryTable,
	}, nil)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

	id, err := svc.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "res-1"})
	assert.Empty(t, id)
	assert.Same(t, denied, err)
}

// TestBuildTaskListFiltersByVisibleResources 是 #472 的标题项：列表曾经返回全量。
//
// 过滤放在哪一侧按可见集的大小分流,两种形态各有各的坏法,所以两边都要钉住:
// 小集合下推进 SQL——total 与分页才对得上,只被授了两张表的账号否则会拿到一个
// 空首页配上五位数的 total,自己的任务翻不到;大集合不下推——几千个 id 每翻一页
// 都带一遍不值当,而被授到那个量的账号本来就看得见绝大多数行。
func TestBuildTaskListFiltersByVisibleResources(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*buildTaskService,
		*mock_interfaces.MockBuildTaskAccess, *mock_interfaces.MockResourceService) {
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		rs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		cs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		return &buildTaskService{bta: bta, rs: rs, ums: ums, cs: cs}, bta, rs
	}
	page := func(ids ...string) []*interfaces.BuildTaskSummary {
		out := make([]*interfaces.BuildTaskSummary, 0, len(ids))
		for i, id := range ids {
			out = append(out, &interfaces.BuildTaskSummary{ID: fmt.Sprintf("task-%d", i), ResourceID: id})
		}
		return out
	}
	manyIDs := func(n int) []string {
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf("res-%d", i))
		}
		return out
	}

	t.Run("小可见集下推进查询", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResources(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return(interfaces.AuthorizedScope{IDs: []string{"res-1", "res-2"}}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, []string{"res-1", "res-2"}, params.ResourceIDs,
					"小集合必须下推,否则 total 与分页都不对")
				return page("res-1"), 1, nil
			})
		// 已经下推了就不该再对页问一遍:多问一次 mock 会失败。

		tasks, total, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
		assert.EqualValues(t, 1, total, "下推之后 total 是过滤后的计数")
	})

	t.Run("通配放行带排除集时,排除集下推", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{All: true, Excluded: []string{"internal-1"}}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, []string{"internal-1"}, params.ExcludeResourceIDs,
					"内部资源的排除集恒定很小,永远值得下推")
				assert.Empty(t, params.ResourceIDs)
				return nil, 0, nil
			})

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
	})

	t.Run("大可见集改为在取回的页上过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		big := manyIDs(600) // 超过下推阈值
		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{IDs: big}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Empty(t, params.ResourceIDs, "大集合不该塞进 IN 列表")
				return page("res-0", "res-999"), 2, nil
			})
		// 只就这一页上的两张表问一次,而不是 600 个 id。
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-0", "res-999"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowOnlyIDs("res-0")).Times(1)

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "res-0", tasks[0].ResourceID)
	})

	t.Run("类型级放行且无排除集时,两侧都不过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{All: true}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Empty(t, params.ResourceIDs)
				assert.Empty(t, params.ExcludeResourceIDs)
				return page("res-1"), 1, nil
			})

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
	})

	t.Run("一个都看不见就直接空,不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{}, nil)
		_ = bta // bta.List 不该被调用

		tasks, total, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定的 resource_id 是取交集,不是放宽", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{IDs: []string{"res-1"}}, nil)
		_ = bta // 问的是看不见的那张表,不该查库

		tasks, total, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "res-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定看得见的 resource_id,不再额外过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{IDs: manyIDs(600)}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Empty(t, params.ResourceIDs, "查询已经钉在这一张表上了")
				return page("res-1"), 1, nil
			})
		// 参数已限定在一张已判过的表上,再对整页问一遍是白花钱:FilterAuthorizedResources
		// 不该被调用。

		tasks, _, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "res-1"})
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
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
