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
// 过滤对取回的这一页做,只就页上出现的表问一次鉴权。把可见集提前解析、整个塞进
// SQL,会让逐个授权的账号每翻一页都带上它被授权过的全部 id。代价写在 List 的
// 注释里:total 是「匹配查询的行数」,不是「能读的行数」。
func TestBuildTaskListFiltersByVisibleResources(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*buildTaskService,
		*mock_interfaces.MockBuildTaskAccess, *mock_interfaces.MockResourceService) {
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		// 摘要里的名字回填与授权无关,这里一律放空。
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
	returned := func(tasks []*interfaces.BuildTaskSummary) []string {
		out := make([]string, 0, len(tasks))
		for _, t := range tasks {
			out = append(out, t.ResourceID)
		}
		return out
	}

	t.Run("只留下看得见的那几行", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("res-1", "res-2"), int64(2), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-1", "res-2"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowOnlyIDs("res-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"res-1"}, returned(tasks))
	})

	t.Run("只就这一页上的表问鉴权,不是全量 id", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				return page("res-1", "res-1", "res-2"), 3, nil
			})
		// 问的是这一页上的表,而不是这个账号被授权过的全部 id——后者才是要避免的
		// 那个大数组。去重在 FilterAuthorizedResources 里做。
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-1", "res-1", "res-2"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowAllIDs).Times(1)

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
	})

	t.Run("内部目录下的任务不看不见就滤掉", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		// 判定问的是 internal_resource 类型,持业务 resource:* 的人答不上,于是滤掉;
		// 单独授过那个内部目录的人则通过目录回落留下来。
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("biz-1", "internal-1"), int64(2), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(allowOnlyIDs("biz-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"biz-1"}, returned(tasks))
	})

	t.Run("一页全被滤掉就返回空,不报错", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("res-1"), int64(1), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(denyAllIDs)

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
	})

	t.Run("显式指定看不见的 resource_id,查都不查", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-other"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(denyAllIDs)
		// bta.List 不该被调用:先判再查,拒掉就不用付这次查询。
		_ = bta

		tasks, total, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "res-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定看得见的 resource_id,这一页不再逐行复判", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		// 只允许一次鉴权调用:参数已经限定在这张表上,再对整页问一遍是白花钱。
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-1"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowAllIDs).Times(1)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("res-1"), int64(1), nil)

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
