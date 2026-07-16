// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func semanticUnderstandingWorkerTask(t *testing.T, taskID string) *asynq.Task {
	t.Helper()

	payload, err := sonic.Marshal(&interfaces.SemanticUnderstandingTaskMessage{TaskID: taskID})
	require.NoError(t, err)
	return asynq.NewTask(interfaces.SemanticUnderstandingTaskType, payload)
}

func TestSemanticUnderstandingTaskWorkerHandleTask(t *testing.T) {
	t.Run("runs agent and marks succeeded", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
		}

		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:      "semantic-task-1",
			Status:  interfaces.SemanticUnderstandingTaskStatusPending,
			AgentID: interfaces.SemanticUnderstandingResourceAgentID,
			Input:   `{"resource":{"id":"resource-1"}}`,
			Creator: interfaces.AccountInfo{ID: "account-1"},
		}

		taskService.EXPECT().
			GetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		agentService.EXPECT().
			Run(gomock.Any(), semanticTask).
			Return("agent-task-1", nil)
		taskService.EXPECT().
			MarkRunning(gomock.Any(), "semantic-task-1", "agent-task-1").
			Return(true, nil)
		agentService.EXPECT().
			WaitResult(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID: "agent-task-1",
				Status: interfaces.BknAgentTaskStatusSucceeded,
				Result: []byte(`{"confidence":0.82,"fields":[{"name":"id"}],"warnings":[]}`),
			}, nil)
		taskService.EXPECT().
			MarkSucceeded(gomock.Any(), "semantic-task-1", `{"confidence":0.82,"fields":[{"name":"id"}],"warnings":[]}`, 0.82, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ string, _ float64, detailJSON string) (bool, error) {
				var detail map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(detailJSON), &detail))
				assert.Contains(t, detail, "fields")
				assert.Contains(t, detail, "warnings")
				return true, nil
			})

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

		require.NoError(t, err)
	})

	t.Run("marks failed when agent task failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
		}

		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:          "semantic-task-1",
			Status:      interfaces.SemanticUnderstandingTaskStatusRunning,
			AgentTaskID: "agent-task-1",
			Creator:     interfaces.AccountInfo{ID: "account-1"},
		}

		taskService.EXPECT().
			GetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		agentService.EXPECT().
			WaitResult(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID:        "agent-task-1",
				Status:        interfaces.BknAgentTaskStatusFailed,
				FailureDetail: "agent failed",
			}, nil)
		taskService.EXPECT().
			MarkFailed(gomock.Any(), "semantic-task-1", "agent failed").
			Return(true, nil)

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

		require.NoError(t, err)
	})
}

func TestParseBknAgentResult(t *testing.T) {
	gotResult, gotConfidence, gotDetail, err := parseBknAgentResult(&interfaces.BknAgentTask{
		Result: []byte(`{"confidence":0.9,"fields":[{"name":"name"}],"ignored":true}`),
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"confidence":0.9,"fields":[{"name":"name"}],"ignored":true}`, gotResult)
	assert.Equal(t, 0.9, gotConfidence)
	assert.JSONEq(t, `{"fields":[{"name":"name"}]}`, gotDetail)
}
