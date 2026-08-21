// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package semantic_understanding_task

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
	cs := mock_interfaces.NewMockCatalogService(ctrl)
	svc := &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs}

	denied := errors.New("forbidden")
	suta.EXPECT().GetByID(gomock.Any(), "task-1").Return(resourceScopedTask(), nil)
	// 资源域的任务也判在它所属的目录上,与列表同一口径。
	cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

	task, err := svc.GetByID(context.Background(), "task-1")
	require.Nil(t, task)
	assert.Same(t, denied, err)
}

// 批量删除是整体事务：一条没权限就整批停下，不能删掉其余的。
func TestSemanticTaskDeleteStopsTheWholeBatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	cs := mock_interfaces.NewMockCatalogService(ctrl)
	svc := &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs}

	denied := errors.New("forbidden")
	suta.EXPECT().GetByIDs(gomock.Any(), []string{"task-1"}).
		Return([]*interfaces.SemanticUnderstandingTask{resourceScopedTask()}, nil)
	cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
	// suta.DeleteByIDs 未被期望。

	assert.Same(t, denied, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false))
}

// TestSemanticTaskDelegatesToTheCatalogCheck: 判定统一交给 CatalogService，包括
// 目录被删之后的兜底——那套三级递降在 catalog 包里测，这里只钉住「确实是交出去
// 判的」，不在两个包各测一遍同一件事。
func TestSemanticTaskDelegatesToTheCatalogCheck(t *testing.T) {
	ctrl := gomock.NewController(t)
	suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	cs := mock_interfaces.NewMockCatalogService(ctrl)
	svc := &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs}

	suta.EXPECT().GetByIDs(gomock.Any(), gomock.Any()).
		Return([]*interfaces.SemanticUnderstandingTask{resourceScopedTask()}, nil)
	cs.EXPECT().CheckTaskPermission(gomock.Any(), "cat-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
	suta.EXPECT().DeleteByIDs(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	require.NoError(t, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false))
}

// 被删之后任务不随之消失,只会被标成 cancelled。判在已经不存在的父上会永远 403
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
	resourceTask := func(id, catalogID string) *interfaces.SemanticUnderstandingTaskSummary {
		return &interfaces.SemanticUnderstandingTaskSummary{
			ID: id, Scope: interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID: "res-of-" + id, CatalogID: catalogID,
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

	t.Run("两种 scope 都按目录判", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, _, cs := newSvc(ctrl)

		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			resourceTask("t-res-ok", "cat-1"),
			resourceTask("t-res-no", "cat-2"),
			catalogTask("t-cat-ok", "cat-1"),
			catalogTask("t-cat-no", "cat-2"),
		}, int64(4), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(),
			[]string{"cat-1", "cat-2", "cat-1", "cat-2"},
			interfaces.OPERATION_TYPE_TASK_MANAGE).DoAndReturn(allowOnlyIDs("cat-1"))

		tasks, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"t-res-ok", "t-cat-ok"}, ids(tasks),
			"资源域的任务也按它所在的目录判,不单独问表")
	})

	t.Run("内部目录下的任务看不见就滤掉", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, _, cs := newSvc(ctrl)

		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			catalogTask("t-biz", "biz-cat"),
			catalogTask("t-internal", "internal-cat"),
		}, int64(2), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(allowOnlyIDs("biz-cat"))

		tasks, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"t-biz"}, ids(tasks))
	})

	t.Run("一页全被滤掉就返回空,不报错", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, _, cs := newSvc(ctrl)

		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			catalogTask("t-1", "cat-1"),
		}, int64(1), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(denyAllIDs)

		tasks, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
	})

	t.Run("没有 catalog_id 的任务不会凭空可见", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, _, cs := newSvc(ctrl)

		// 理论上不该出现,但真出现时按「看不见」处理,而不是漏出去。
		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.SemanticUnderstandingTaskSummary{
			{ID: "t-orphan", Scope: interfaces.SemanticUnderstandingTaskScopeCatalog},
		}, int64(1), nil)
		cs.EXPECT().FilterAuthorizedCatalogs(gomock.Any(), []string{}, gomock.Any()).
			DoAndReturn(allowAllIDs)

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
