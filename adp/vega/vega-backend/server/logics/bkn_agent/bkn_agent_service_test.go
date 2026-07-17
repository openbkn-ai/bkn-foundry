// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn_agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestBknAgentServiceRun(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	agentAccess := vmock.NewMockBknAgentAccess(ctrl)
	service := &bknAgentService{baa: agentAccess}

	agentAccess.EXPECT().
		Run(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.BknAgentRunRequest{})).
		DoAndReturn(func(_ context.Context, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentRunResponse, error) {
			assert.Equal(t, interfaces.SemanticUnderstandingResourceAgentID, req.AgentID)
			assert.JSONEq(t, `{"resource":{"id":"resource-1"}}`, req.Message)
			assert.Equal(t, "object", req.ResponseFormat["type"])
			assert.Equal(t, []string{"confidence", "resource", "fields", "warnings"}, req.ResponseFormat["required"])
			return &interfaces.BknAgentRunResponse{TaskID: "agent-task-1"}, nil
		})

	got, err := service.Run(context.Background(), &interfaces.SemanticUnderstandingTask{
		AgentID: interfaces.SemanticUnderstandingResourceAgentID,
		Input:   `{"resource":{"id":"resource-1"}}`,
		Scope:   interfaces.SemanticUnderstandingTaskScopeResource,
	})

	require.NoError(t, err)
	assert.Equal(t, "agent-task-1", got)
}

func TestBknAgentServiceWaitResult(t *testing.T) {
	t.Run("polls until succeeded", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		agentAccess := vmock.NewMockBknAgentAccess(ctrl)
		service := &bknAgentService{
			baa:          agentAccess,
			pollInterval: time.Millisecond,
			maxPolls:     2,
		}

		agentAccess.EXPECT().
			GetTask(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID: "agent-task-1",
				Status: interfaces.BknAgentTaskStatusRunning,
			}, nil)
		agentAccess.EXPECT().
			GetTask(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID: "agent-task-1",
				Status: interfaces.BknAgentTaskStatusSucceeded,
				Result: []byte(`{"confidence":0.8}`),
			}, nil)

		got, err := service.WaitResult(context.Background(), "agent-task-1")

		require.NoError(t, err)
		assert.Equal(t, interfaces.BknAgentTaskStatusSucceeded, got.Status)
	})

	t.Run("returns failed task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		agentAccess := vmock.NewMockBknAgentAccess(ctrl)
		service := &bknAgentService{baa: agentAccess, maxPolls: 1}

		agentAccess.EXPECT().
			GetTask(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID:        "agent-task-1",
				Status:        interfaces.BknAgentTaskStatusFailed,
				FailureDetail: "agent failed",
			}, nil)

		got, err := service.WaitResult(context.Background(), "agent-task-1")

		require.NoError(t, err)
		assert.Equal(t, interfaces.BknAgentTaskStatusFailed, got.Status)
		assert.Equal(t, "agent failed", got.FailureDetail)
	})
}
