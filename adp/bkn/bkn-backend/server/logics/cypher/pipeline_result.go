// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func scopeRow(entry map[string]any) map[string]any {
	row := make(map[string]any, len(entry))
	for name, value := range entry {
		row["unwind:"+name] = value
	}
	return row
}

func evaluateCondition(plan *Plan, expression *GraphExpression, row map[string]any) (bool, error) {
	if expression == nil {
		return true, nil
	}
	value, err := evaluateGraph(plan, expression, row)
	if err != nil {
		return false, err
	}
	if value == nil {
		return false, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, expressionTypeError()
	}
	return boolean, nil
}

func pipelineError(ctx context.Context, err error) error {
	if errors.Is(err, errGraphBudget) {
		return unionFailure(ctx, "staged evaluation exceeded its shared row or payload budget; no complete result is available")
	}
	var failure *expressionError
	if errors.As(err, &failure) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_QueryFailed).WithErrorDetails(map[string]string{"phase": "evaluate", "kind": failure.Kind, "message": failure.Message})
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return executionError(ctx, err)
	}
	return rest.NewHTTPError(ctx, http.StatusBadGateway, berrors.BknBackend_Cypher_QueryFailed).WithErrorDetails("staged observation could not be materialized")
}

type pipelineBudget struct{ rows, bytes, comparisons int }

func (b *pipelineBudget) account(rows []map[string]any, candidates int) error {
	b.rows += max(len(rows), candidates)
	if b.rows > graphMatchBudget {
		return errGraphBudget
	}
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		b.bytes += len(encoded) + 64
		if b.bytes > graphPayloadBudget {
			return errGraphBudget
		}
	}
	return nil
}

func (s *cypherQueryService) runPipeline(ctx context.Context, query interfaces.CypherQuery, compiled *compiledQuery) (*interfaces.CypherQueryResult, error) {
	rows := []map[string]any{{}}
	budget := &pipelineBudget{}
	objects := map[string]string{}
	candidates := 0
	limited := false
	var result *interfaces.CypherQueryResult
	for index, stage := range compiled.plan.Stages {
		if err := ctx.Err(); err != nil {
			return nil, pipelineError(ctx, err)
		}
		child := compiled.stages[index]
		switch stage.Kind {
		case "match":
			var err error
			var count int
			rows, count, err = s.runMatchStage(ctx, query, child, stage, rows, budget, objects)
			if err != nil {
				return nil, err
			}
			candidates += count
			if err := budget.account(rows, count); err != nil {
				return nil, pipelineError(ctx, err)
			}
		case "unwind":
			unwind := stage.Plan.Result.Unwinds[0]
			expanded := []map[string]any{}
			for _, row := range rows {
				if err := ctx.Err(); err != nil {
					return nil, pipelineError(ctx, err)
				}
				value, err := evaluateGraph(stage.Plan, unwind.Value, row)
				if err != nil {
					return nil, pipelineError(ctx, err)
				}
				if value == nil {
					continue
				}
				values, ok := value.([]any)
				if !ok {
					values = []any{value}
				}
				for _, item := range values {
					if len(expanded) >= graphMatchBudget {
						return nil, pipelineError(ctx, errGraphBudget)
					}
					next := make(map[string]any, len(row)+1)
					for name, value := range row {
						next[name] = value
					}
					next["unwind:"+unwind.Variable] = item
					expanded = append(expanded, next)
				}
			}
			rows = expanded
			if err := budget.account(rows, 0); err != nil {
				return nil, pipelineError(ctx, err)
			}
		case "with", "return":
			var err error
			result, err = assembleGraph(ctx, stage.Plan, rows)
			if err != nil {
				return nil, pipelineError(ctx, err)
			}
			limited = limited || result.Execution.ConclusionsLimited
			if err := budget.account(result.Entries, len(rows)); err != nil {
				return nil, pipelineError(ctx, err)
			}
			rows = make([]map[string]any, 0, len(result.Entries))
			for _, entry := range result.Entries {
				rows = append(rows, scopeRow(entry))
			}
		}
	}
	result.TraceDescriptor = compiled.traceDescriptor
	result.Execution.CandidateRows = candidates
	result.Execution.ConclusionsLimited = limited
	result.EvaluationRows = budget.rows
	return result, nil
}

