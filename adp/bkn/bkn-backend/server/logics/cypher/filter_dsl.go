// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"
	"strings"
)

// The bind-join executor reads every table through vega-backend's resource
// data API rather than through one SQL statement. A condition on one table can
// still be evaluated by the data source, written as the API's filter_condition
// tree; this file decides which conditions can be, and writes them.
//
// A condition is pushed only when the data source gives exactly the answer the
// SQL path would. vega-backend writes each filter operation as the matching
// SQL predicate (== as =, != as <>, in as IN, like as LIKE '%v%', prefix as
// LIKE 'v%', null as IS NULL), so the comparison, the collation and the NULL
// handling are the database's in both paths. Negation is pushed by inverting
// the operator rather than by wrapping it: the API has no NOT, and under SQL's
// three-valued logic NOT (a = v) and a <> v agree on every row, NULL included,
// as do De Morgan's laws.

// pushedFilter is a condition written for the data source, or the constant it
// reduces to when it does not depend on the row. The constant is kept apart
// from a filter tree so that an always false condition can short-circuit a
// read instead of being sent.
type pushedFilter struct {
	condition map[string]any
	// constant is set when the condition is the same for every row: true
	// drops it, false means the table contributes no row.
	constant *bool
}

var (
	alwaysTrue  = true
	alwaysFalse = false
)

// dslOperators maps a planned comparison to the filter operation that the data
// source evaluates the same way, and dslNegations to the one that evaluates its
// negation.
var dslOperators = map[string]string{
	"=": "==", "<>": "!=", "<": "<", "<=": "<=", ">": ">", ">=": ">=",
}

var dslNegations = map[string]string{
	"=": "!=", "<>": "==", "<": ">=", "<=": ">", ">": "<=", ">=": "<",
}

// conjuncts flattens a condition into the terms of its top-level AND, which is
// the granularity at which a WHERE clause can be split between the data source
// and the executor.
func conjuncts(predicate PlanPredicate) []PlanPredicate {
	if predicate == nil {
		return nil
	}
	if logical, ok := predicate.(PlanLogical); ok && logical.Operator == "AND" {
		var terms []PlanPredicate
		for _, operand := range logical.Operands {
			terms = append(terms, conjuncts(operand)...)
		}
		return terms
	}
	return []PlanPredicate{predicate}
}

// predicateTables lists the tables a condition reads, each once.
func predicateTables(predicate PlanPredicate) []int {
	seen := map[int]bool{}
	var tables []int
	add := func(table int) {
		if !seen[table] {
			seen[table] = true
			tables = append(tables, table)
		}
	}
	walkPredicateColumns(predicate, func(table int, _ string) { add(table) })
	return tables
}

// walkPredicateColumns calls visit for every column a condition reads.
func walkPredicateColumns(predicate PlanPredicate, visit func(table int, column string)) {
	switch node := predicate.(type) {
	case PlanCondition:
		visit(node.Table, node.Column)
	case PlanNullCheck:
		visit(node.Table, node.Column)
	case PlanMembership:
		visit(node.Table, node.Column)
	case PlanStringMatch:
		visit(node.Table, node.Column)
	case PlanColumnComparison:
		visit(node.LeftTable, node.LeftColumn)
		visit(node.RightTable, node.RightColumn)
	case PlanExpressionComparison:
		walkExpressionColumns(node.Left, visit)
		walkExpressionColumns(node.Right, visit)
	case PlanNegation:
		walkPredicateColumns(node.Operand, visit)
	case PlanLogical:
		for _, operand := range node.Operands {
			walkPredicateColumns(operand, visit)
		}
	case dateComparison:
		walkPredicateColumns(node.PlanPredicate, visit)
	}
}

// walkExpressionColumns calls visit for every column a computed value reads,
// inside aggregates as well.
func walkExpressionColumns(value PlanExpression, visit func(table int, column string)) {
	switch node := value.(type) {
	case PlanValueColumn:
		visit(node.Table, node.Column)
	case PlanAggregate:
		if node.Argument != nil {
			walkExpressionColumns(node.Argument, visit)
		} else if !node.Star {
			visit(node.Table, node.Column)
		}
	case PlanArithmetic:
		walkExpressionColumns(node.Left, visit)
		walkExpressionColumns(node.Right, visit)
	case PlanNegative:
		walkExpressionColumns(node.Operand, visit)
	}
}

