// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn_agent

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func newTestBknAgentAccess(appSetting *common.AppSetting, httpClient rest.HTTPClient) *bknAgentAccess {
	return &bknAgentAccess{
		appSetting: appSetting,
		httpClient: httpClient,
		baseURL:    appSetting.BknAgentUrl,
	}
}

func TestBknAgentAccessRun(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*bknAgentAccess, *rmock.MockHTTPClient) {
		t.Helper()

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		appSetting := &common.AppSetting{BknAgentUrl: "http://bkn-agent"}
		mockHTTPClient := rmock.NewMockHTTPClient(ctrl)
		return newTestBknAgentAccess(appSetting, mockHTTPClient), mockHTTPClient
	}

	t.Run("success", func(t *testing.T) {
		access, mockHTTPClient := setup(t)
		respBody, err := sonic.Marshal(&interfaces.BknAgentRunResponse{TaskID: "agent-task-1"})
		require.NoError(t, err)

		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), "http://bkn-agent/api/bkn-agent/v1/run", gomock.Any(), gomock.Any()).
			Return(http.StatusAccepted, respBody, nil)

		got, err := access.Run(ctx, &interfaces.BknAgentRunRequest{
			AgentID: interfaces.SemanticUnderstandingResourceAgentID,
			Input:   []byte(`{"resource":{"id":"resource-1"}}`),
		})

		require.NoError(t, err)
		assert.Equal(t, "agent-task-1", got.TaskID)
	})

	t.Run("success with fallback id", func(t *testing.T) {
		access, mockHTTPClient := setup(t)

		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), "http://bkn-agent/api/bkn-agent/v1/run", gomock.Any(), gomock.Any()).
			Return(http.StatusAccepted, []byte(`{"id":"agent-task-1"}`), nil)

		got, err := access.Run(ctx, &interfaces.BknAgentRunRequest{
			AgentID: interfaces.SemanticUnderstandingResourceAgentID,
			Input:   []byte(`{"resource":{"id":"resource-1"}}`),
		})

		require.NoError(t, err)
		assert.Equal(t, "agent-task-1", got.TaskID)
	})

	t.Run("missing url", func(t *testing.T) {
		access := newTestBknAgentAccess(&common.AppSetting{}, nil)

		got, err := access.Run(ctx, &interfaces.BknAgentRunRequest{})

		require.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("http error", func(t *testing.T) {
		access, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("network error"))

		got, err := access.Run(ctx, &interfaces.BknAgentRunRequest{
			AgentID: interfaces.SemanticUnderstandingResourceAgentID,
			Input:   []byte(`{}`),
		})

		require.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestBknAgentAccessGetTask(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*bknAgentAccess, *rmock.MockHTTPClient) {
		t.Helper()

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		appSetting := &common.AppSetting{BknAgentUrl: "http://bkn-agent"}
		mockHTTPClient := rmock.NewMockHTTPClient(ctrl)
		return newTestBknAgentAccess(appSetting, mockHTTPClient), mockHTTPClient
	}

	t.Run("success", func(t *testing.T) {
		access, mockHTTPClient := setup(t)
		respBody := []byte(`{"task_id":"agent-task-1","status":"succeeded","result":{"confidence":0.8}}`)

		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), "http://bkn-agent/api/bkn-agent/v1/tasks/agent-task-1", nil, gomock.Any()).
			Return(http.StatusOK, respBody, nil)

		got, err := access.GetTask(ctx, "agent-task-1")

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "agent-task-1", got.TaskID)
		assert.Equal(t, interfaces.BknAgentTaskStatusSucceeded, got.Status)
		assert.JSONEq(t, `{"confidence":0.8}`, string(got.Result))
	})

	t.Run("status error", func(t *testing.T) {
		access, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusInternalServerError, []byte("failed"), nil)

		got, err := access.GetTask(ctx, "agent-task-1")

		require.Error(t, err)
		assert.Nil(t, got)
	})
}
