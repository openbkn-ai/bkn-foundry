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
		cs.EXPECT().InternalGetByID(gomock.Any(), "cat-1", false).Return(nil, nil)
		rs.EXPECT().AuthorizedResourceIDs(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(nil, true, nil)
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
		cs.EXPECT().InternalGetByID(gomock.Any(), "cat-1", false).Return(nil, nil)
		rs.EXPECT().AuthorizedResourceIDs(gomock.Any(), gomock.Any()).Return(nil, false, nil)

		err := svc.DeleteByIDs(context.Background(), []string{"task-1"}, false)
		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	})
}

// TestSemanticTaskListFiltersByVisibleParents: 任务是双 scope 的,所以可见性是
// 析取——资源级任务看它那张表,目录级任务看它那个目录,不能合成一个 id 集。
func TestSemanticTaskListFiltersByVisibleParents(t *testing.T) {
	newSvc := func(ctrl *gomock.Controller) (*semanticUnderstandingTaskService,
		*mock_interfaces.MockSemanticUnderstandingTaskAccess,
		*mock_interfaces.MockResourceService, *mock_interfaces.MockCatalogService) {
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		ums := mock_interfaces.NewMockUserMgmtService(ctrl)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		return &semanticUnderstandingTaskService{suta: suta, rs: rs, cs: cs, ums: ums}, suta, rs, cs
	}

	t.Run("两边的可见集都下推", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, rs, cs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResourceIDs(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return([]string{"res-1"}, false, nil)
		cs.EXPECT().AuthorizedCatalogIDs(gomock.Any(), interfaces.OPERATION_TYPE_VIEW_DETAIL).
			Return([]string{"cat-1"}, false, nil)
		suta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p interfaces.SemanticUnderstandingTaskQueryParams) (
				[]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
				require.NotNil(t, p.Visibility)
				assert.Equal(t, []string{"res-1"}, p.Visibility.ResourceIDs)
				assert.Equal(t, []string{"cat-1"}, p.Visibility.CatalogIDs)
				assert.False(t, p.Visibility.AllResources)
				assert.False(t, p.Visibility.AllCatalogs)
				return nil, 0, nil
			})

		_, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
	})

	t.Run("一边是类型级授权也要如实带上，不能当成空集", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, rs, cs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResourceIDs(gomock.Any(), gomock.Any()).Return(nil, true, nil)
		cs.EXPECT().AuthorizedCatalogIDs(gomock.Any(), gomock.Any()).Return(nil, false, nil)
		suta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p interfaces.SemanticUnderstandingTaskQueryParams) (
				[]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
				require.NotNil(t, p.Visibility)
				assert.True(t, p.Visibility.AllResources, "持 resource:* 的人应看到全部资源级任务")
				return nil, 0, nil
			})

		_, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
	})

	t.Run("两边都看不见就直接空，不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, _, rs, cs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResourceIDs(gomock.Any(), gomock.Any()).Return(nil, false, nil)
		cs.EXPECT().AuthorizedCatalogIDs(gomock.Any(), gomock.Any()).Return(nil, false, nil)

		tasks, total, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, tasks)
		assert.Zero(t, total)
	})

	t.Run("两边都是类型级授权则完全不过滤", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc, suta, rs, cs := newSvc(ctrl)

		rs.EXPECT().AuthorizedResourceIDs(gomock.Any(), gomock.Any()).Return(nil, true, nil)
		cs.EXPECT().AuthorizedCatalogIDs(gomock.Any(), gomock.Any()).Return(nil, true, nil)
		suta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p interfaces.SemanticUnderstandingTaskQueryParams) (
				[]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
				assert.Nil(t, p.Visibility, "看得见全部时不该带过滤条件")
				return nil, 0, nil
			})

		_, _, err := svc.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})
		require.NoError(t, err)
	})
}
