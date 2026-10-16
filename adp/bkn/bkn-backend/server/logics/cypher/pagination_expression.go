// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

func analyzePagination(ctx parsing.IOC_ExpressionContext, clause string) (*int64, ValueExpression, error) {
	value, originalError := analyzeRowCount(ctx, clause)
	if originalError == nil {
		return &value, nil, nil
	}
	expression, err := readValueExpression(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	if _, literal := expression.(LiteralExpression); literal {
		return nil, nil, originalError
	}
	if operation, ok := expression.(OperationExpression); ok && operation.Operator == "NEGATE" {
		if _, literal := operation.Arguments[0].(LiteralExpression); literal {
			return nil, nil, originalError
		}
	}
	return nil, expression, nil
}

func constantPaginationExpression(expression ValueExpression, locals map[string]bool) bool {
	if expression == nil {
		return true
	}
	switch value := expression.(type) {
	case LiteralExpression, ParameterExpression:
		return true
	case VariableExpression:
		return locals[value.Name]
	case PropertyExpression:
		return locals[value.Ref.Variable]
	case OperationExpression:
		for _, argument := range value.Arguments {
			if !constantPaginationExpression(argument, locals) {
				return false
			}
		}
		return true
	case MapExpression:
		for _, field := range value.Fields {
			if !constantPaginationExpression(field.Value, locals) {
				return false
			}
		}
		return true
	case CollectionExpression:
		if !constantPaginationExpression(value.Source, locals) {
			return false
		}
		scope := map[string]bool{}
		for name, bound := range locals {
			scope[name] = bound
		}
		scope[value.Variable] = true
		return constantPaginationExpression(value.Predicate, scope) && constantPaginationExpression(value.Projection, scope)
	default:
		return false
	}
}

func bindQueryPagination(query *Query, parameters map[string]any) (*Query, error) {
	if query.SkipExpression == nil && query.LimitExpression == nil && len(query.Stages) == 0 && len(query.Union) == 0 {
		return query, nil
	}
	bound := *query
	bind := func(expression ValueExpression) (*int64, error) {
		if !constantPaginationExpression(expression, nil) {
			return nil, planErrorf(Position{}, "pagination cannot read graph facts, aggregates or query variables")
		}
		planner := &planner{parameters: parameters, tableOf: map[string]int{}, bindings: map[string]*GraphExpression{}, plan: &Plan{}}
		value, err := planner.graphExpression(expression, nil, CompileOptions{Parameters: parameters})
		if err != nil {
			return nil, err
		}
		evaluated, err := evaluateGraph(planner.plan, value, evaluationRow(map[string]any{}, requestEvaluationState(context.Background())))
		if err != nil {
			return nil, planErrorf(Position{}, "invalid pagination: %v", err)
		}
		integer, err := expressionInteger(evaluated)
		if err != nil || integer < 0 {
			return nil, planErrorf(Position{}, "pagination requires a non-negative integer")
		}
		return &integer, nil
	}
	var err error
	if query.SkipExpression != nil {
		bound.Skip, err = bind(query.SkipExpression)
		if err != nil {
			return nil, err
		}
		bound.SkipExpression = nil
	}
	if query.LimitExpression != nil {
		bound.Limit, err = bind(query.LimitExpression)
		if err != nil {
			return nil, err
		}
		bound.LimitExpression = nil
	}
	bound.Stages = append([]QueryStage(nil), query.Stages...)
	for i, stage := range query.Stages {
		bound.Stages[i].Query, err = bindQueryPagination(stage.Query, parameters)
		if err != nil {
			return nil, err
		}
	}
	bound.Union = append([]*Query(nil), query.Union...)
	for i, branch := range query.Union {
		bound.Union[i], err = bindQueryPagination(branch, parameters)
		if err != nil {
			return nil, err
		}
	}
	return &bound, nil
}
