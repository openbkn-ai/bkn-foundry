// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knactionrecall

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// ExecuteAction Execute an action (asynchronously).
//
// Paired with GetActionInfo: Agent first uses get_action_info to get the executable definition and.
// dynamic_params schema, and then use this interface to fill in the real dynamic parameter values to trigger execution, forming.
// The closed loop of "discover → read definition → execute". True execution and dynamic parameter integrity checking in.
// The execute endpoint of ontology-query is completed, and this layer only performs transparent transmission.
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

// GetActionExecution queries the status and results of a single action execution.
// Paired with execute_action: Agent gets execution_id after submitting with execute_action.
// Then use this interface to query the status and object-by-object results of this execution.
//
// The returned results will eliminate heavy items that are not used for Agent decision-making (action_type_snapshot, duplicate.
// executor/action_source, paging metadata, etc.), only the status, count and per-object results are retained.
// To reduce token usage.
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

// actionExecutionKeepKeys is a top-level field in the single execution details that is useful to the Agent and needs to be retained.
// execution_mode/target_count must be left: in once mode, total_count is the number of tool calls (always 1),
// Without these two fields, the Agent will misread "30 instances merged into 1 call" as "only 1 object was processed.".
var actionExecutionKeepKeys = []string{
	"id", "kn_id", "action_type_id", "action_type_name",
	"status", "trigger_type", "execution_mode", "target_count",
	"total_count", "success_count", "failed_count",
	"start_time", "end_time", "duration_ms", "dynamic_params", "results",
}

// slimActionExecution projects the slim structure from the execution details returned by the backend:
// Remove action_type_snapshot, executor(_id), action_source, object_type_id,.
// results_limit/offset/total etc. are redundant and compress per-object results.
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

// actionResultKeepKeys are fields that need to be kept in object-by-object results.
// Targets only appear in once mode. They contain the instance covered by this call and are judged by the Agent.
// The only basis for "which objects this aggregation result corresponds to".
var actionResultKeepKeys = []string{
	"_instance_id", "_instance_identity", "_display", "targets",
	"status", "parameters", "duration_ms", "error_message", "result",
}

// maxSlimTargets is the upper limit of returns of targets in a single result.
// In the once mode, results is always 1, results_limit only cuts the number of results, and cannot cut the targets within the strip.
// A scan without _instance_identities can hit at most ACTION_EXECUTION_MAX_OBJECTS.
// (Default 10000) instances. There is no upper limit, and MB-level instance details can be poured into the Agent context in one query.
// Contrary to the purpose of this laminated token. For the total number of coverage, see target_count. Only samples are given here.
const maxSlimTargets = 20

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
		if targets, ok := out["targets"].([]any); ok && len(targets) > maxSlimTargets {
			out["targets"] = targets[:maxSlimTargets]
			out["targets_total"] = len(targets)
			out["targets_truncated"] = true
		}
		slim = append(slim, out)
	}
	return slim
}

// ListActionExecutions lists action execution history (can be filtered and paging by action type/status/trigger method).
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
	// Each item in the list also excludes heavy items (action_type_snapshot, etc.), leaving only the overview field.
	// Results are not included in the list: instance-by-instance details are only given in get_action_execution, and the list takes the task summary.
	if entries, ok := resp["entries"].([]any); ok {
		slimmed := make([]any, 0, len(entries))
		for _, e := range entries {
			if m, ok := e.(map[string]any); ok {
				summary := slimActionExecution(m)
				delete(summary, "results")
				slimmed = append(slimmed, summary)
			} else {
				slimmed = append(slimmed, e)
			}
		}
		resp["entries"] = slimmed
	}
	return resp, nil
}