// pushFilter writes a condition over one table as a filter_condition tree. ok
// is false when the data source cannot evaluate it exactly as the SQL path
// would, and the executor has to evaluate it instead.
func pushFilter(predicate PlanPredicate) (pushedFilter, bool) {
	return pushFilterNegated(predicate, false)
}

func pushFilterNegated(predicate PlanPredicate, negated bool) (pushedFilter, bool) {
	switch node := predicate.(type) {
	case PlanCondition:
		operators := dslOperators
		if negated {
			operators = dslNegations
		}
		operation, known := operators[node.Operator]
		if !known {
			return pushedFilter{}, false
		}
		value, ok := literalFilterValue(node.Value)
		if !ok {
			return pushedFilter{}, false
		}
		return pushedFilter{condition: fieldCondition(node.Column, operation, value)}, true

	case PlanNullCheck:
		isNull := !node.Negated
		if negated {
			isNull = !isNull
		}
		operation := "not_null"
		if isNull {
			operation = "null"
		}
		return pushedFilter{condition: map[string]any{"field": node.Column, "operation": operation}}, true

	case PlanMembership:
		excluded := node.Negated != negated
		if len(node.Values) == 0 {
			// An empty list matches nothing, so NOT IN an empty list matches
			// everything -- NULL included, as the SQL path writes it as 1 = 1.
			if excluded {
				return pushedFilter{constant: &alwaysTrue}, true
			}
			return pushedFilter{constant: &alwaysFalse}, true
		}
		values := make([]any, 0, len(node.Values))
		for _, literal := range node.Values {
			value, ok := literalFilterValue(literal)
			if !ok {
				return pushedFilter{}, false
			}
			values = append(values, value)
		}
		operation := "in"
		if excluded {
			operation = "not_in"
		}
		return pushedFilter{condition: fieldCondition(node.Column, operation, values)}, true

	case PlanStringMatch:
		var operation string
		switch node.Operator {
		case StartsWith:
			operation = "prefix"
		case Contains:
			operation = "like"
		default:
			// ENDS WITH has no filter operation that writes LIKE '%v'.
			return pushedFilter{}, false
		}
		value := node.Value
		if operation == "like" {
			// like matches its value as a literal substring, reading a
			// backslash as an escape and refusing a bare %; escaping both keeps
			// the literal match the SQL path makes. prefix takes the value as
			// it is.
			value = likeValueEscaper.Replace(value)
		}
		if negated {
			operation = "not_" + operation
		}
		return pushedFilter{condition: fieldCondition(node.Column, operation, value)}, true

	case PlanNever:
		if negated {
			return pushedFilter{constant: &alwaysTrue}, true
		}
		return pushedFilter{constant: &alwaysFalse}, true

	case PlanNegation:
		return pushFilterNegated(node.Operand, !negated)

	case PlanLogical:
		operator := node.Operator
		switch {
		case operator == "AND" && !negated, operator == "OR" && negated:
			return pushJunction("and", node.Operands, negated)
		case operator == "OR" && !negated, operator == "AND" && negated:
			return pushJunction("or", node.Operands, negated)
		default:
			// XOR has no filter operation.
			return pushedFilter{}, false
		}

	default:
		// Comparisons between columns or over computed values read more than
		// one field, which the filter tree cannot express.
		return pushedFilter{}, false
	}
}

// pushJunction writes an AND or OR, folding constant operands away the way
// the logic would: a false operand decides an AND, a true one decides an OR.
func pushJunction(operation string, operands []PlanPredicate, negated bool) (pushedFilter, bool) {
	deciding := operation == "or"
	subConditions := make([]map[string]any, 0, len(operands))
	for _, operand := range operands {
		pushed, ok := pushFilterNegated(operand, negated)
		if !ok {
			return pushedFilter{}, false
		}
		if pushed.constant != nil {
			if *pushed.constant == deciding {
				return pushed, true
			}
			continue
		}
		subConditions = append(subConditions, pushed.condition)
	}
	switch len(subConditions) {
	case 0:
		result := !deciding
		return pushedFilter{constant: &result}, true
	default:
		return pushedFilter{condition: junction(operation, subConditions)}, true
	}
}

