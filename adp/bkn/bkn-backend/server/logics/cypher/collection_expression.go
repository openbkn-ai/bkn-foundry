// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type GraphCollection struct {
	Kind, Variable                string
	Source, Predicate, Projection *GraphExpression
}

const evaluationStateKey = "\x00cypher-evaluation"

// Evaluation state belongs to a request, never to the reusable compiled plan.
type expressionEvaluationState struct {
	now                time.Time
	ctx                context.Context
	iterations         int
	service            *cypherQueryService
	query              interfaces.CypherQuery
	plans              map[*Plan]*compiledQuery
	models             map[*Plan]*graphFactModel
	budget             *pipelineBudget
	objects            map[string]string
	subqueryCandidates int
}

type evaluationContextKey struct{}

func requestEvaluationState(ctx context.Context) *expressionEvaluationState {
	if state, ok := ctx.Value(evaluationContextKey{}).(*expressionEvaluationState); ok {
		return state
	}
	return &expressionEvaluationState{ctx: ctx, now: time.Now().UTC(), plans: map[*Plan]*compiledQuery{}, models: map[*Plan]*graphFactModel{}, budget: &pipelineBudget{}, objects: map[string]string{}}
}

func registerEvaluationPlans(state *expressionEvaluationState, compiled *compiledQuery) {
	state.plans[compiled.plan] = compiled
	for _, children := range [][]*compiledQuery{compiled.stages, compiled.branches, compiled.observations, compiled.subqueries} {
		for _, child := range children {
			registerEvaluationPlans(state, child)
		}
	}
}

func evaluationRow(row map[string]any, state *expressionEvaluationState) map[string]any {
	copy := make(map[string]any, len(row)+1)
	for key, value := range row {
		copy[key] = value
	}
	copy[evaluationStateKey] = state
	return copy
}

func (p *planner) graphCollection(value CollectionExpression, relations map[string]EdgeRef, options CompileOptions) (*GraphExpression, error) {
	source, err := p.graphExpression(value.Source, relations, options)
	if err != nil {
		return nil, err
	}
	local := *p
	local.bindings = copyScope(p.bindings)
	local.bindings[value.Variable] = &GraphExpression{Binding: value.Variable, Type: unwindType(source), Shape: unwindShape(source)}
	collection := &GraphCollection{Kind: value.Kind, Variable: value.Variable, Source: source}
	if value.Predicate != nil {
		collection.Predicate, err = local.graphExpression(value.Predicate, relations, options)
		if err != nil {
			return nil, err
		}
		if containsGraphAggregate(collection.Predicate) {
			return nil, planErrorf(value.Pos, "aggregates are not allowed in collection predicates")
		}
	}
	collection.Projection, err = local.graphExpression(value.Projection, relations, options)
	if err != nil {
		return nil, err
	}
	if containsGraphAggregate(collection.Projection) {
		return nil, planErrorf(value.Pos, "aggregates are not allowed in collection projections")
	}
	typ := &interfaces.CypherValueType{Kind: "boolean"}
	if value.Kind == "list" {
		typ = &interfaces.CypherValueType{Kind: "list", Element: collection.Projection.Type}
	}
	return &GraphExpression{Collection: collection, Type: typ}, nil
}

