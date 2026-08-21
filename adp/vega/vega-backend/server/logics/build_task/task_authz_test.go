// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
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
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs, cs: cs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", CatalogID: "cat-1",
			Status: interfaces.BuildTaskStatusStopped,
		}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
		// 状态流转与落库一次都不该发生。

		assert.Same(t, denied, svc.Start(context.Background(), "task-1", false))
	})

	t.Run("stop", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs, cs: cs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", CatalogID: "cat-1", Status: interfaces.BuildTaskStatusRunning,
		}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

		assert.Same(t, denied, svc.Stop(context.Background(), "task-1"))
	})

	t.Run("delete 整批停下，不删已通过的那些", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs, cs: cs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", CatalogID: "cat-1", Status: interfaces.BuildTaskStatusStopped,
		}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
		// DeleteByIDs 未被期望——一条没权限就该整批不删。

		assert.Same(t, denied, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false, false))
	})
}

// TestBuildTaskReadRequiresViewDetail：读一个任务判在它所属的目录上,与列表同一
// 口径——否则会出现「列表里看不到、按 id 却读得到」。
func TestBuildTaskReadRequiresViewDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	cs := mock_interfaces.NewMockCatalogService(ctrl)
	svc := &buildTaskService{bta: bta, rs: rs, cs: cs}

	denied := errors.New("forbidden")
	bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
		ID: "task-1", ResourceID: "res-1", CatalogID: "cat-1",
	}, nil)
	cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
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
	cs := mock_interfaces.NewMockCatalogService(ctrl)
	svc := &buildTaskService{rs: rs, cs: cs}

	denied := errors.New("forbidden")
	rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(&interfaces.Resource{
		ID: "res-1", CatalogID: "cat-1", Category: interfaces.ResourceCategoryTable,
	}, nil)
	// 判在表所在的目录上,而不是表本身。
	cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

	id, err := svc.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "res-1"})
	assert.Empty(t, id)
	assert.Same(t, denied, err)
}

// TestBuildTaskListFiltersByVisibleCatalogs 是 #472 的标题项:列表曾经返回全量。
//
// 判定统一落在目录上——表的管理权已经收敛到它所在的目录,任务是目录下的产物。
// 过滤对取回的这一页做,问的目录数被页大小兜住,而不是被这个账号被授权过的对象
// 数量兜住。
func TestBuildTaskListFiltersByVisibleCatalogs(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*buildTaskService,
		*mock_interfaces.MockBuildTaskAccess, *mock_interfaces.MockResourceService,
		*mock_interfaces.MockCatalogService) {
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		rs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		cs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		return &buildTaskService{bta: bta, rs: rs, ums: ums, cs: cs}, bta, rs, cs
	}
	page := func(catalogIDs ...string) []*interfaces.BuildTaskSummary {
		out := make([]*interfaces.BuildTaskSummary, 0, len(catalogIDs))
		for i, id := range catalogIDs {
			out = append(out, &interfaces.BuildTaskSummary{
				ID: fmt.Sprintf("task-%d", i), ResourceID: fmt.Sprintf("res-%d", i), CatalogID: id,
			})
		}
		return out
	}

	t.Run("只留下看得见的那几行", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, _, cs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("cat-1", "cat-2"), int64(2), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), []string{"cat-1", "cat-2"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowOnlyIDs("cat-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "cat-1", tasks[0].CatalogID)
	})

	t.Run("只就这一页上的目录问鉴权,不是全量 id", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, _, cs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("cat-1", "cat-1", "cat-2"), int64(3), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, ids []string, _ string) (map[string]bool, error) {
				assert.Len(t, ids, 3, "传的是这一页上的 catalog_id,页大小就是上限")
				return map[string]bool{"cat-1": true}, nil
			}).Times(1)

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Len(t, tasks, 2)
	})

	t.Run("内部目录下的任务看不见就滤掉", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, _, cs := newSvc(ctrl)

		// 内部目录在批量过滤里按 internal_catalog 分型问,业务角色的 catalog:*
		// 够不到它,所以挂在它底下的任务不会漏出去。
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("biz-cat", "internal-cat"), int64(2), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(allowOnlyIDs("biz-cat"))

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "biz-cat", tasks[0].CatalogID)
	})

	t.Run("一页全被滤掉就返回空,不报错", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, _, cs := newSvc(ctrl)

		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("cat-1"), int64(1), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(denyAllIDs)

		tasks, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
	})

	t.Run("鉴权服务答不上来要报错,不能报成空页", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs, cs := newSvc(ctrl)

		// 500 不是「这张表没有任务」,而是「问不出来」。吞掉它会让界面显示一张
		// 空表、监控看到一次成功请求,正在跑的任务凭空消失。
		boom := rest.NewHTTPError(context.Background(), http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_FilterResourcesFailed)
		rs.EXPECT().InternalGetByID(gomock.Any(), "res-1").
			Return(&interfaces.Resource{ID: "res-1", CatalogID: "cat-1"}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1", gomock.Any()).Return(boom)
		_ = bta // bta.List 不该被调用

		_, _, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "res-1"})
		require.Error(t, err, "鉴权故障必须上抛")
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})

	t.Run("显式指定看不见的 resource_id,查都不查", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs, cs := newSvc(ctrl)

		rs.EXPECT().InternalGetByID(gomock.Any(), "res-other").
			Return(&interfaces.Resource{ID: "res-other", CatalogID: "cat-other"}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-other",
			interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return(rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))
		_ = bta // bta.List 不该被调用

		tasks, total, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{ResourceID: "res-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定看得见的 resource_id,这一页不再逐行复判", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, rs, cs := newSvc(ctrl)

		rs.EXPECT().InternalGetByID(gomock.Any(), "res-1").
			Return(&interfaces.Resource{ID: "res-1", CatalogID: "cat-1"}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
			interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(page("cat-1"), int64(1), nil)
		// 已经判过了,再对整页问一遍是白花钱:FilterAuthorizedCatalogs 不该被调用。

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
