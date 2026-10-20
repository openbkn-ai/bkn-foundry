// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"

	dtype "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/data_type"
)

// PlanCase is CASE WHEN ... THEN ... [ELSE ...] END. Else is nil when the query
// wrote none, which gives null, as SQL does too.
type PlanCase struct {
	Whens []PlanCaseWhen
	Else  PlanExpression
}

func (PlanCase) planExpression() {}

// PlanCaseWhen is one branch.
type PlanCaseWhen struct {
	Condition PlanPredicate
	Result    PlanExpression
}

// PlanCoalesce is COALESCE(...).
type PlanCoalesce struct {
	Arguments []PlanExpression
}

func (PlanCoalesce) planExpression() {}

// planCase plans a CASE. Its branches may give strings as well as numbers, but
// all of them one or the other: Cypher would return a column of mixed types,
// and SQL would quietly convert one kind into the other.
func (p *planner) planCase(node CaseValue) (PlanExpression, numberKind, error) {
	p.plan.SQLOnly = true
	planned := PlanCase{}
	var kinds []numberKind
	for _, when := range node.Whens {
		condition, err := p.planPredicate(when.Condition)
		if err != nil {
			return nil, kindUnknown, err
		}
		result, kind, err := p.planResult(when.Result)
		if err != nil {
			return nil, kindUnknown, err
		}
		planned.Whens = append(planned.Whens, PlanCaseWhen{Condition: condition, Result: result})
		kinds = append(kinds, kind)
	}
	if node.Else != nil {
		result, kind, err := p.planResult(node.Else)
		if err != nil {
			return nil, kindUnknown, err
		}
		planned.Else = result
		kinds = append(kinds, kind)
	}
	kind, err := resultKind(kinds, node.Pos, "the branches of CASE")
	if err != nil {
		return nil, kindUnknown, err
	}
	return planned, kind, nil
}

func (p *planner) planCoalesce(node Coalesce) (PlanExpression, numberKind, error) {
	p.plan.SQLOnly = true
	planned := PlanCoalesce{}
	var kinds []numberKind
	for _, argument := range node.Arguments {
		value, kind, err := p.planResult(argument)
		if err != nil {
			return nil, kindUnknown, err
		}
		planned.Arguments = append(planned.Arguments, value)
		kinds = append(kinds, kind)
	}
	kind, err := resultKind(kinds, node.Pos, "the arguments of coalesce")
	if err != nil {
		return nil, kindUnknown, err
	}
	return planned, kind, nil
}

// planResult plans what a branch or an argument gives. Unlike a term of
// arithmetic it may be a string: a property of any type, or a literal or
// parameter of any kind but boolean.
func (p *planner) planResult(value Expression) (PlanExpression, numberKind, error) {
	switch node := value.(type) {
	case PropertyRef:
		table, column, err := p.resolveProperty(node)
		if err != nil {
			return nil, kindUnknown, err
		}
		kind, declared := p.propertyKind(table, node.Property)
		if strings.ToLower(declared) == dtype.DATATYPE_BOOLEAN {
			// MySQL would return it as 1 or 0, as it would a boolean literal.
			return nil, kindUnknown, planErrorf(node.Pos,
				"%s is boolean, which CASE and coalesce do not return; return a condition instead", node)
		}
		return PlanValueColumn{Table: table, Column: column, Property: node.Property}, kind, nil
	case Literal:
		kind := literalKind(node)
		if node.Kind == LiteralNull {
			kind = kindNull
		}
		return PlanValueLiteral{Value: node, InputPointer: "$.query"}, kind, nil
	case ParameterRef:
		literal, err := p.resolveValue(Operand{Parameter: &node}, node.Pos)
		if err != nil {
			return nil, kindUnknown, err
		}
		if literal.Kind == LiteralBoolean {
			return nil, kindUnknown, planErrorf(node.Pos,
				"parameter %q is a boolean, which CASE and coalesce do not return; return a condition instead", node.Name)
		}
		return PlanValueLiteral{Value: literal, InputPointer: parameterInputPointer(node.Name)}, literalKind(literal), nil
	default:
		return p.planExpression(value)
	}
}

// resultKind is what a CASE or coalesce gives, from what each of its values
// gives. A null literal says nothing either way (literalKind reads it as not a
// number, so it is told apart by kindNull), and neither does a property the
// model leaves untyped.
func resultKind(kinds []numberKind, pos Position, what string) (numberKind, error) {
	numbers, others := 0, 0
	kind := kindInteger
	for _, valueKind := range kinds {
		switch valueKind {
		case kindNull:
		case kindNotNumber:
			others++
		case kindFloat:
			numbers++
			kind = kindFloat
		case kindInteger:
			numbers++
		default:
			numbers++
			if kind != kindFloat {
				kind = kindUnknown
			}
		}
	}
	switch {
	case numbers > 0 && others > 0:
		return kindUnknown, planErrorf(pos, "%s mix numbers with other values; give them all one kind", what)
	case others > 0:
		return kindNotNumber, nil
	case numbers == 0:
		return kindUnknown, nil
	default:
		return kind, nil
	}
}

// caseConditions and caseValues list what a PlanCase or PlanCoalesce reads, for
// the walkers that look for columns and aggregates inside computed values.
func caseConditions(value PlanExpression) []PlanPredicate {
	planned, ok := value.(PlanCase)
	if !ok {
		return nil
	}
	conditions := make([]PlanPredicate, 0, len(planned.Whens))
	for _, when := range planned.Whens {
		conditions = append(conditions, when.Condition)
	}
	return conditions
}

func caseValues(value PlanExpression) []PlanExpression {
	switch node := value.(type) {
	case PlanCase:
		values := make([]PlanExpression, 0, len(node.Whens)+1)
		for _, when := range node.Whens {
			values = append(values, when.Result)
		}
		if node.Else != nil {
			values = append(values, node.Else)
		}
		return values
	case PlanCoalesce:
		return node.Arguments
	default:
		return nil
	}
}

// planPredicateAggregates reports whether a condition reads an aggregate, as
// one inside a CASE after a grouping WITH may: CASE WHEN n > 10 ... where n is
// count(*).
func planPredicateAggregates(predicate PlanPredicate) bool {
	switch node := predicate.(type) {
	case PlanExpressionComparison:
		return planContainsAggregate(node.Left) || planContainsAggregate(node.Right)
	case PlanNegation:
		return planPredicateAggregates(node.Operand)
	case PlanLogical:
		for _, operand := range node.Operands {
			if planPredicateAggregates(operand) {
				return true
			}
		}
	}
	return false
}
