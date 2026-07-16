// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics/bkn_agent"
	"vega-backend/logics/semantic_understanding_task"
)

// SemanticUnderstandingTaskWorker handles semantic-understanding execution tasks.
type SemanticUnderstandingTaskWorker struct {
	appSetting *common.AppSetting
	suts       interfaces.SemanticUnderstandingTaskService
	bas        interfaces.BknAgentService
}

// NewSemanticUnderstandingTaskWorker creates a semantic-understanding task worker.
func NewSemanticUnderstandingTaskWorker(appSetting *common.AppSetting) *SemanticUnderstandingTaskWorker {
	return &SemanticUnderstandingTaskWorker{
		appSetting: appSetting,
		suts:       semantic_understanding_task.NewSemanticUnderstandingTaskService(appSetting),
		bas:        bkn_agent.NewBknAgentService(appSetting),
	}
}

// HandleTask runs a semantic-understanding task through bkn-agent and persists the result.
func (h *SemanticUnderstandingTaskWorker) HandleTask(ctx context.Context, task *asynq.Task) error {
	var msg interfaces.SemanticUnderstandingTaskMessage
	if err := sonic.Unmarshal(task.Payload(), &msg); err != nil {
		logger.Errorf("Failed to unmarshal semantic understanding task message: %v", err)
		return err
	}
	if msg.TaskID == "" {
		return fmt.Errorf("semantic understanding task id is required")
	}

	taskInfo, err := h.suts.GetByID(ctx, msg.TaskID)
	if err != nil {
		logger.Errorf("Failed to get semantic understanding task %s: %v", msg.TaskID, err)
		return err
	}
	if taskInfo == nil {
		return fmt.Errorf("semantic understanding task %s not found", msg.TaskID)
	}
	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusSucceeded ||
		taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusFailed {
		logger.Infof("Semantic understanding task already finished: id=%s, status=%s", taskInfo.ID, taskInfo.Status)
		return nil
	}

	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, taskInfo.Creator)

	agentTaskID := taskInfo.AgentTaskID
	if agentTaskID == "" {
		agentTaskID, err = h.bas.Run(ctx, taskInfo)
		if err != nil {
			_, _ = h.suts.MarkFailed(ctx, taskInfo.ID, err.Error())
			return err
		}

		running, err := h.suts.MarkRunning(ctx, taskInfo.ID, agentTaskID)
		if err != nil {
			return err
		}
		if !running {
			logger.Infof("Semantic understanding task was not claimed for running: id=%s", taskInfo.ID)
			return nil
		}
	}

	agentTask, err := h.bas.WaitResult(ctx, agentTaskID)
	if err != nil {
		_, _ = h.suts.MarkFailed(ctx, taskInfo.ID, err.Error())
		return err
	}
	if agentTask.Status == interfaces.BknAgentTaskStatusFailed {
		_, _ = h.suts.MarkFailed(ctx, taskInfo.ID, bknAgentFailureDetail(agentTask))
		return nil
	}

	resultJSON, confidence, confidenceDetailJSON, err := parseBknAgentResult(agentTask)
	if err != nil {
		_, _ = h.suts.MarkFailed(ctx, taskInfo.ID, err.Error())
		return nil
	}

	_, err = h.suts.MarkSucceeded(ctx, taskInfo.ID, resultJSON, confidence, confidenceDetailJSON)
	return err
}

func bknAgentFailureDetail(agentTask *interfaces.BknAgentTask) string {
	if agentTask == nil {
		return "agent task failed"
	}
	if agentTask.FailureDetail != "" {
		return agentTask.FailureDetail
	}
	if agentTask.Error != "" {
		return agentTask.Error
	}
	return fmt.Sprintf("agent task %s failed", agentTask.TaskID)
}

func parseBknAgentResult(agentTask *interfaces.BknAgentTask) (string, float64, string, error) {
	if agentTask == nil {
		return "", 0, "", fmt.Errorf("agent task result is required")
	}
	result := agentTask.Result
	if len(result) == 0 {
		result = agentTask.ResultJSON
	}
	if len(result) == 0 {
		return "", 0, "", fmt.Errorf("agent task result is empty")
	}

	resultObject := map[string]json.RawMessage{}
	if err := json.Unmarshal(result, &resultObject); err != nil {
		return "", 0, "", fmt.Errorf("unmarshal agent task result failed: %w", err)
	}

	var confidence float64
	confidenceRaw, ok := resultObject["confidence"]
	if !ok {
		return "", 0, "", fmt.Errorf("agent task result missing confidence")
	}
	if err := json.Unmarshal(confidenceRaw, &confidence); err != nil {
		return "", 0, "", fmt.Errorf("unmarshal agent task confidence failed: %w", err)
	}
	if confidence < 0 || confidence > 1 {
		return "", 0, "", fmt.Errorf("agent task confidence must be between 0 and 1")
	}

	detail := make(map[string]json.RawMessage)
	for _, key := range []string{
		"table",
		"fields",
		"logic_views",
		"obsolete_logic_views",
		"warnings",
		"confidence_detail",
		"confidence_details",
	} {
		if value, ok := resultObject[key]; ok {
			detail[key] = value
		}
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return "", 0, "", fmt.Errorf("marshal confidence detail failed: %w", err)
	}

	return string(result), confidence, string(detailJSON), nil
}
