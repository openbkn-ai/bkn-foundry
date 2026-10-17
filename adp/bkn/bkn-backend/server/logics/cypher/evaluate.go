// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The bind-join executor finishes a query in memory: the conditions the data
// source could not evaluate, then the projection, grouping, DISTINCT, ORDER BY,
// SKIP and LIMIT that one SQL statement would have done. The SQL path is the
// reference, so where SQL and Cypher disagree this follows SQL as the
// statement would have run on MySQL: a comparison with NULL is unknown, SUM
// over no value is NULL, division by zero is NULL, and NULL sorts first in
// ascending order.

// binding is one combination of rows the pattern matched, indexed by plan
// table. A nil row is a table an OPTIONAL MATCH found nothing for, whose
// columns all read as NULL.
type binding []map[string]any

// truth is a three-valued logical result.
type truth int8

const (
	truthFalse truth = iota
	truthTrue
	truthUnknown
)

func truthOf(value bool) truth {
	if value {
		return truthTrue
	}
	return truthFalse
}

func (t truth) not() truth {
	switch t {
	case truthTrue:
		return truthFalse
	case truthFalse:
		return truthTrue
	default:
		return truthUnknown
	}
}

// number is a numeric value, kept as an integer for as long as it is one so
// that large keys and counts stay exact.
type number struct {
	integer bool
	i       int64
	f       float64
}

func (n number) float() float64 {
	if n.integer {
		return float64(n.i)
	}
	return n.f
}

// asNumber reads a value as a number. Values read from vega-backend arrive as
// json.Number; values the plan carries arrive as Go numbers. A boolean reads as
// 1 or 0, as it does in MySQL.
func asNumber(value any) (number, bool) {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return number{integer: true, i: i}, true
		}
		if f, err := v.Float64(); err == nil {
			return number{f: f}, true
		}
		return number{}, false
	case int64:
		return number{integer: true, i: v}, true
	case int:
		return number{integer: true, i: int64(v)}, true
	case float64:
		return number{f: v}, true
	case bool:
		if v {
			return number{integer: true, i: 1}, true
		}
		return number{integer: true, i: 0}, true
	default:
		return number{}, false
	}
}

// coerceNumber reads a value as a number where SQL would compute with it. A
// string converts the way MySQL converts one: its leading numeric part, or 0,
// as a double -- which is also why SUM over a string column is a float there.
func coerceNumber(value any) (number, bool) {
	text, isString := value.(string)
	if !isString {
		return asNumber(value)
	}
	return number{f: leadingFloat(text)}, true
}

// leadingFloat parses the longest numeric prefix of a string, after leading
// spaces, as MySQL does when it converts a string to a number.
func leadingFloat(text string) float64 {
	text = strings.TrimLeft(text, " \t\n\r")
	end, digits, seenDot, seenExp := 0, false, false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
		case (c == '+' || c == '-') && (i == 0 || text[i-1] == 'e' || text[i-1] == 'E'):
		case c == '.' && !seenDot && !seenExp:
			seenDot = true
		case (c == 'e' || c == 'E') && digits && !seenExp:
			seenExp = true
		default:
			i = len(text)
			continue
		}
		if digits {
			end = i + 1
		}
	}
	for end > 0 {
		if f, err := strconv.ParseFloat(text[:end], 64); err == nil {
			return f
		}
		end--
	}
	return 0
}

func (n number) value() json.Number {
	if n.integer {
		return json.Number(strconv.FormatInt(n.i, 10))
	}
	// A whole float keeps a fractional form, so it still reads as a float.
	text := strconv.FormatFloat(n.f, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eEn") {
		text += ".0"
	}
	return json.Number(text)
}

func compareNumbers(a, b number) int {
	if a.integer && b.integer {
		switch {
		case a.i < b.i:
			return -1
		case a.i > b.i:
			return 1
		default:
			return 0
		}
	}
	af, bf := a.float(), b.float()
	switch {
	case af < bf:
		return -1
	case af > bf:
		return 1
	default:
		return 0
	}
}

