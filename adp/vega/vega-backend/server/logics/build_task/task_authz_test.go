// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"errors"
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
// 过滤下推到 SQL 而不是对取回的一页做筛选:后者会让 total_count 虚高、每页条数
// 忽多忽少，把服务端排序分页的语义破坏掉。
func TestBuildTaskListFiltersByVisibleResources(t *testing.T) {
	t.Run("按可见资源集过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs, ums: ums, cs: cs}

		rs.EXPECT().AuthorizedResources(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return(interfaces.AuthorizedScope{IDs: []string{"res-1"}}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, []string{"res-1"}, params.ResourceIDs, "可见资源集必须下推到查询里")
				return []*interfaces.BuildTaskSummary{}, 0, nil
			})
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
	})

	t.Run("持 resource:* 也看不到内部目录下的任务", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs, ums: ums, cs: cs}

		// 类型级放行是发在 resource 上的,internal_resource 是另一个类型:业务角色
		// 拿不到平台自己的表,它们的构建任务也不能从这条列表里漏出去。
		rs.EXPECT().AuthorizedResources(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return(interfaces.AuthorizedScope{All: true, Excluded: []string{"internal-1", "internal-2"}}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, []string{"internal-1", "internal-2"}, params.ExcludeResourceIDs,
					"排除集必须下推到 SQL,否则内部资源的任务会跟着列出来")
				assert.Empty(t, params.ResourceIDs, "通配放行不该被展开成 id 清单")
				return []*interfaces.BuildTaskSummary{}, 0, nil
			})
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
	})

	t.Run("被单独授权的内部目录,它下面的任务仍要列出来", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs, ums: ums, cs: cs}

		// resource:* 之外还单独拿到了某个内部目录的 view_detail:那个目录下的表
		// 不在排除集里,它们的构建任务照常可见。排除集只装两侧都没批的。
		rs.EXPECT().AuthorizedResources(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return(interfaces.AuthorizedScope{All: true, Excluded: []string{"in-other"}}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, []string{"in-other"}, params.ExcludeResourceIDs)
				assert.NotContains(t, params.ExcludeResourceIDs, "in-granted",
					"目录已经授权了,它下面的表不该被排除")
				return []*interfaces.BuildTaskSummary{}, 0, nil
			})
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
	})

	t.Run("问一张被排除的内部资源,直接空", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{All: true, Excluded: []string{"internal-1"}}, nil)
		// bta.List 不该被调用。

		tasks, total, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "internal-1"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("一个都看不见就直接空，不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).Return(interfaces.AuthorizedScope{}, nil)
		// bta.List 未被期望。

		tasks, total, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定的 resource_id 是取交集，不是放宽", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).
			Return(interfaces.AuthorizedScope{IDs: []string{"res-1"}}, nil)
		// 问的是看不见的那张表，bta.List 不该被调用。

		tasks, total, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "res-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("持类型级授权则完全不过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs, ums: ums, cs: cs}

		rs.EXPECT().AuthorizedResources(gomock.Any(), gomock.Any()).Return(interfaces.AuthorizedScope{All: true}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Empty(t, params.ResourceIDs, "持类型级授权时不该下推 id 集——那会把「看得见全部」变成「只看得见当时列出来的那些」")
				return []*interfaces.BuildTaskSummary{}, 0, nil
			})
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
	})
}
