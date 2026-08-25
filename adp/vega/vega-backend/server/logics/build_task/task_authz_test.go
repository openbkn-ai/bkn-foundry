// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"errors"
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

		assert.Same(t, denied, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false))
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
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

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

// TestBuildTaskListPushesTheVisibleCatalogsIntoTheQuery 钉住 #472 的分页要求。
//
// 过滤放在查询里而不是对取回的页做:页内过滤会让 total 计入看不见的行,还会出现
// 中间空页而后面仍有可见行——按空页停会漏数据,按 total 翻会请求大量全被滤掉的
// 页。可下推是因为判的是目录:一个部署几十个,不随建表增长。
func TestBuildTaskListPushesTheVisibleCatalogsIntoTheQuery(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*buildTaskService,
		*mock_interfaces.MockBuildTaskAccess, *mock_interfaces.MockCatalogService) {
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		rs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		cs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		return &buildTaskService{bta: bta, rs: rs, ums: ums, cs: cs}, bta, cs
	}

	t.Run("可见目录集下推进查询", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return([]string{"cat-1", "cat-2"}, false, nil, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, []string{"cat-1", "cat-2"}, params.CatalogIDs,
					"可见集必须进查询,否则 total 与分页都不对")
				return []*interfaces.BuildTaskSummary{{ID: "t-1", CatalogID: "cat-1"}}, 1, nil
			})

		tasks, total, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
		assert.EqualValues(t, 1, total, "total 是过滤后的计数")
	})

	t.Run("类型级授权不加谓词,但排除够不到的内部目录", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), gomock.Any()).
			Return(nil, true, []string{"internal-cat"}, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Empty(t, params.CatalogIDs, "通配放行不该被展开成 id 清单")
				assert.Equal(t, []string{"internal-cat"}, params.ExcludeCatalogIDs)
				return nil, 0, nil
			})

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
	})

	t.Run("一个目录都看不见就直接空,不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), gomock.Any()).
			Return(nil, false, nil, nil)
		_ = bta // bta.List 不该被调用

		tasks, total, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("显式指定看不见的 catalog_id,查都不查", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, cs := newSvc(ctrl)

		cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-other", interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))
		_ = bta

		tasks, total, err := svc.List(context.Background(),
			interfaces.BuildTasksQueryParams{CatalogID: "cat-other"})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("鉴权服务答不上来要报错,不能报成空页", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, bta, cs := newSvc(ctrl)

		boom := rest.NewHTTPError(context.Background(), http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_FilterResourcesFailed)
		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), gomock.Any()).
			Return(nil, false, nil, boom)
		_ = bta

		_, _, err := svc.List(context.Background(), interfaces.BuildTasksQueryParams{})
		require.Error(t, err, "鉴权故障必须上抛")
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
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