// compareValues orders two non-null values. ok is false when either is NULL
// or the two cannot be compared, which a condition reads as unknown.
func compareValues(a, b any) (int, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	as, aString := a.(string)
	bs, bString := b.(string)
	if aString && bString {
		return strings.Compare(as, bs), true
	}
	// A string against a number compares as numbers, the way MySQL converts.
	an, aOK := coerceNumber(a)
	bn, bOK := coerceNumber(b)
	if !aOK || !bOK {
		return 0, false
	}
	return compareNumbers(an, bn), true
}

// canonicalKey encodes a value so that values SQL treats as equal encode the
// same: 1, 1.0 and TRUE are one key. It is how joins, groups and DISTINCT
// compare rows.
func canonicalKey(value any) string {
	if value == nil {
		return "\x00"
	}
	if s, ok := value.(string); ok {
		return "s" + s
	}
	if n, ok := asNumber(value); ok {
		if !n.integer && n.f == math.Trunc(n.f) && math.Abs(n.f) < 1<<62 {
			return "n" + strconv.FormatInt(int64(n.f), 10)
		}
		if n.integer {
			return "n" + strconv.FormatInt(n.i, 10)
		}
		return "n" + strconv.FormatFloat(n.f, 'g', -1, 64)
	}
	return fmt.Sprintf("o%v", value)
}

func canonicalTuple(values []any) string {
	var b strings.Builder
	for _, value := range values {
		key := canonicalKey(value)
		b.WriteString(strconv.Itoa(len(key)))
		b.WriteByte(':')
		b.WriteString(key)
	}
	return b.String()
}

// filterValue is how a value read from one table is sent back in another
// table's filter: as the number or string it is.
func filterValue(value any) any {
	if n, ok := value.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return value
}

func literalValue(literal Literal) any {
	switch literal.Kind {
	case LiteralString:
		return literal.String
	case LiteralInteger:
		return literal.Integer
	case LiteralFloat:
		return literal.Float
	case LiteralBoolean:
		return literal.Boolean
	default:
		return nil
	}
}

// scope is what a value is evaluated against: one matched row, or, once rows
// are grouped, the group, whose first row supplies the grouping columns.
type scope struct {
	row   binding
	group []binding
}

func (s scope) column(table int, column string) any {
	if table < 0 || table >= len(s.row) || s.row[table] == nil {
		return nil
	}
	return s.row[table][column]
}

// evaluatePredicate decides a condition for one matched row.
func evaluatePredicate(predicate PlanPredicate, s scope) (truth, error) {
	switch node := predicate.(type) {
	case PlanCondition:
		return compareTruth(s.column(node.Table, node.Column), node.Operator, literalValue(node.Value))

	case PlanNullCheck:
		isNull := s.column(node.Table, node.Column) == nil
		return truthOf(isNull != node.Negated), nil

	case PlanMembership:
		if len(node.Values) == 0 {
			return truthOf(node.Negated), nil
		}
		value := s.column(node.Table, node.Column)
		if value == nil {
			return truthUnknown, nil
		}
		result := truthFalse
		for _, literal := range node.Values {
			if order, ok := compareValues(value, literalValue(literal)); ok && order == 0 {
				result = truthTrue
				break
			}
		}
		if node.Negated {
			return result.not(), nil
		}
		return result, nil

	case PlanStringMatch:
		value := s.column(node.Table, node.Column)
		if value == nil {
			return truthUnknown, nil
		}
		text, ok := value.(string)
		if !ok {
			text = fmt.Sprint(value)
		}
		switch node.Operator {
		case StartsWith:
			return truthOf(strings.HasPrefix(text, node.Value)), nil
		case EndsWith:
			return truthOf(strings.HasSuffix(text, node.Value)), nil
		case Contains:
			return truthOf(strings.Contains(text, node.Value)), nil
		default:
			return truthUnknown, fmt.Errorf("cannot evaluate a %q string predicate", node.Operator)
		}

	case PlanColumnComparison:
		return compareTruth(s.column(node.LeftTable, node.LeftColumn), node.Operator,
			s.column(node.RightTable, node.RightColumn))

	case PlanExpressionComparison:
		left, err := evaluateExpression(node.Left, s)
		if err != nil {
			return truthUnknown, err
		}
		right, err := evaluateExpression(node.Right, s)
		if err != nil {
			return truthUnknown, err
		}
		return compareTruth(left, node.Operator, right)

	case PlanNever:
		return truthFalse, nil

	case dateComparison:
		return evaluateDateComparison(node.PlanPredicate, s)

	case PlanNegation:
		operand, err := evaluatePredicate(node.Operand, s)
		return operand.not(), err

	case PlanLogical:
		return evaluateLogical(node, s)

	default:
		return truthUnknown, fmt.Errorf("cannot evaluate a %T condition", predicate)
	}
}

