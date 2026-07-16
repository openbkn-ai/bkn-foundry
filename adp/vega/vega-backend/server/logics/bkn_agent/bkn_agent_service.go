// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package bkn_agent provides bkn-agent semantic-understanding orchestration.
package bkn_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
)

var (
	baServiceOnce sync.Once
	baService     interfaces.BknAgentService
)

type bknAgentService struct {
	appSetting   *common.AppSetting
	baa          interfaces.BknAgentAccess
	pollInterval time.Duration
	maxPolls     int
}

func NewBknAgentService(appSetting *common.AppSetting) interfaces.BknAgentService {
	baServiceOnce.Do(func() {
		baService = &bknAgentService{
			appSetting:   appSetting,
			baa:          logics.BAA,
			pollInterval: 2 * time.Second,
			maxPolls:     300,
		}
	})
	return baService
}

func (s *bknAgentService) Run(ctx context.Context, task *interfaces.SemanticUnderstandingTask) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BknAgentService.Run")
	defer span.End()

	if task == nil {
		return "", fmt.Errorf("semantic understanding task is required")
	}
	if task.AgentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}
	if task.Input == "" {
		return "", fmt.Errorf("input is required")
	}
	if !json.Valid([]byte(task.Input)) {
		return "", fmt.Errorf("input must be valid json")
	}

	resp, err := s.baa.Run(ctx, &interfaces.BknAgentRunRequest{
		AgentID: task.AgentID,
		Message: task.Input,
	})
	if err != nil {
		span.SetStatus(codes.Error, "Run bkn-agent failed")
		return "", err
	}
	if resp == nil || resp.TaskID == "" {
		return "", fmt.Errorf("agent run response missing task_id")
	}
	return resp.TaskID, nil
}

func (s *bknAgentService) WaitResult(ctx context.Context, agentTaskID string) (*interfaces.BknAgentTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BknAgentService.WaitResult")
	defer span.End()

	if agentTaskID == "" {
		return nil, fmt.Errorf("agent task id is required")
	}

	maxPolls := s.maxPolls
	if maxPolls <= 0 {
		maxPolls = 1
	}
	for i := 0; i < maxPolls; i++ {
		task, err := s.baa.GetTask(ctx, agentTaskID)
		if err != nil {
			span.SetStatus(codes.Error, "Get bkn-agent task failed")
			return nil, err
		}
		if task == nil {
			return nil, fmt.Errorf("agent task %s not found", agentTaskID)
		}
		switch task.Status {
		case interfaces.BknAgentTaskStatusSucceeded,
			interfaces.BknAgentTaskStatusFailed:
			return task, nil
		case interfaces.BknAgentTaskStatusPending,
			interfaces.BknAgentTaskStatusRunning:
		default:
			return nil, fmt.Errorf("unknown agent task status: %s", task.Status)
		}

		if i == maxPolls-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.pollInterval):
		}
	}
	return nil, fmt.Errorf("agent task %s did not finish after %d polls", agentTaskID, maxPolls)
}
