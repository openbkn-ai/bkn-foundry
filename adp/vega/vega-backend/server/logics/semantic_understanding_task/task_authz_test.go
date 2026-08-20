// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package semantic_understanding_task

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func resourceScopedTask() *interfaces.SemanticUnderstandingTask {
	return &interfaces.SemanticUnderstandingTask{
		ID:         "task-1",
		Scope:      interfaces.SemanticUnderstandingTaskScopeResource,
		ResourceID: "res-1",
		CatalogID:  "cat-1",
	}
}

// 任务的 input 快照里装着这张表未脱敏的样例行，所以读这条任务等于读那些行。
// 创建那道门拦下的东西，不能从详情端点走出去（#571 / bkn-studio#342）。
func TestSemanticTaskReadRequiresPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	svc := &semanticUnderstandingTaskService{suta: suta, rs: rs}

	denied := errors.New("forbidden")
	suta.EXPECT().GetByID(gomock.Any(), "task-1").Return(resourceScopedTask(), nil)
	rs.EXPECT().InternalGetByID(gomock.Any(), "res-1").Return(&interfaces.Resource{ID: "res-1"}, nil)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(denied)

	task, err := svc.GetByID(context.Background(), "task-1")
	require.Nil(t, task)
	assert.Same(t, denied, err)
}

// 批量删除是整体事务：一条没权限就整批停下，不能删掉其余的。
func TestSemanticTaskDeleteStopsTheWholeBatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	svc := &semanticUnderstandingTaskService{suta: suta, rs: rs}

	denied := errors.New("forbidden")
	suta.EXPECT().GetByIDs(gomock.Any(), []string{"task-1"}).
		Return([]*interfaces.SemanticUnderstandingTask{resourceScopedTask()}, nil)
	rs.EXPECT().InternalGetByID(gomock.Any(), "res-1").Return(&interfaces.Resource{ID: "res-1"}, nil)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
	// suta.DeleteByIDs 未被期望。

	assert.Same(t, denied, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false))
}

// TestSemanticTaskOrphanFallsBackToCatalogThenTypeWide: 表被删除后任务不随之消失，
// 只会被标成 cancelled。判在已经不存在的父上会永远 403——任务既看不了也删不掉，
// 永久滞留在列表里，而批量删除是全或无，一条孤儿会毒死整批。
func TestSemanticTaskOrphanFallsBackToCatalogThenTypeWide(t *testing.T) {
	t.Run("表没了，退到目录", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs}

		suta.EXPECT().GetByIDs(gomock.Any(), gomock.Any()).
			Return([]*interfaces.SemanticUnderstandingTask{resourceScopedTask()}, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), "res-1").Return(nil, nil) // 表已删
		cs.EXPECT().InternalGetByID(gomock.Any(), "cat-1", false).
			Return(&interfaces.Catalog{ID: "cat-1"}, nil)
		cs.EXPECT().CheckCatalogPermission(gomock.Any(), "cat-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		suta.EXPECT().DeleteByIDs(gomock.Any(), gomock.Any()).Return(int64(1), nil)

		require.NoError(t, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false))
	})

	t.Run("目录也没了，交给持类型级授权的人", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs}

		suta.EXPECT().GetByIDs(gomock.Any(), gomock.Any()).
			Return([]*interfaces.SemanticUnderstandingTask{resourceScopedTask()}, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), "res-1").Return(nil, nil)
		// 目录已删:InternalGetByID 返回的是 404 错误,不是 (nil, nil)。
		cs.EXPECT().InternalGetByID(gomock.Any(), "cat-1", false).
			Return(nil, rest.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound))
		cs.EXPECT().HasTypeWideGrant(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).Return(true, nil)
		suta.EXPECT().DeleteByIDs(gomock.Any(), gomock.Any()).Return(int64(1), nil)

		require.NoError(t, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false))
	})

	t.Run("父都没了且没有类型级授权，仍然拒绝", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs}

		suta.EXPECT().GetByIDs(gomock.Any(), gomock.Any()).
			Return([]*interfaces.SemanticUnderstandingTask{resourceScopedTask()}, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), "res-1").Return(nil, nil)
		cs.EXPECT().InternalGetByID(gomock.Any(), "cat-1", false).
			Return(nil, rest.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound))
		cs.EXPECT().HasTypeWideGrant(gomock.Any(), gomock.Any()).Return(false, nil)

		err := svc.DeleteByIDs(context.Background(), []string{"task-1"}, false)
		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	})
}