func evaluateLogical(node PlanLogical, s scope) (truth, error) {
	switch node.Operator {
	case "AND":
		result := truthTrue
		for _, operand := range node.Operands {
			value, err := evaluatePredicate(operand, s)
			if err != nil {
				return truthUnknown, err
			}
			if value == truthFalse {
				return truthFalse, nil
			}
			if value == truthUnknown {
				result = truthUnknown
			}
		}
		return result, nil
	case "OR":
		result := truthFalse
		for _, operand := range node.Operands {
			value, err := evaluatePredicate(operand, s)
			if err != nil {
				return truthUnknown, err
			}
			if value == truthTrue {
				return truthTrue, nil
			}
			if value == truthUnknown {
				result = truthUnknown
			}
		}
		return result, nil
	case "XOR":
		result := false
		for _, operand := range node.Operands {
			value, err := evaluatePredicate(operand, s)
			if err != nil || value == truthUnknown {
				return truthUnknown, err
			}
			result = result != (value == truthTrue)
		}
		return truthOf(result), nil
	default:
		return truthUnknown, fmt.Errorf("cannot evaluate the %q operator", node.Operator)
	}
}

func compareTruth(left any, operator string, right any) (truth, error) {
	order, ok := compareValues(left, right)
	if !ok {
		return truthUnknown, nil
	}
	return orderTruth(order, operator)
}

// orderTruth reads a comparison's result off the order of its two sides.
func orderTruth(order int, operator string) (truth, error) {
	switch operator {
	case "=":
		return truthOf(order == 0), nil
	case "<>":
		return truthOf(order != 0), nil
	case "<":
		return truthOf(order < 0), nil
	case "<=":
		return truthOf(order <= 0), nil
	case ">":
		return truthOf(order > 0), nil
	case ">=":
		return truthOf(order >= 0), nil
	default:
		return truthUnknown, fmt.Errorf("cannot evaluate the %q comparison", operator)
	}
}

// errIntegerOverflow is an integer result outside the 64-bit range, which
// MySQL refuses as well.
var errIntegerOverflow = fmt.Errorf("integer arithmetic overflowed")

// evaluateExpression computes a value. An aggregate inside it is computed over
// the scope's group.
func evaluateExpression(value PlanExpression, s scope) (any, error) {
	switch node := value.(type) {
	case PlanValueColumn:
		return s.column(node.Table, node.Column), nil
	case PlanValueLiteral:
		return literalValue(node.Value), nil
	case PlanAggregate:
		return evaluateAggregate(node, s.group)
	case PlanNegative:
		operand, err := evaluateExpression(node.Operand, s)
		if err != nil || operand == nil {
			return nil, err
		}
		n, ok := coerceNumber(operand)
		if !ok {
			return nil, fmt.Errorf("cannot negate a non-numeric value")
		}
		if n.integer {
			if n.i == math.MinInt64 {
				return nil, errIntegerOverflow
			}
			return number{integer: true, i: -n.i}.value(), nil
		}
		return number{f: -n.f}.value(), nil
	case PlanArithmetic:
		left, err := evaluateExpression(node.Left, s)
		if err != nil {
			return nil, err
		}
		right, err := evaluateExpression(node.Right, s)
		if err != nil {
			return nil, err
		}
		return computeArithmetic(node.Operator, left, right)
	default:
		return nil, fmt.Errorf("cannot evaluate a %T value", value)
	}
}