func (s *cypherQueryService) runMatchStage(ctx context.Context, query interfaces.CypherQuery, compiled *compiledQuery, stage PlanStage, incoming []map[string]any, budget *pipelineBudget, objects map[string]string) ([]map[string]any, int, error) {
	if len(incoming) == 0 {
		return []map[string]any{}, 0, nil
	}
	response, err := s.execute(ctx, query, compiled.plan, interfaces.RawQueryRequest{
		Query: compiled.statement, QueryFormat: interfaces.VEGA_QUERY_FORMAT_SQL, InputDialect: interfaces.VEGA_DIALECT_MYSQL,
		Paging: interfaces.ResourceDataPagingRequest{Mode: interfaces.VEGA_PAGING_MODE_SINGLE, Limit: compiled.rowLimit}, QueryTimeoutSec: interfaces.CYPHER_DEFAULT_TIMEOUT_SEC,
	})
	if err != nil {
		return nil, 0, executionError(ctx, err)
	}
	if response == nil {
		return nil, 0, pipelineError(ctx, errors.New("missing observation"))
	}
	if len(response.Entries) > graphMatchBudget || response.TotalCount > int64(len(response.Entries)) || (response.Paging != nil && response.Paging.NextCursor != nil) {
		return nil, 0, unionFailure(ctx, "staged MATCH observation is incomplete; no complete result is available")
	}
	// Account raw source bytes, including dependencies hidden from public JSON.
	for _, row := range response.Entries {
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, 0, pipelineError(ctx, err)
		}
		budget.bytes += len(encoded) + 64
		if budget.bytes > graphPayloadBudget {
			return nil, 0, pipelineError(ctx, errGraphBudget)
		}
	}
	materialized := make([]map[string]any, 0, len(response.Entries))
	for _, raw := range response.Entries {
		entry := map[string]any{}
		for _, projection := range compiled.plan.Result.Projections {
			value, err := evaluateGraph(compiled.plan, projection.Value, raw)
			if err != nil {
				return nil, 0, pipelineError(ctx, err)
			}
			entry[projection.Alias] = value
		}
		materialized = append(materialized, entry)
	}
	output := []map[string]any{}
	for _, source := range incoming {
		matched := false
		for i, entry := range materialized {
			if err := ctx.Err(); err != nil {
				return nil, 0, pipelineError(ctx, err)
			}
			budget.comparisons++
			if budget.comparisons > graphMatchBudget*8 {
				return nil, 0, pipelineError(ctx, errGraphBudget)
			}
			compatible := true
			for _, name := range stage.Shared {
				equal, err := expressionEquality(source["unwind:"+name], entry[name])
				if err != nil {
					return nil, 0, pipelineError(ctx, err)
				}
				if equal != true {
					compatible = false
					break
				}
			}
			if !compatible {
				continue
			}
			row := make(map[string]any, len(source)+len(entry))
			for name, value := range source {
				row[name] = value
			}
			for name, value := range entry {
				if _, exists := row["unwind:"+name]; !exists {
					row["unwind:"+name] = value
				}
			}
			// MATCH predicates also need the fresh physical columns.
			for name, value := range response.Entries[i] {
				row[name] = value
			}
			accepted, err := evaluateCondition(compiled.plan, compiled.plan.Result.Where, row)
			if err != nil {
				return nil, 0, pipelineError(ctx, err)
			}
			if !accepted {
				continue
			}
			for name := range response.Entries[i] {
				delete(row, name)
			}
			if err := checkGraphObservation(entry, objects); err != nil {
				return nil, 0, pipelineError(ctx, err)
			}
			output = append(output, row)
			matched = true
			if len(output) > graphMatchBudget {
				return nil, 0, pipelineError(ctx, errGraphBudget)
			}
		}
		if !matched && stage.Optional {
			row := make(map[string]any, len(source)+len(compiled.plan.Result.Projections))
			for name, value := range source {
				row[name] = value
			}
			for _, projection := range compiled.plan.Result.Projections {
				if _, exists := row["unwind:"+projection.Alias]; !exists {
					row["unwind:"+projection.Alias] = nil
				}
			}
			output = append(output, row)
		}
	}
	return output, len(response.Entries), nil
}
