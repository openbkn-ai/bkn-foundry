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

// TestBuildTaskListFiltersByVisibleResources 是 #472 的标题项:列表曾经返回全量。
//
// 过滤对取回的这一页做,问的 id 数被页大小兜住,而不是被这个账号被授权过的资源
// 数量兜住——后者在一个跑久了的部署上能到几千,每翻一页都塞进 IN 列表并不划算。
// 代价是 total 为未过滤计数、某页可能短甚至为空,调用方要翻到没有为止。
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

	t.Run("只留下看得见的那几行", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				return page("res-1", "res-2"), int64(2), nil
			})
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-1", "res-2"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowOnlyIDs("res-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "res-1", tasks[0].ResourceID)
	})

	t.Run("只就这一页上的表问鉴权,不是全量 id", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("res-1", "res-1", "res-2"), int64(3), nil)
		// 同一张表出现两次也只问一次:去重在服务里做。
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, ids []string, _ string) (map[string]bool, error) {
				assert.Len(t, ids, 3, "传的是这一页上的 resource_id,页大小就是上限")
				return map[string]bool{"res-1": true}, nil
			}).Times(1)

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Len(t, tasks, 2)
	})

	t.Run("内部目录下的任务看不见就滤掉", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		// 内部资源在批量过滤里按 internal_resource 分型问,业务角色的 resource:*
		// 够不到它,所以挂在它底下的任务不会漏出去。
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("biz-1", "internal-1"), int64(2), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(allowOnlyIDs("biz-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "biz-1", tasks[0].ResourceID)
	})

	t.Run("一页全被滤掉就返回空,不报错", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("res-1"), int64(1), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(denyAllIDs)

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
	})

	t.Run("显式指定看不见的 resource_id,查都不查", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-other",
			interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(errors.New("denied"))
		_ = bta // bta.List 不该被调用

		tasks, total, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "res-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定看得见的 resource_id,这一页不再逐行复判", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs := newSvc(ctrl)

		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
			interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("res-1"), int64(1), nil)
		// 已经判过这张表了,再对整页问一遍是白花钱:FilterAuthorizedResources 不该被调用。

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