func computeArithmetic(operator string, left, right any) (any, error) {
	if left == nil || right == nil {
		return nil, nil
	}
	a, aOK := coerceNumber(left)
	b, bOK := coerceNumber(right)
	if !aOK || !bOK {
		return nil, fmt.Errorf("arithmetic over a non-numeric value")
	}
	switch operator {
	case "/":
		if b.float() == 0 {
			return nil, nil
		}
		return number{f: a.float() / b.float()}.value(), nil
	case "%":
		if !a.integer || !b.integer {
			// A value that was a string computes as a double, as MOD does.
			if b.float() == 0 {
				return nil, nil
			}
			return number{f: math.Mod(a.float(), b.float())}.value(), nil
		}
		if b.i == 0 {
			return nil, nil
		}
		if a.i == math.MinInt64 && b.i == -1 {
			return number{integer: true}.value(), nil
		}
		return number{integer: true, i: a.i % b.i}.value(), nil
	}
	if a.integer && b.integer {
		result, err := integerArithmetic(operator, a.i, b.i)
		if err != nil {
			return nil, err
		}
		return number{integer: true, i: result}.value(), nil
	}
	switch operator {
	case "+":
		return number{f: a.float() + b.float()}.value(), nil
	case "-":
		return number{f: a.float() - b.float()}.value(), nil
	case "*":
		return number{f: a.float() * b.float()}.value(), nil
	default:
		return nil, fmt.Errorf("cannot evaluate the %q operator", operator)
	}
}

func integerArithmetic(operator string, a, b int64) (int64, error) {
	switch operator {
	case "+":
		sum := a + b
		if (a > 0 && b > 0 && sum < 0) || (a < 0 && b < 0 && sum >= 0) {
			return 0, errIntegerOverflow
		}
		return sum, nil
	case "-":
		difference := a - b
		if (a >= 0 && b < 0 && difference < 0) || (a < 0 && b > 0 && difference >= 0) {
			return 0, errIntegerOverflow
		}
		return difference, nil
	case "*":
		if a == 0 || b == 0 {
			return 0, nil
		}
		negative := (a < 0) != (b < 0)
		hi, lo := bits.Mul64(absInt64(a), absInt64(b))
		limit := uint64(math.MaxInt64)
		if negative {
			limit++
		}
		if hi != 0 || lo > limit {
			return 0, errIntegerOverflow
		}
		if negative {
			return -int64(lo-1) - 1, nil
		}
		return int64(lo), nil
	default:
		return 0, fmt.Errorf("cannot evaluate the %q operator", operator)
	}
}

func absInt64(v int64) uint64 {
	if v < 0 {
		return uint64(-(v + 1)) + 1
	}
	return uint64(v)
}

