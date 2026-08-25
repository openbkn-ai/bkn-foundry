package logics

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

func TestCascadeDeleteBuildTasks(t *testing.T) {
	t.Run("returns list error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		filter := interfaces.BuildTasksQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 100, Offset: 20},
			ResourceID:            "resource-1",
		}
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, got interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				assert.Equal(t, "resource-1", got.ResourceID)
				assert.Zero(t, got.Limit)
				assert.Zero(t, got.Offset)
				return nil, errors.New("list failed")
			})

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, filter, "")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list failed")
	})

	t.Run("rejects running tasks before deleting anything", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{
			{ID: "task-1", Status: interfaces.BuildTaskStatusRunning},
			{ID: "task-2", Status: interfaces.BuildTaskStatusStopping},
		}, nil)

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, interfaces.BuildTasksQueryParams{}, "")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "running_ids")
	})

	t.Run("deletes task even when local index deletion fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		task := &interfaces.BuildTaskSummary{ID: "task-1", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusCompleted}
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{task}, nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), BuildIndexName(task.ResourceID, task.ID)).Return(errors.New("drop failed"))
		bta.EXPECT().DeleteByIDs(gomock.Any(), []string{task.ID}).Return(int64(1), nil)

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, interfaces.BuildTasksQueryParams{}, "")

		require.NoError(t, err)
	})

	t.Run("returns task delete error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		task := &interfaces.BuildTaskSummary{ID: "task-1", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusCompleted}
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{task}, nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), BuildIndexName(task.ResourceID, task.ID)).Return(nil)
		bta.EXPECT().DeleteByIDs(gomock.Any(), []string{task.ID}).Return(int64(0), errors.New("delete failed"))

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, interfaces.BuildTasksQueryParams{}, "")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
	})

	t.Run("deletes current index after producing task was already deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{}, nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), "current-index").Return(nil)

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim,
			interfaces.BuildTasksQueryParams{ResourceID: "resource-1"}, "current-index")

		require.NoError(t, err)
	})

	t.Run("deletes current and task-derived index only once", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		task := &interfaces.BuildTaskSummary{ID: "task-1", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusCompleted}
		indexName := BuildIndexName(task.ResourceID, task.ID)
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{task}, nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), indexName).Return(nil).Times(1)
		bta.EXPECT().DeleteByIDs(gomock.Any(), []string{task.ID}).Return(int64(1), nil)

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim,
			interfaces.BuildTasksQueryParams{ResourceID: "resource-1"}, indexName)

		require.NoError(t, err)
	})
}