// likeValueEscaper writes a literal substring in the escaped form vega-backend's
// like and not_like accept.
var likeValueEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`)

// maxSubConditions is the most operands vega-backend accepts in one and/or.
const maxSubConditions = 100

// junction joins conditions under and or or. Past maxSubConditions operands it
// nests them in groups, which and and or allow.
func junction(operation string, subConditions []map[string]any) map[string]any {
	if len(subConditions) == 1 {
		return subConditions[0]
	}
	for len(subConditions) > maxSubConditions {
		grouped := make([]map[string]any, 0, (len(subConditions)+maxSubConditions-1)/maxSubConditions)
		for from := 0; from < len(subConditions); from += maxSubConditions {
			to := min(from+maxSubConditions, len(subConditions))
			if to-from == 1 {
				grouped = append(grouped, subConditions[from])
				continue
			}
			grouped = append(grouped, map[string]any{"operation": operation, "sub_conditions": subConditions[from:to]})
		}
		subConditions = grouped
	}
	if len(subConditions) == 1 {
		return subConditions[0]
	}
	return map[string]any{"operation": operation, "sub_conditions": subConditions}
}

func fieldCondition(field, operation string, value any) map[string]any {
	return map[string]any{"field": field, "operation": operation, "value_from": "const", "value": value}
}

// andFilters joins the filters pushed to one table. ok is false when one of
// them is always false, so the table contributes no row.
func andFilters(filters []pushedFilter) (map[string]any, bool) {
	var subConditions []map[string]any
	for _, filter := range filters {
		if filter.constant != nil {
			if !*filter.constant {
				return nil, false
			}
			continue
		}
		subConditions = append(subConditions, filter.condition)
	}
	if len(subConditions) == 0 {
		return nil, true
	}
	return junction("and", subConditions), true
}

// literalFilterValue is the JSON value a literal is sent as. ok is false for a
// literal the filter tree cannot carry, which leaves the condition to the
// executor.
func literalFilterValue(literal Literal) (any, bool) {
	switch literal.Kind {
	case LiteralString:
		return literal.String, true
	case LiteralInteger:
		return literal.Integer, true
	case LiteralFloat:
		return literal.Float, true
	case LiteralBoolean:
		return literal.Boolean, true
	default:
		return nil, false
	}
}

// keyInCondition restricts a read to the rows whose key columns take one of the
// given values. A composite key is restricted column by column, which admits a
// superset of the wanted tuples; the executor matches the tuples exactly
// afterwards.
func keyInCondition(columns []string, tuples [][]any) (map[string]any, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("a join reads no key column")
	}
	subConditions := make([]map[string]any, 0, len(columns))
	for i, column := range columns {
		seen := map[string]bool{}
		values := make([]any, 0, len(tuples))
		for _, tuple := range tuples {
			key := canonicalKey(tuple[i])
			if !seen[key] {
				seen[key] = true
				values = append(values, filterValue(tuple[i]))
			}
		}
		subConditions = append(subConditions, fieldCondition(column, "in", values))
	}
	return junction("and", subConditions), nil
}

// dateComparison is a comparison over a date or time property. vega-backend
// accepts only epoch milliseconds or RFC 3339 for such a field and converts
// them with the session's time zone, while the SQL path lets the database read
// the literal as a wall-clock value. It is therefore never pushed; the
// executor compares the values as wall-clock times, as the database would.
type dateComparison struct {
	PlanPredicate
}

// markDateComparisons wraps every comparison and IN over a date or time
// column, at any depth, so that pushFilter leaves it to the executor.
func markDateComparisons(predicate PlanPredicate, isDate func(table int, column string) bool) PlanPredicate {
	switch node := predicate.(type) {
	case PlanCondition:
		if isDate(node.Table, node.Column) {
			return dateComparison{PlanPredicate: node}
		}
	case PlanMembership:
		if isDate(node.Table, node.Column) {
			return dateComparison{PlanPredicate: node}
		}
	case PlanNegation:
		return PlanNegation{Operand: markDateComparisons(node.Operand, isDate)}
	case PlanLogical:
		operands := make([]PlanPredicate, len(node.Operands))
		for i, operand := range node.Operands {
			operands[i] = markDateComparisons(operand, isDate)
		}
		return PlanLogical{Operator: node.Operator, Operands: operands}
	}
	return predicate
}