// evaluateAggregate folds an aggregate over a group the way SQL does: NULL
// arguments are skipped, and SUM, AVG, MIN and MAX of nothing are NULL.
func evaluateAggregate(aggregate PlanAggregate, group []binding) (any, error) {
	if aggregate.Star {
		return number{integer: true, i: int64(len(group))}.value(), nil
	}
	values := make([]any, 0, len(group))
	seen := map[string]bool{}
	for _, row := range group {
		var value any
		if aggregate.Argument != nil {
			computed, err := evaluateExpression(aggregate.Argument, scope{row: row})
			if err != nil {
				return nil, err
			}
			value = computed
		} else {
			value = scope{row: row}.column(aggregate.Table, aggregate.Column)
		}
		if value == nil {
			continue
		}
		if aggregate.Distinct {
			key := canonicalKey(value)
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		values = append(values, value)
	}

	switch aggregate.Function {
	case "COUNT":
		return number{integer: true, i: int64(len(values))}.value(), nil
	case "SUM", "AVG":
		if len(values) == 0 {
			return nil, nil
		}
		sum := number{integer: true}
		for _, value := range values {
			n, ok := coerceNumber(value)
			if !ok {
				return nil, fmt.Errorf("%s over a non-numeric value", aggregate.Function)
			}
			if sum.integer && n.integer {
				if total, err := integerArithmetic("+", sum.i, n.i); err == nil {
					sum.i = total
					continue
				}
			}
			sum = number{f: sum.float() + n.float()}
		}
		if aggregate.Function == "AVG" {
			return number{f: sum.float() / float64(len(values))}.value(), nil
		}
		return sum.value(), nil
	case "MIN", "MAX":
		if len(values) == 0 {
			return nil, nil
		}
		best := values[0]
		for _, value := range values[1:] {
			order, ok := compareValues(value, best)
			if !ok {
				continue
			}
			if (aggregate.Function == "MIN" && order < 0) || (aggregate.Function == "MAX" && order > 0) {
				best = value
			}
		}
		return best, nil
	default:
		return nil, fmt.Errorf("cannot evaluate the %s aggregate", aggregate.Function)
	}
}

// columnValue computes one output column for a scope.
func columnValue(column PlanColumn, s scope) (any, error) {
	switch {
	case column.Expression != nil:
		return evaluateExpression(column.Expression, s)
	case column.Aggregate != nil:
		return evaluateAggregate(*column.Aggregate, s.group)
	default:
		return s.column(column.Table, column.Column), nil
	}
}

// outputRow is one result row and the scope it was computed from, which ORDER
// BY may still read.
type outputRow struct {
	values []any
	scope  scope
}

// finishRows turns the matched rows into the result: grouping and aggregation,
// DISTINCT, ORDER BY, then SKIP and LIMIT. defaultLimit applies when the query
// wrote no LIMIT, as it does to the generated statement. presorted rows came
// back from the data source in the query's order, which is kept.
func finishRows(plan *Plan, rows []binding, defaultLimit int64, presorted bool) ([]map[string]any, error) {
	outputs, err := projectRows(plan, rows)
	if err != nil {
		return nil, err
	}
	if plan.Distinct {
		outputs = distinctRows(outputs)
	}
	if len(plan.OrderBy) > 0 && !presorted {
		if err := sortRows(plan, outputs); err != nil {
			return nil, err
		}
	}

	start := int64(0)
	if plan.Skip != nil && *plan.Skip > 0 {
		start = *plan.Skip
	}
	limit := defaultLimit
	if plan.Limit != nil {
		limit = *plan.Limit
	}
	if start > int64(len(outputs)) {
		start = int64(len(outputs))
	}
	end := int64(len(outputs))
	if limit >= 0 && start+limit < end {
		end = start + limit
	}

	entries := make([]map[string]any, 0, end-start)
	for _, output := range outputs[start:end] {
		entry := make(map[string]any, len(plan.Select))
		for i, column := range plan.Select {
			entry[column.Alias] = output.values[i]
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func projectRows(plan *Plan, rows []binding) ([]outputRow, error) {
	aggregating := false
	for _, column := range plan.Select {
		if column.aggregates() {
			aggregating = true
			break
		}
	}
	if !aggregating {
		outputs := make([]outputRow, 0, len(rows))
		for _, row := range rows {
			output, err := projectScope(plan, scope{row: row, group: []binding{row}})
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, output)
		}
		return outputs, nil
	}

	var groups [][]binding
	if len(plan.GroupBy) == 0 {
		// Aggregating every column folds the whole match into one row, which
		// SQL returns even when nothing matched.
		groups = [][]binding{rows}
	} else {
		index := map[string]int{}
		for _, row := range rows {
			keys := make([]any, 0, len(plan.GroupBy))
			for _, column := range plan.GroupBy {
				value, err := columnValue(column, scope{row: row, group: []binding{row}})
				if err != nil {
					return nil, err
				}
				keys = append(keys, value)
			}
			key := canonicalTuple(keys)
			position, exists := index[key]
			if !exists {
				position = len(groups)
				index[key] = position
				groups = append(groups, nil)
			}
			groups[position] = append(groups[position], row)
		}
	}

	outputs := make([]outputRow, 0, len(groups))
	for _, group := range groups {
		var first binding
		if len(group) > 0 {
			first = group[0]
		}
		output, err := projectScope(plan, scope{row: first, group: group})
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func projectScope(plan *Plan, s scope) (outputRow, error) {
	values := make([]any, len(plan.Select))
	for i, column := range plan.Select {
		value, err := columnValue(column, s)
		if err != nil {
			return outputRow{}, err
		}
		values[i] = value
	}
	return outputRow{values: values, scope: s}, nil
}

func distinctRows(outputs []outputRow) []outputRow {
	seen := map[string]bool{}
	kept := outputs[:0]
	for _, output := range outputs {
		key := canonicalTuple(output.values)
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, output)
	}
	return kept
}

func sortRows(plan *Plan, outputs []outputRow) error {
	keys := make([][]any, len(outputs))
	for i, output := range outputs {
		row := make([]any, len(plan.OrderBy))
		for j, order := range plan.OrderBy {
			value, err := orderValue(plan, order, output)
			if err != nil {
				return err
			}
			row[j] = value
		}
		keys[i] = row
	}
	positions := make([]int, len(outputs))
	for i := range positions {
		positions[i] = i
	}
	sort.SliceStable(positions, func(x, y int) bool {
		a, b := keys[positions[x]], keys[positions[y]]
		for j, order := range plan.OrderBy {
			c := sortCompare(a[j], b[j])
			if c == 0 {
				continue
			}
			if order.Descending {
				return c > 0
			}
			return c < 0
		}
		return false
	})
	sorted := make([]outputRow, len(outputs))
	for i, position := range positions {
		sorted[i] = outputs[position]
	}
	copy(outputs, sorted)
	return nil
}

func orderValue(plan *Plan, order PlanOrder, output outputRow) (any, error) {
	switch {
	case order.Alias != "":
		for i, column := range plan.Select {
			if column.Alias == order.Alias {
				return output.values[i], nil
			}
		}
		return nil, fmt.Errorf("sort key %q is not a returned column", order.Alias)
	case order.Expression != nil:
		return evaluateExpression(order.Expression, output.scope)
	case order.Aggregate != nil:
		return evaluateAggregate(*order.Aggregate, output.scope.group)
	default:
		return output.scope.column(order.Table, order.Column), nil
	}
}

// sortCompare orders two values for ORDER BY. NULL is the smallest value, as
// in MySQL, and values that cannot be compared are ordered by kind so that the
// order is at least total.
func sortCompare(a, b any) int {
	if a == nil || b == nil {
		switch {
		case a == nil && b == nil:
			return 0
		case a == nil:
			return -1
		default:
			return 1
		}
	}
	if order, ok := compareValues(a, b); ok {
		return order
	}
	return strings.Compare(canonicalKey(a), canonicalKey(b))
}

// evaluateDateComparison decides a comparison or IN over a date or time column
// by wall-clock time, the way the database reads a literal against such a
// column. A value that is not a recognisable date falls back to comparing as
// written.
func evaluateDateComparison(predicate PlanPredicate, s scope) (truth, error) {
	switch node := predicate.(type) {
	case PlanCondition:
		order, ok := compareDates(s.column(node.Table, node.Column), literalValue(node.Value))
		if !ok {
			return truthUnknown, nil
		}
		return orderTruth(order, node.Operator)
	case PlanMembership:
		if len(node.Values) == 0 {
			return truthOf(node.Negated), nil
		}
		value := s.column(node.Table, node.Column)
		if value == nil {
			return truthUnknown, nil
		}
		result := truthFalse
		for _, literal := range node.Values {
			if order, ok := compareDates(value, literalValue(literal)); ok && order == 0 {
				result = truthTrue
				break
			}
		}
		if node.Negated {
			return result.not(), nil
		}
		return result, nil
	default:
		return evaluatePredicate(predicate, s)
	}
}

// wallClockLayouts are the forms a date reaches the executor in: RFC 3339 as
// vega-backend writes a column value, and the forms a query writes a literal in.
var wallClockLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

// wallClock reads a date as its wall-clock reading, dropping any offset: a
// DATETIME column has none, and the driver reports it in its own zone.
func wallClock(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}
	text = strings.TrimSpace(text)
	for _, layout := range wallClockLayouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(),
				parsed.Second(), parsed.Nanosecond(), time.UTC), true
		}
	}
	return time.Time{}, false
}

// compareDates orders two date values by wall-clock time, or compares them as
// written when either is not a date. ok is false when either is NULL.
func compareDates(a, b any) (int, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	at, aOK := wallClock(a)
	bt, bOK := wallClock(b)
	if !aOK || !bOK {
		return compareValues(a, b)
	}
	return at.Compare(bt), true
}
