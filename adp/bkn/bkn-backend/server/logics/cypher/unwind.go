// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// assembleUnwinds filters MATCH rows before expanding list variables, then
// uses the common grouping, ordering and paging evaluator on the expanded rows.
func assembleUnwinds(ctx context.Context, plan *Plan, rows []map[string]any) (*interfaces.CypherQueryResult, error) {
	logical := *plan.Result
	expanded := []map[string]any{}
	observedBytes := 0
	var expand func(map[string]any, int) error
	expand = func(row map[string]any, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index == len(logical.Unwinds) {
			if len(expanded) >= graphMatchBudget {
				return errGraphBudget
			}
			expanded = append(expanded, row)
			return nil
		}
		unwind := logical.Unwinds[index]
		value, err := evaluateGraph(plan, unwind.Value, row)
		if err != nil {
			return err
		}
		if value == nil {
			return nil
		}
		values, ok := value.([]any)
		if !ok {
			values = []any{value}
		}
		for _, item := range values {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := make(map[string]any, len(row)+1)
			for name, v := range row {
				child[name] = v
			}
			child["unwind:"+unwind.Variable] = item
			encoded, err := json.Marshal(child)
			if err != nil {
				return err
			}
			observedBytes += len(encoded) + 64
			if observedBytes > graphPayloadBudget {
				return errGraphBudget
			}
			if err := expand(child, index+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, row := range rows {
		if logical.Where != nil {
			value, err := evaluateGraph(plan, logical.Where, row)
			if err != nil {
				return nil, err
			}
			if value == nil {
				continue
			}
			boolean, ok := value.(bool)
			if !ok {
				return nil, expressionTypeError()
			}
			if !boolean {
				continue
			}
		}
		if err := expand(row, 0); err != nil {
			return nil, err
		}
	}
	logical.Unwinds = nil
	logical.Where = nil
	copyPlan := *plan
	copyPlan.Result = &logical
	result, err := assembleGraph(ctx, &copyPlan, expanded)
	if err != nil {
		return nil, err
	}
	result.Execution.CandidateRows = len(rows)
	return result, nil
}
