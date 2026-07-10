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
		bta.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, got interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
				assert.Equal(t, "resource-1", got.ResourceID)
				assert.Zero(t, got.Limit)
				assert.Zero(t, got.Offset)
				return nil, int64(0), errors.New("list failed")
			})

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, filter)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list failed")
	})

	t.Run("rejects running tasks before deleting anything", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTask{
			{ID: "task-1", Status: interfaces.BuildTaskStatusRunning},
			{ID: "task-2", Status: interfaces.BuildTaskStatusStopping},
		}, int64(2), nil)

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, interfaces.BuildTasksQueryParams{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "running_ids")
	})

	t.Run("deletes task even when local index deletion fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		task := &interfaces.BuildTask{ID: "task-1", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusCompleted}
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTask{task}, int64(1), nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName(task.ResourceID, task.ID)).Return(errors.New("drop failed"))
		bta.EXPECT().Delete(gomock.Any(), task.ID).Return(nil)

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, interfaces.BuildTasksQueryParams{})

		require.NoError(t, err)
	})

	t.Run("returns task delete error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		task := &interfaces.BuildTask{ID: "task-1", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusCompleted}
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTask{task}, int64(1), nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName(task.ResourceID, task.ID)).Return(nil)
		bta.EXPECT().Delete(gomock.Any(), task.ID).Return(errors.New("delete failed"))

		err := CascadeDeleteBuildTasks(context.Background(), bta, lim, interfaces.BuildTasksQueryParams{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
	})
}
