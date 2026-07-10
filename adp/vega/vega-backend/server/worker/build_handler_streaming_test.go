// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestStreamingBuildHandlerHandleTask(t *testing.T) {
	t.Run("injects creator into downstream context", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
		resAccess := vmock.NewMockResourceAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		sh := &streamingBuildHandler{taskAccess: taskAccess, resAccess: resAccess, cs: cs, lim: lim}
		creator := interfaces.AccountInfo{ID: "u1", Type: "user"}

		taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusInit, Creator: creator,
		}, nil)
		taskAccess.EXPECT().UpdateStatusIfIn(gomock.Any(), "t1",
			[]string{interfaces.BuildTaskStatusInit},
			map[string]interface{}{"status": interfaces.BuildTaskStatusRunning, "errorMsg": ""}).
			Return(true, nil)
		resAccess.EXPECT().GetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)

		var gotAccount interfaces.AccountInfo
		var hasAccount bool
		cs.EXPECT().GetByID(gomock.Any(), "c1", true).DoAndReturn(
			func(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
				gotAccount, hasAccount = workerAccountFromCtx(ctx)
				return nil, errors.New("forbidden")
			})

		task := asynq.NewTask("build:streaming", workerBuildTaskPayload(t, interfaces.StreamingBuildTaskMessage{TaskID: "t1"}))
		err := sh.HandleTask(context.Background(), task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get catalog failed")
		require.True(t, hasAccount)
		assert.Equal(t, creator, gotAccount)
	})

	t.Run("skips duplicate message when task is already claimed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
		sh := &streamingBuildHandler{taskAccess: taskAccess}

		taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusInit,
		}, nil)
		taskAccess.EXPECT().UpdateStatusIfIn(gomock.Any(), "t1",
			[]string{interfaces.BuildTaskStatusInit},
			map[string]interface{}{"status": interfaces.BuildTaskStatusRunning, "errorMsg": ""}).
			Return(false, nil)

		task := asynq.NewTask("build:streaming", workerBuildTaskPayload(t, interfaces.StreamingBuildTaskMessage{TaskID: "t1"}))
		require.NoError(t, sh.HandleTask(context.Background(), task))
	})
}
