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
//
// 返回结果会剔除 Agent 决策用不到的重货（action_type_snapshot、重复的
// executor/action_source、分页元数据等），仅保留状态、计数与逐对象结果，
// 以降低 token 占用。
func (s *knActionRecallServiceImpl) GetActionExecution(ctx context.Context, req *interfaces.KnGetActionExecutionRequest) (map[string]any, error) {
	resp, err := s.ontologyQuery.GetActionExecution(ctx, &interfaces.GetActionExecutionRequest{
		KnID:        req.KnID,
		ExecutionID: req.ExecutionID,
	})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnActionRecall#GetActionExecution] GetActionExecution failed, err: %v", err)
		return nil, err
	}
	return slimActionExecution(resp), nil
}

// actionExecutionKeepKeys 是单次执行详情中对 Agent 有用、需保留的顶层字段。
var actionExecutionKeepKeys = []string{
	"id", "kn_id", "action_type_id", "action_type_name",
	"status", "trigger_type", "total_count", "success_count", "failed_count",
	"start_time", "end_time", "duration_ms", "dynamic_params", "results",
}

// slimActionExecution 从后端返回的执行详情中投影出精简结构：
// 剔除 action_type_snapshot、executor(_id)、action_source、object_type_id、
// results_limit/offset/total 等冗余，并压缩逐对象结果。
func slimActionExecution(full map[string]any) map[string]any {
	if full == nil {
		return nil
	}
	slim := make(map[string]any, len(actionExecutionKeepKeys))
	for _, k := range actionExecutionKeepKeys {
		if v, ok := full[k]; ok {
			slim[k] = v
		}
	}
	if raw, ok := slim["results"].([]any); ok {
		slim["results"] = slimActionResults(raw)
	}
	return slim
}

// actionResultKeepKeys 是逐对象结果中需保留的字段。
var actionResultKeepKeys = []string{
	"_instance_id", "_instance_identity", "_display",
	"status", "parameters", "duration_ms", "error_message", "result",
}

func slimActionResults(results []any) []any {
	slim := make([]any, 0, len(results))
	for _, item := range results {
		r, ok := item.(map[string]any)
		if !ok {
			slim = append(slim, item)
			continue
		}
		out := make(map[string]any, len(actionResultKeepKeys))
		for _, k := range actionResultKeepKeys {
			if v, ok := r[k]; ok {
				out[k] = v
			}
		}
		slim = append(slim, out)
	}
	return slim
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
		SearchAfter:   req.SearchAfter,
	})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnActionRecall#ListActionExecutions] ListActionExecutions failed, err: %v", err)
		return nil, err
	}
	// 列表每条同样剔除重货（action_type_snapshot 等），仅保留概览字段
	if entries, ok := resp["entries"].([]any); ok {
		slimmed := make([]any, 0, len(entries))
		for _, e := range entries {
			if m, ok := e.(map[string]any); ok {
				slimmed = append(slimmed, slimActionExecution(m))
			} else {
				slimmed = append(slimmed, e)
			}
		}
		resp["entries"] = slimmed
	}
	return resp, nil
}