func evaluateCollection(plan *Plan, collection *GraphCollection, row map[string]any) (any, error) {
	source, err := evaluateGraph(plan, collection.Source, row)
	if err != nil || source == nil {
		return nil, err
	}
	items, ok := source.([]any)
	if !ok {
		return nil, expressionTypeError()
	}
	state, _ := row[evaluationStateKey].(*expressionEvaluationState)
	if state == nil {
		state = &expressionEvaluationState{ctx: context.Background()}
	}
	local := evaluationRow(row, state)
	result := []any{}
	truths, unknowns, bytes := 0, 0, 0
	for _, item := range items {
		state.iterations++
		if state.iterations > graphMatchBudget*8 {
			return nil, errGraphBudget
		}
		if err := state.ctx.Err(); err != nil {
			return nil, err
		}
		local["unwind:"+collection.Variable] = item
		predicate := item
		if collection.Predicate != nil {
			predicate, err = evaluateGraph(plan, collection.Predicate, local)
			if err != nil {
				return nil, err
			}
		} else if collection.Kind == "list" {
			predicate = true
		}
		if predicate != nil {
			if _, ok := predicate.(bool); !ok {
				return nil, expressionTypeError()
			}
		}
		if collection.Kind == "list" {
			if predicate != true {
				continue
			}
			value, err := evaluateGraph(plan, collection.Projection, local)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			bytes += len(encoded) + 16
			if len(result) >= graphMatchBudget || bytes > graphPayloadBudget {
				return nil, errGraphBudget
			}
			result = append(result, value)
			continue
		}
		if predicate == true {
			truths++
		}
		if predicate == nil {
			unknowns++
		}
		if collection.Kind == "all" && predicate == false {
			return false, nil
		}
		if collection.Kind == "any" && predicate == true {
			return true, nil
		}
		if collection.Kind == "none" && predicate == true {
			return false, nil
		}
		if collection.Kind == "single" && truths > 1 {
			return false, nil
		}
	}
	if collection.Kind == "list" {
		return result, nil
	}
	if unknowns > 0 {
		return nil, nil
	}
	switch collection.Kind {
	case "all", "none":
		return true, nil
	case "any":
		return false, nil
	case "single":
		return truths == 1, nil
	default:
		return nil, expressionTypeError()
	}
}

// Shapes carry every possible graph origin. Choosing one branch at runtime
// must never let a different branch's property dependency escape authorization.
func propagateExpressionShape(value *GraphExpression) {
	if len(value.Arguments) > 0 {
		a := value.Arguments[0].Type.Kind
		if (value.Operation == "NEGATE" || value.Operation == "POSITIVE") && a == "duration" {
			value.Type = &interfaces.CypherValueType{Kind: "duration"}
		}
		if len(value.Arguments) > 1 {
			b := value.Arguments[1].Type.Kind
			if ((value.Operation == "+" || value.Operation == "-") && b == "duration" && isTemporalFunction(a)) || ((value.Operation == "*" || value.Operation == "/") && a == "duration") {
				value.Type = &interfaces.CypherValueType{Kind: a}
			}
			if (value.Operation == "+" && a == "duration" && isTemporalFunction(b)) || (value.Operation == "*" && b == "duration") {
				value.Type = &interfaces.CypherValueType{Kind: b}
			}
		}
	}

	var choices []*GraphExpression
	switch value.Operation {
	case "FUNCTION:nodes", "FUNCTION:relationships":
		value.Shape = pathFunctionShape(value)
	case "CASE":
		for i := 1; i < len(value.Arguments); i += 2 {
			choices = append(choices, value.Arguments[i])
		}
		choices = append(choices, value.Arguments[len(value.Arguments)-1])
	case "FUNCTION:coalesce":
		choices = value.Arguments
	case "FUNCTION:head", "FUNCTION:last":
		value.Shape = unwindShape(value.Arguments[0])
	case "FUNCTION:startnode", "FUNCTION:endnode":
		var visit func(*GraphExpression)
		visit = func(item *GraphExpression) {
			item = shapeOf(item)
			if item == nil {
				return
			}
			if item.Operation == "SHAPES" {
				for _, child := range item.Arguments {
					visit(child)
				}
				return
			}
			if item.Relation != nil {
				object := item.Relation.Source
				if value.Operation == "FUNCTION:endnode" {
					object = item.Relation.Target
				}
				choices = append(choices, &GraphExpression{Object: object, Type: &interfaces.CypherValueType{Kind: "object", ObjectTypeID: object.TypeID}})
			}
		}
		visit(value.Arguments[0])
	}
	if len(choices) > 0 {
		value.Shape = &GraphExpression{Operation: "SHAPES", Arguments: choices}
	}
	if value.Shape != nil {
		var typ *interfaces.CypherValueType
		mixed := false
		var visit func(*GraphExpression)
		visit = func(item *GraphExpression) {
			item = shapeOf(item)
			if item == nil {
				return
			}
			if item.Operation == "SHAPES" {
				for _, child := range item.Arguments {
					visit(child)
				}
				return
			}
			if item.Type == nil || item.Type.Kind == "null" {
				return
			}
			if typ == nil {
				typ = item.Type
			} else if !reflect.DeepEqual(typ, item.Type) {
				mixed = true
			}
		}
		visit(value.Shape)
		if typ != nil && !mixed {
			value.Type = typ
		}
	}
}
