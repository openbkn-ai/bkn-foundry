// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knactionrecall

import (
	"context"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

// ExecuteAction 执行行动（异步）。
//
// 与 GetActionInfo 配对：Agent 先用 get_action_info 拿到行动的可执行定义与
// dynamic_params schema，再用本接口填入真实动态参数值触发执行，形成
// 「发现 → 读定义 → 执行」的闭环。真正的执行与动态参数完整性校验在
// ontology-query 的 execute 端点完成，本层仅做透传。
func (s *knActionRecallServiceImpl) ExecuteAction(ctx context.Context, req *interfaces.KnActionExecuteRequest) (*interfaces.KnActionExecuteResponse, error) {
	execReq := &interfaces.ExecuteActionsRequest{
		KnID:               req.KnID,
		AtID:               req.AtID,
		InstanceIdentities: req.InstanceIdentities,
		DynamicParams:      req.DynamicParams,
	}

	resp, err := s.ontologyQuery.ExecuteActions(ctx, execReq)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnActionRecall#ExecuteAction] ExecuteActions failed, err: %v", err)
		return nil, err
	}

	return &interfaces.KnActionExecuteResponse{
		ExecutionID: resp.ExecutionID,
		Status:      resp.Status,
		Message:     resp.Message,
		CreatedAt:   resp.CreatedAt,
	}, nil
}

// GetActionExecution 查询单次行动执行的状态与结果。
// 与 execute_action 配对：Agent 用 execute_action 提交后拿到 execution_id，
// 再用本接口查询该次执行的 status 与逐对象 results。
func (s *knActionRecallServiceImpl) GetActionExecution(ctx context.Context, req *interfaces.KnGetActionExecutionRequest) (map[string]any, error) {
	resp, err := s.ontologyQuery.GetActionExecution(ctx, &interfaces.GetActionExecutionRequest{
		KnID:        req.KnID,
		ExecutionID: req.ExecutionID,
	})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnActionRecall#GetActionExecution] GetActionExecution failed, err: %v", err)
		return nil, err
	}
	return resp, nil
}

// ListActionExecutions 列出行动执行历史（可按行动类型/状态/触发方式过滤，分页）。
func (s *knActionRecallServiceImpl) ListActionExecutions(ctx context.Context, req *interfaces.KnListActionExecutionsRequest) (map[string]any, error) {
	resp, err := s.ontologyQuery.ListActionExecutions(ctx, &interfaces.ListActionExecutionsRequest{
		KnID:          req.KnID,
		ActionTypeID:  req.ActionTypeID,
		Status:        req.Status,
		TriggerType:   req.TriggerType,
		StartTimeFrom: req.StartTimeFrom,
		StartTimeTo:   req.StartTimeTo,
		Offset:        req.Offset,
		Limit:         req.Limit,
	})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnActionRecall#ListActionExecutions] ListActionExecutions failed, err: %v", err)
		return nil, err
	}
	return resp, nil
}
