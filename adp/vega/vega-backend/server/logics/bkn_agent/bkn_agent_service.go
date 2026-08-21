// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package bkn_agent provides bkn-agent semantic-understanding orchestration.
package bkn_agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
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
	appSetting *common.AppSetting
	baa        interfaces.BknAgentAccess
}

func NewBknAgentService(appSetting *common.AppSetting) interfaces.BknAgentService {
	baServiceOnce.Do(func() {
		baService = &bknAgentService{
			appSetting: appSetting,
			baa:        logics.BAA,
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
	if !sonic.Valid([]byte(task.Input)) {
		return "", fmt.Errorf("input must be valid json")
	}
	responseFormat, err := semanticUnderstandingResponseFormat(task.Scope)
	if err != nil {
		return "", err
	}

	resp, err := s.baa.Run(ctx, &interfaces.BknAgentRunRequest{
		AgentID:        task.AgentID,
		Message:        task.Input,
		ResponseFormat: responseFormat,
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

func (s *bknAgentService) GetTask(ctx context.Context, agentTaskID string) (*interfaces.BknAgentTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BknAgentService.GetTask")
	defer span.End()

	if agentTaskID == "" {
		return nil, fmt.Errorf("agent task id is required")
	}

	task, err := s.baa.GetTask(ctx, agentTaskID)
	if err != nil {
		span.SetStatus(codes.Error, "Get bkn-agent task failed")
	}
	return task, err
}