// TestSemanticTaskListFiltersByVisibleParents: 任务挂在父对象上,列表就按父对象
// 判——资源域的问表,目录域的问目录。过滤对取回的这一页做,问的 id 数被页大小
// 兜住,而不是被这个账号被授权过的数量兜住。
func TestSemanticTaskListFiltersByVisibleParents(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*semanticUnderstandingTaskService,
		*mock_interfaces.MockSemanticUnderstandingTaskAccess,
		*mock_interfaces.MockResourceService, *mock_interfaces.MockCatalogService) {
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		rs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		cs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		return &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs, ums: ums}, suta, rs, cs
	}
	resourceTask := func(id, resourceID string) *interfaces.SemanticUnderstandingTaskSummary {
		return &interfaces.SemanticUnderstandingTaskSummary{
			ID: id, Scope: interfaces.SemanticUnderstandingTaskScopeResource, ResourceID: resourceID,
		}
	}
	catalogTask := func(id, catalogID string) *interfaces.SemanticUnderstandingTaskSummary {
		return &interfaces.SemanticUnderstandingTaskSummary{
			ID: id, Scope: interfaces.SemanticUnderstandingTaskScopeCatalog, CatalogID: catalogID,
		}
	}
	ids := func(tasks []*interfaces.SemanticUnderstandingTaskSummary) []string {
		out := make([]string, 0, len(tasks))
		for _, t := range tasks {
			out = append(out, t.ID)
		}
		return out
	}

	t.Run("两个域各按各的父对象过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, rs, cs := newSvc(ctrl)

		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			resourceTask("t-res-ok", "res-1"),
			resourceTask("t-res-no", "res-2"),
			catalogTask("t-cat-ok", "cat-1"),
			catalogTask("t-cat-no", "cat-2"),
		}, int64(4), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-1", "res-2"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowOnlyIDs("res-1"))
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), []string{"cat-1", "cat-2"},
			interfaces.OPERATION_TYPE_VIEW_DETAIL).DoAndReturn(allowOnlyIDs("cat-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"t-res-ok", "t-cat-ok"}, ids(tasks))
	})

	t.Run("资源域的任务不会拿目录 id 去问,反之亦然", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, rs, cs := newSvc(ctrl)

		// 一条资源域任务上同时带着 catalog_id:判定必须按 scope 走,不能两边都问。
		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			{ID: "t-1", Scope: interfaces.SemanticUnderstandingTaskScopeResource,
				ResourceID: "res-1", CatalogID: "cat-1"},
		}, int64(1), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), []string{"res-1"}, gomock.Any()).
			DoAndReturn(denyAllIDs)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), []string{}, gomock.Any()).
			DoAndReturn(allowAllIDs)

		tasks, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks, "资源侧拒了就该滤掉,不能靠目录侧捡回来")
	})

	t.Run("内部对象下的任务看不见就滤掉", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, rs, cs := newSvc(ctrl)

		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			resourceTask("t-biz", "biz-1"),
			resourceTask("t-internal", "internal-1"),
			catalogTask("t-internal-cat", "internal-cat"),
		}, int64(3), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(allowOnlyIDs("biz-1"))
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(denyAllIDs)

		tasks, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"t-biz"}, ids(tasks))
	})

	t.Run("一页全被滤掉就返回空,不报错", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, rs, cs := newSvc(ctrl)

		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			resourceTask("t-1", "res-1"),
		}, int64(1), nil)
		rs.EXPECT().FilterAuthorizedResources(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(denyAllIDs)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(denyAllIDs)

		tasks, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
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
