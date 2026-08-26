// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mockinterfaces "vega-backend/interfaces/mock"
)

func TestCascadeDeleteBuildTasks(t *testing.T) {
	t.Run("returns list error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mockinterfaces.NewMockBuildTaskAccess(ctrl)
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, errors.New("list failed"))

		err := CascadeDeleteBuildTasks(context.Background(), bta, interfaces.BuildTasksQueryParams{})

		require.ErrorContains(t, err, "list failed")
	})

	t.Run("rejects running tasks before deletion", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mockinterfaces.NewMockBuildTaskAccess(ctrl)
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{{ID: "task-1", Status: interfaces.BuildTaskStatusRunning}}, nil)

		err := CascadeDeleteBuildTasks(context.Background(), bta, interfaces.BuildTasksQueryParams{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "running_ids")
	})

	t.Run("only deletes task rows", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mockinterfaces.NewMockBuildTaskAccess(ctrl)
		task := &interfaces.BuildTaskSummary{ID: "task-1", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusCompleted}
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{task}, nil)
		bta.EXPECT().DeleteByIDs(gomock.Any(), []string{task.ID}).Return(int64(1), nil)

		require.NoError(t, CascadeDeleteBuildTasks(context.Background(), bta, interfaces.BuildTasksQueryParams{}))
	})
}
