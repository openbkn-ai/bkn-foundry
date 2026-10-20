// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// CASE and coalesce are values like any other computed one: RETURN, WITH,
// ORDER BY, the sides of a comparison and the argument of an aggregate take
// them, as in sum(CASE WHEN g.penalty = '1' THEN 1 ELSE 0 END). They are
// written to SQL as CASE and COALESCE, which every dialect has.
//
// What a branch reads depends on where the expression stands, as it does for
// arithmetic: the pattern's variables, or after a WITH the names it passed on.
// caseReaders carries both readers so one analysis serves either place.
type caseReaders struct {
	value     func(parsing.IOC_ExpressionContext) (Expression, error)
	condition func(parsing.IOC_ExpressionContext) (Predicate, error)
}

// patternCaseReaders read a CASE over the pattern's variables. A bare name in a
// condition is refused there, as it is in RETURN: only WHERE resolves the names
// a WITH introduced.
func patternCaseReaders() caseReaders {
	return caseReaders{
		value: analyzeCaseValue,
		condition: func(ctx parsing.IOC_ExpressionContext) (Predicate, error) {
			predicate, err := analyzePredicate(ctx)
			if err != nil {
				return nil, err
			}
			if err := refuseNames(predicate); err != nil {
				return nil, err
			}
			return predicate, nil
		},
	}
}

// analyzeCaseValue reads a branch's result or an argument of coalesce: a
// computed value, or a single literal of any kind, since a branch may give a
// string as well as a number.
func analyzeCaseValue(ctx parsing.IOC_ExpressionContext) (Expression, error) {
	return analyzeExpression(ctx)
}

// caseOrCoalesce reports whether an atom is a CASE or a coalesce call, which
// read as computed values rather than as a property or a function to refuse.
func caseOrCoalesce(atom parsing.IOC_AtomContext) bool {
	if atom.OC_CaseExpression() != nil {
		return true
	}
	invocation := atom.OC_FunctionInvocation()
	return invocation != nil && strings.EqualFold(identifier(invocation.OC_FunctionName().GetText()), "coalesce")
}

// analyzeCaseOrCoalesce reads an atom caseOrCoalesce accepted.
func analyzeCaseOrCoalesce(atom parsing.IOC_AtomContext, readers caseReaders) (Expression, error) {
	if ctx := atom.OC_CaseExpression(); ctx != nil {
		return analyzeCase(ctx, readers)
	}
	return analyzeCoalesce(atom.OC_FunctionInvocation(), readers)
}

func analyzeCase(ctx parsing.IOC_CaseExpressionContext, readers caseReaders) (Expression, error) {
	alternatives := ctx.AllOC_CaseAlternative()
	firstWhen := alternatives[0].GetStart().GetTokenIndex()
	var operand, elseValue parsing.IOC_ExpressionContext
	for _, expression := range ctx.AllOC_Expression() {
		if expression.GetStart().GetTokenIndex() < firstWhen {
			operand = expression
		} else {
			elseValue = expression
		}
	}

	value := CaseValue{Pos: positionOf(ctx)}
	var subject Expression
	if operand != nil {
		read, err := readers.value(operand)
		if err != nil {
			return nil, err
		}
		subject = read
	}
	for _, alternative := range alternatives {
		when, then := alternative.OC_Expression(0), alternative.OC_Expression(1)
		var condition Predicate
		if subject == nil {
			read, err := readers.condition(when)
			if err != nil {
				return nil, err
			}
			condition = read
		} else {
			compared, err := readers.value(when)
			if err != nil {
				return nil, err
			}
			if condition, err = caseEquality(subject, compared, when); err != nil {
				return nil, err
			}
		}
		result, err := caseResult(then, readers)
		if err != nil {
			return nil, err
		}
		value.Whens = append(value.Whens, CaseWhen{Condition: condition, Result: result})
	}
	if elseValue != nil {
		result, err := caseResult(elseValue, readers)
		if err != nil {
			return nil, err
		}
		value.Else = result
	}
	return value, nil
}

// caseEquality writes the condition of a simple CASE's branch, x = v, in the
// shape a WHERE would have given it, so it plans and generates the same way.
func caseEquality(subject, compared Expression, at parsing.IOC_ExpressionContext) (Predicate, error) {
	if literal, ok := compared.(Literal); ok && literal.Kind == LiteralNull {
		return nil, unsupportedf(at, "WHEN null in a simple CASE",
			"nothing equals null; write CASE WHEN x IS NULL THEN ... instead")
	}
	pos := positionOf(at)
	if property, ok := subject.(PropertyRef); ok {
		switch value := compared.(type) {
		case Literal:
			return Comparison{Left: property, Operator: "=", Right: Operand{Literal: &value}, Pos: pos}, nil
		case ParameterRef:
			return Comparison{Left: property, Operator: "=", Right: Operand{Parameter: &value}, Pos: pos}, nil
		case PropertyRef:
			return PropertyComparison{Left: property, Operator: "=", Right: value, Pos: pos}, nil
		}
	}
	return ExpressionComparison{Left: subject, Operator: "=", Right: compared, Pos: pos}, nil
}

// caseResult reads what a branch gives. A boolean is refused: MySQL would
// return it as 1 or 0, and the condition itself already returns true or false.
func caseResult(ctx parsing.IOC_ExpressionContext, readers caseReaders) (Expression, error) {
	result, err := readers.value(ctx)
	if err != nil {
		return nil, err
	}
	if literal, ok := result.(Literal); ok && literal.Kind == LiteralBoolean {
		return nil, unsupportedf(ctx, "a boolean as the result of CASE",
			"return the condition itself, as in RETURN n.x > 3 AS flag")
	}
	return result, nil
}

func analyzeCoalesce(invocation parsing.IOC_FunctionInvocationContext, readers caseReaders) (Expression, error) {
	arguments := invocation.AllOC_Expression()
	if invocation.DISTINCT() != nil || len(arguments) < 2 {
		return nil, unsupportedf(invocation, "this coalesce", "coalesce takes two or more values, as in coalesce(n.a, n.b, 0)")
	}
	value := Coalesce{Pos: positionOf(invocation)}
	for _, argument := range arguments {
		read, err := caseResult(argument, readers)
		if err != nil {
			return nil, err
		}
		value.Arguments = append(value.Arguments, read)
	}
	return value, nil
}

// caseParts lists what a CASE or coalesce is computed from: its conditions and
// the values its branches or arguments give.
func caseParts(value Expression) ([]Predicate, []Expression, bool) {
	switch node := value.(type) {
	case CaseValue:
		var conditions []Predicate
		var values []Expression
		for _, when := range node.Whens {
			conditions = append(conditions, when.Condition)
			values = append(values, when.Result)
		}
		if node.Else != nil {
			values = append(values, node.Else)
		}
		return conditions, values, true
	case Coalesce:
		return nil, node.Arguments, true
	default:
		return nil, nil, false
	}
}
