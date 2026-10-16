// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

type PatternExpression struct {
	Kind                  string
	Pattern               Pattern
	Predicate, Projection ValueExpression
	Query                 *Query
	Pos                   Position
}

func (PatternExpression) valueExpression() {}

type GraphSubquery struct {
	Kind       string
	Plan       *Plan
	Outer      map[string]*GraphExpression
	Projection *GraphExpression
}

type correlatedInputKey struct{}

func readPatternExpression(node parsing.IOC_AtomContext, depth int) (ValueExpression, error) {
	value := PatternExpression{Pos: positionOf(node)}
	builder := newPatternBuilder()
	builder.graph, builder.allowUnlabeled = true, true
	var where parsing.IOC_WhereContext
	if list := node.OC_PatternComprehension(); list != nil {
		value.Kind = "list"
		name := ""
		if variable := list.OC_Variable(); variable != nil {
			var err error
			name, err = namedIdentifier(variable, "path variable")
			if err != nil {
				return nil, err
			}
		}
		pattern := list.OC_RelationshipsPattern()
		if err := builder.addPatternPath(pattern.OC_NodePattern(), pattern.AllOC_PatternElementChain(), name, positionOf(pattern)); err != nil {
			return nil, err
		}
		var err error
		value.Projection, err = readValueExpression(list.OC_Expression(), depth+1)
		if err != nil {
			return nil, err
		}
		where = list.OC_Where()
	} else if predicate := node.OC_PatternPredicate(); predicate != nil {
		value.Kind = "pattern"
		pattern := predicate.OC_RelationshipsPattern()
		if err := builder.addPatternPath(pattern.OC_NodePattern(), pattern.AllOC_PatternElementChain(), "", positionOf(pattern)); err != nil {
			return nil, err
		}
	} else {
		value.Kind = "exists"
		exists := node.OC_ExistentialSubquery()
		if regular := exists.OC_RegularQuery(); regular != nil {
			var err error
			value.Query, err = analyzeRegularQuery(regular, true)
			return value, err
		}
		if err := builder.addPattern(exists.OC_Pattern()); err != nil {
			return nil, err
		}
		where = exists.OC_Where()
	}
	value.Pattern = builder.pattern
	if where != nil {
		var err error
		value.Predicate, err = readValueExpression(where.OC_Expression(), depth+1)
		if err != nil {
			return nil, err
		}
	}
	if inline := inlinePatternExpression(combine("AND", builder.inline, value.Pos)); inline != nil {
		if value.Predicate == nil {
			value.Predicate = inline
		} else {
			value.Predicate = OperationExpression{Operator: "AND", Arguments: []ValueExpression{inline, value.Predicate}, Pos: value.Pos}
		}
	}
	return value, nil
}

