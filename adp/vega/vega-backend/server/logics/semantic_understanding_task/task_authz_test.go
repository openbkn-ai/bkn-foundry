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

// TestSemanticTaskListPushesTheVisibleCatalogsIntoTheQuery: 与另外两条列表同一
// 口径。两种 scope 的任务都带 catalog_id,所以一个谓词覆盖两者。
func TestSemanticTaskListPushesTheVisibleCatalogsIntoTheQuery(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*semanticUnderstandingTaskService,
		*mock_interfaces.MockSemanticUnderstandingTaskAccess, *mock_interfaces.MockCatalogService) {
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		rs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		cs.EXPECT().InternalGetByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		return &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs, ums: ums}, suta, cs
	}

	t.Run("可见目录集下推进查询", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return([]string{"cat-1"}, false, nil, nil)
		suta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p interfaces.SemanticUnderstandingTaskQueryParams) (
				[]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
				assert.Equal(t, []string{"cat-1"}, p.CatalogIDs,
					"资源域与目录域的任务都带 catalog_id,一个谓词就够")
				return []*interfaces.SemanticUnderstandingTaskSummary{{ID: "t-1", CatalogID: "cat-1"}}, 1, nil
			})

		tasks, total, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
		assert.EqualValues(t, 1, total)
	})

	t.Run("一个目录都看不见就直接空,不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, cs := newSvc(ctrl)

		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), gomock.Any()).
			Return(nil, false, nil, nil)
		_ = suta

		tasks, total, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
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
