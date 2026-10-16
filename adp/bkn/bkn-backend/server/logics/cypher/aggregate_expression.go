// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// GraphAggregation lifts aggregate leaves out of arbitrary scalar projections.
// Row-dependent subexpressions become grouping keys, while constants remain
// evaluable even for the empty global group.
type GraphAggregation struct {
	Inputs, Outputs []GraphProjection
}

func containsGraphAggregate(value *GraphExpression) bool {
	if value == nil {
		return false
	}
	if value.Element != nil {
		return true
	}
	if value.Collection != nil && containsGraphAggregate(value.Collection.Source) {
		return true
	}
	for _, argument := range value.Arguments {
		if containsGraphAggregate(argument) {
			return true
		}
	}
	for _, field := range value.Fields {
		if containsGraphAggregate(field) {
			return true
		}
	}
	return false
}

func graphDependsOnRow(value *GraphExpression, locals map[string]bool) bool {
	if value == nil {
		return false
	}
	if value.Binding != "" {
		return !locals[value.Binding]
	}
	if value.Object != nil || value.Relation != nil || value.Column != "" || value.Path != nil {
		return true
	}
	if value.Subquery != nil {
		return len(value.Subquery.Outer) > 0
	}
	if collection := value.Collection; collection != nil {
		if graphDependsOnRow(collection.Source, locals) {
			return true
		}
		scope := map[string]bool{}
		for key, item := range locals {
			scope[key] = item
		}
		scope[collection.Variable] = true
		return graphDependsOnRow(collection.Predicate, scope) || graphDependsOnRow(collection.Projection, scope)
	}
	for _, argument := range value.Arguments {
		if graphDependsOnRow(argument, locals) {
			return true
		}
	}
	for _, field := range value.Fields {
		if graphDependsOnRow(field, locals) {
			return true
		}
	}
	return false
}

func prepareGraphAggregation(result *GraphResultPlan) {
	needsLifting := false
	for _, projection := range result.Projections {
		if containsGraphAggregate(projection.Value) {
			result.HasAggregates = true
			if projection.Value.Element == nil {
				needsLifting = true
			}
		}
	}
	if !result.HasAggregates {
		return
	}
	// Constants alongside aggregates need the empty global group as well.
	for _, projection := range result.Projections {
		if projection.Value.Element == nil && !graphDependsOnRow(projection.Value, nil) {
			needsLifting = true
		}
	}
	if !needsLifting {
		return
	}
	aggregation := &GraphAggregation{}
	var lift func(*GraphExpression) *GraphExpression
	lift = func(value *GraphExpression) *GraphExpression {
		if value.Element != nil || (!containsGraphAggregate(value) && graphDependsOnRow(value, nil)) {
			name := fmt.Sprintf("\x00aggregate-input-%d", len(aggregation.Inputs))
			aggregation.Inputs = append(aggregation.Inputs, GraphProjection{Alias: name, Value: value})
			return &GraphExpression{Binding: name, Type: value.Type, Shape: value}
		}
		if !containsGraphAggregate(value) {
			return value
		}
		copy := *value
		if value.Collection != nil {
			collection := *value.Collection
			collection.Source = lift(collection.Source)
			copy.Collection = &collection
		}
		copy.Arguments = nil
		for _, argument := range value.Arguments {
			copy.Arguments = append(copy.Arguments, lift(argument))
		}
		if value.Fields != nil {
			copy.Fields = map[string]*GraphExpression{}
			for _, name := range sortedGraphKeys(value.Fields) {
				copy.Fields[name] = lift(value.Fields[name])
			}
		}
		return &copy
	}
	for _, projection := range result.Projections {
		aggregation.Outputs = append(aggregation.Outputs, GraphProjection{Alias: projection.Alias, Value: lift(projection.Value)})
	}
	result.Aggregation = aggregation
}

func assembleAggregateExpressions(ctx context.Context, plan *Plan, rows []map[string]any) (*interfaces.CypherQueryResult, error) {
	logical := plan.Result
	input := *logical
	input.Aggregation = nil
	input.Projections = logical.Aggregation.Inputs
	input.Distinct = false
	input.Skip = 0
	input.Limit = graphMatchBudget
	input.OrderBy = nil
	input.PostWhere = nil
	inputPlan := *plan
	inputPlan.Result = &input
	groups, err := assembleGraph(ctx, &inputPlan, rows)
	if err != nil {
		return nil, err
	}
	output := *logical
	output.Aggregation = nil
	output.HasAggregates = false
	output.Where = nil
	output.Projections = logical.Aggregation.Outputs
	outputPlan := *plan
	outputPlan.Result = &output
	groupedRows := make([]map[string]any, 0, len(groups.Entries))
	state := requestEvaluationState(ctx)
	for _, group := range groups.Entries {
		groupedRows = append(groupedRows, evaluationRow(scopeRow(group), state))
	}
	result, err := assembleGraph(ctx, &outputPlan, groupedRows)
	if err != nil {
		return nil, err
	}
	result.Execution.MatchRows = groups.Execution.MatchRows
	result.Execution.CandidateRows = groups.Execution.CandidateRows
	return result, nil
}

func containsVolatileExpression(value *GraphExpression) bool {
	if value == nil {
		return false
	}
	if value.Operation == "FUNCTION:rand" || strings.HasSuffix(value.Operation, ".realtime") {
		return true
	}
	for _, argument := range value.Arguments {
		if containsVolatileExpression(argument) {
			return true
		}
	}
	for _, field := range value.Fields {
		if containsVolatileExpression(field) {
			return true
		}
	}
	if collection := value.Collection; collection != nil {
		return containsVolatileExpression(collection.Source) || containsVolatileExpression(collection.Predicate) || containsVolatileExpression(collection.Projection)
	}
	return false
}