func (p *planner) graphSubquery(value PatternExpression, relations map[string]EdgeRef, options CompileOptions) (*GraphExpression, error) {
	outer := copyScope(p.bindings)
	for _, name := range sortedGraphKeys(p.tableOf) {
		if name == "" {
			continue
		}
		expression, err := p.graphExpression(VariableExpression{Name: name, Pos: value.Pos}, relations, options)
		if err != nil {
			return nil, err
		}
		outer[name] = expression
	}
	for _, name := range sortedGraphKeys(relations) {
		expression, err := p.graphExpression(VariableExpression{Name: name, Pos: value.Pos}, relations, options)
		if err != nil {
			return nil, err
		}
		outer[name] = expression
	}
	scope := map[string]*GraphExpression{}
	for name, expression := range outer {
		scope[name] = scopeBinding(name, expression)
	}
	if value.Kind == "pattern" {
		for _, node := range value.Pattern.Nodes {
			if node.Variable != "" && !node.Anonymous {
				if _, ok := scope[node.Variable]; !ok {
					return nil, planErrorf(node.Pos, "pattern predicate cannot introduce variable %q", node.Variable)
				}
			}
		}
		for _, edge := range value.Pattern.Edges {
			if edge.Variable != "" {
				if _, ok := scope[edge.Variable]; !ok {
					return nil, planErrorf(edge.Pos, "pattern predicate cannot introduce variable %q", edge.Variable)
				}
			}
		}
	}
	subquery := &GraphSubquery{Kind: value.Kind, Outer: outer}
	typ := &interfaces.CypherValueType{Kind: "boolean"}
	var err error
	if value.Query != nil {
		correlatedOptions := options
		correlatedOptions.Correlated = true
		subquery.Plan, err = compileScopedSubquery(value.Query, p.schema, correlatedOptions, scope)
	} else {
		query := &Query{ValueEvaluation: true, Pattern: value.Pattern, ResidualWhere: value.Predicate}
		var local map[string]*GraphExpression
		subquery.Plan, local, _, err = compileGraphPattern(query, p.schema, scope, options)
		if err == nil && value.Kind == "list" {
			localPlanner := newScopePlanner(p.schema, local, options)
			localPlanner.plan = subquery.Plan
			subquery.Projection, err = localPlanner.graphExpression(value.Projection, nil, options)
			if err == nil && containsGraphAggregate(subquery.Projection) {
				return nil, planErrorf(value.Pos, "aggregates are not allowed in pattern comprehension projections")
			}
			if err == nil {
				typ = &interfaces.CypherValueType{Kind: "list", Element: subquery.Projection.Type}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	p.plan.Subqueries = append(p.plan.Subqueries, subquery.Plan)
	return &GraphExpression{Subquery: subquery, Type: typ}, nil
}

func evaluateSubquery(parent *Plan, subquery *GraphSubquery, row map[string]any) (any, error) {
	state, _ := row[evaluationStateKey].(*expressionEvaluationState)
	if state == nil || state.service == nil {
		return nil, expressionTypeError()
	}
	input := map[string]any{evaluationStateKey: state}
	for _, name := range sortedGraphKeys(subquery.Outer) {
		value, err := evaluateGraph(parent, subquery.Outer[name], row)
		if err != nil {
			return nil, err
		}
		input["unwind:"+name] = value
	}
	compiled := state.plans[subquery.Plan]
	if compiled == nil {
		return nil, expressionTypeError()
	}
	if subquery.Plan.Pattern == nil {
		startCandidates := state.subqueryCandidates
		result, err := state.service.runCompiled(context.WithValue(state.ctx, correlatedInputKey{}, input), state.query, compiled)
		if err != nil {
			return nil, err
		}
		if result.Execution != nil {
			state.subqueryCandidates = startCandidates + result.Execution.CandidateRows
		}
		return len(result.Entries) > 0, nil
	}
	rows, candidates, err := state.service.runGraphPattern(state.ctx, state.query, compiled, PlanStage{Kind: "match", Plan: subquery.Plan}, []map[string]any{input}, state.budget, state.objects)
	if err != nil {
		return nil, err
	}
	state.subqueryCandidates += candidates
	if err := state.budget.account(rows, max(candidates, 1)); err != nil {
		return nil, err
	}
	if subquery.Kind != "list" {
		return len(rows) > 0, nil
	}
	result := []any{}
	for _, local := range rows {
		value, err := evaluateGraph(subquery.Plan, subquery.Projection, local)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func queryHasPatternReads(query *Query) bool {
	found := false
	var expression func(ValueExpression)
	expression = func(value ValueExpression) {
		switch item := value.(type) {
		case PatternExpression:
			found = true
		case OperationExpression:
			for _, arg := range item.Arguments {
				expression(arg)
			}
		case CollectionExpression:
			expression(item.Source)
			expression(item.Predicate)
			expression(item.Projection)
		case AggregateExpression:
			expression(item.Value)
			expression(item.Percentile)
		case MapExpression:
			for _, field := range item.Fields {
				expression(field.Value)
			}
		}
	}
	for _, projection := range query.Return {
		expression(projection.Value)
	}
	for _, unwind := range query.Unwinds {
		expression(unwind.Expression)
	}
	for _, order := range query.OrderBy {
		expression(order.Value)
	}
	expression(query.FullWhere)
	expression(query.ResidualWhere)
	expression(query.PostWhere)
	for _, stage := range query.Stages {
		found = found || queryHasPatternReads(stage.Query)
	}
	for _, branch := range query.Union {
		found = found || queryHasPatternReads(branch)
	}
	return found
}

// Every UNION branch inherits the same outer scope. Branch-local names never
// become visible to a sibling or escape the existential expression.
func compileScopedSubquery(query *Query, schema *Schema, options CompileOptions, scope map[string]*GraphExpression) (*Plan, error) {
	if len(query.Union) > 0 {
		return compileUnionBranches(query, schema, options, func(branch *Query, schema *Schema, options CompileOptions) (*Plan, error) {
			return compileScopedSubquery(branch, schema, options, copyScope(scope))
		})
	}
	if len(query.Stages) == 0 {
		if len(query.Pattern.Nodes) > 0 {
			query = patternQueryStages(query)
		} else {
			query = &Query{ValueEvaluation: true, Stages: []QueryStage{{Kind: "return", Query: query}}}
		}
	}
	return compilePipelineScope(query, schema, options, copyScope(scope))
}
