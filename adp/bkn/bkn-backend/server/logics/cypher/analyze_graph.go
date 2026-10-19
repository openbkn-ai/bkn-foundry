// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// analyzeGraphProjection reads a RETURN item that is a graph value: a bare
// variable, a constant, a map literal, or collect(...). ok is false for
// anything else, which keeps the scalar path it always had.
func analyzeGraphProjection(ctx parsing.IOC_ExpressionContext) (*Projection, bool, error) {
	term, single := singleExpressionAtom(ctx)
	if !single || len(term.propertyLookups) > 0 {
		return nil, false, nil
	}
	atom := term.atom
	var value GraphExpression
	switch {
	case atom.OC_Variable() != nil:
		name := identifier(atom.OC_Variable().GetText())
		if name == "" {
			return nil, false, nil
		}
		value = GraphVariable{Name: name, Pos: positionOf(atom)}
	case atom.OC_FunctionInvocation() != nil:
		if !isCollect(atom.OC_FunctionInvocation()) {
			return nil, false, nil
		}
		collect, err := analyzeCollect(atom.OC_FunctionInvocation())
		if err != nil {
			return nil, true, err
		}
		value = collect
	case atom.OC_Literal() != nil && atom.OC_Literal().OC_MapLiteral() != nil:
		mapValue, err := analyzeGraphMap(atom.OC_Literal().OC_MapLiteral())
		if err != nil {
			return nil, true, err
		}
		value = mapValue
	case atom.OC_Literal() != nil, atom.OC_Parameter() != nil:
		constant, err := analyzeGraphConstant(ctx)
		if err != nil {
			return nil, true, err
		}
		value = constant
	default:
		return nil, false, nil
	}
	return &Projection{Value: value, Alias: sourceText(ctx), Pos: positionOf(ctx)}, true, nil
}

func isCollect(invocation parsing.IOC_FunctionInvocationContext) bool {
	return strings.EqualFold(identifier(invocation.OC_FunctionName().GetText()), "collect")
}

// analyzeCollect reads collect(value). DISTINCT is refused for now: collect
// keeps every match as evidence, and deduplicating evidence needs its own
// identity rule.
func analyzeCollect(invocation parsing.IOC_FunctionInvocationContext) (GraphCollect, error) {
	if invocation.DISTINCT() != nil {
		return GraphCollect{}, unsupportedf(invocation, "collect(DISTINCT ...)",
			"collect keeps every match; remove DISTINCT")
	}
	arguments := invocation.AllOC_Expression()
	if len(arguments) != 1 {
		return GraphCollect{}, unsupportedf(invocation, "collect over several arguments",
			"collect takes one value; collect a map to keep several")
	}
	argument, err := analyzeGraphValue(arguments[0])
	if err != nil {
		return GraphCollect{}, err
	}
	if _, constant := argument.(GraphConstant); constant {
		return GraphCollect{}, unsupportedf(arguments[0], "collect over a constant",
			"collect a node, a relationship, a property or a map of them")
	}
	return GraphCollect{Argument: argument, Pos: positionOf(invocation)}, nil
}

// analyzeGraphMap reads a map literal whose values are graph values.
func analyzeGraphMap(ctx parsing.IOC_MapLiteralContext) (GraphMap, error) {
	keys := ctx.AllOC_PropertyKeyName()
	values := ctx.AllOC_Expression()
	result := GraphMap{Pos: positionOf(ctx)}
	seen := make(map[string]bool, len(keys))
	for i, key := range keys {
		name := identifier(key.GetText())
		if name == "" {
			return GraphMap{}, unsupportedf(key, "an empty map key", "name every key of the map")
		}
		if seen[name] {
			return GraphMap{}, unsupportedf(key, "a map with a repeated key",
				"key %q appears twice; give each value its own key", name)
		}
		seen[name] = true
		value, err := analyzeGraphValue(values[i])
		if err != nil {
			return GraphMap{}, err
		}
		result.Keys = append(result.Keys, name)
		result.Values = append(result.Values, value)
	}
	return result, nil
}

// analyzeGraphValue reads one value inside a map or collect: a variable, a
// property, a constant or a nested map. Aggregates and computed values are
// refused there, so a map is always one value per matched row.
func analyzeGraphValue(ctx parsing.IOC_ExpressionContext) (GraphExpression, error) {
	term, single := singleExpressionAtom(ctx)
	if !single || computed(ctx) {
		return nil, unsupportedf(ctx, "a computed value inside a map or collect",
			"use a variable, a property, a constant or a map")
	}
	if len(term.propertyLookups) > 0 {
		property, err := analyzePropertyRef(ctx)
		if err != nil {
			return nil, err
		}
		return GraphProperty{Property: *property}, nil
	}
	atom := term.atom
	switch {
	case atom.OC_Variable() != nil:
		name := identifier(atom.OC_Variable().GetText())
		if name == "" {
			return nil, unsupported(atom, describeAtom(atom))
		}
		return GraphVariable{Name: name, Pos: positionOf(atom)}, nil
	case atom.OC_Literal() != nil && atom.OC_Literal().OC_MapLiteral() != nil:
		return analyzeGraphMap(atom.OC_Literal().OC_MapLiteral())
	case atom.OC_Literal() != nil, atom.OC_Parameter() != nil:
		return analyzeGraphConstant(ctx)
	case atom.COUNT() != nil, atom.OC_FunctionInvocation() != nil:
		return nil, unsupportedf(ctx, "an aggregate inside a map or collect",
			"collect the map instead, as in collect({a: x, b: y})")
	default:
		return nil, unsupported(atom, describeAtom(atom))
	}
}

// analyzeGraphConstant reads a string, number or boolean, signed numbers
// included, or a parameter.
func analyzeGraphConstant(ctx parsing.IOC_ExpressionContext) (GraphConstant, error) {
	value, err := analyzeOperand(ctx)
	if err != nil {
		return GraphConstant{}, err
	}
	if value.parameter != nil {
		return GraphConstant{Parameter: value.parameter, Pos: value.parameter.Pos}, nil
	}
	if value.literal == nil {
		return GraphConstant{}, unsupportedf(ctx, "an expression here", "expected a constant")
	}
	if value.literal.Kind == LiteralNull {
		return GraphConstant{}, unsupportedf(ctx, "returning null", "a constant column must have a value")
	}
	return GraphConstant{Literal: value.literal, Pos: value.literal.Pos}, nil
}
