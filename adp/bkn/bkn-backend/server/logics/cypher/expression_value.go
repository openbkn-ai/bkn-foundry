// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type omittedSliceBound struct{}

type expressionError struct{ Kind, Message string }

func (e *expressionError) Error() string { return e.Message }
func expressionTypeError() error {
	return &expressionError{Kind: "type_error", Message: "expression operands have incompatible types"}
}
func expressionArithmeticError(message string) error {
	return &expressionError{Kind: "arithmetic_error", Message: message}
}

func normalizeExpressionParameter(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, expressionTypeError()
	}
	if len(raw) > graphPayloadBudget {
		return nil, errGraphBudget
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var normalize func(any, int) (any, error)
	normalize = func(value any, depth int) (any, error) {
		if depth > 32 {
			return nil, fmt.Errorf("parameter nesting exceeds 32 levels")
		}
		switch typed := value.(type) {
		case json.Number:
			if integer, err := typed.Int64(); err == nil {
				return integer, nil
			}
			number, err := typed.Float64()
			if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
				return nil, expressionTypeError()
			}
			if !strings.ContainsAny(typed.String(), ".eE") {
				return nil, expressionArithmeticError("integer parameter is out of range")
			}
			return number, nil
		case []any:
			for i, item := range typed {
				normalized, err := normalize(item, depth+1)
				if err != nil {
					return nil, err
				}
				typed[i] = normalized
			}
			return typed, nil
		case map[string]any:
			for key, item := range typed {
				normalized, err := normalize(item, depth+1)
				if err != nil {
					return nil, err
				}
				typed[key] = normalized
			}
			return typed, nil
		default:
			return value, nil
		}
	}
	normalized, err := normalize(decoded, 0)
	if err != nil {
		return nil, err
	}
	return restoreParameterFloats(value, normalized), nil
}

// JSON encoding drops the decimal marker from integral Go floats. Preserve
// their input type, including inside lists/maps, rather than changing integer
// division or making a float usable as an integer pagination parameter.
func restoreParameterFloats(original, normalized any) any {
	switch value := original.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case []any:
		if items, ok := normalized.([]any); ok {
			for i, item := range value {
				items[i] = restoreParameterFloats(item, items[i])
			}
		}
	case map[string]any:
		if fields, ok := normalized.(map[string]any); ok {
			for key, item := range value {
				fields[key] = restoreParameterFloats(item, fields[key])
			}
		}
	}
	return normalized
}
func expressionValueType(value any) string {
	switch typed := value.(type) {
	case temporalValue:
		return typed.kind
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case int, int64, uint64:
		return "integer"
	case float64:
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "map"
	default:
		return "any"
	}
}

// expressionNumber keeps integer comparisons exact even when a connector
// returns json.Number. Decimal/exponent JSON numbers use the same IEEE value
// as floating-point literals and parameters; integer JSON numbers stay exact.
func expressionNumber(value any) (*big.Rat, bool, bool) {
	integer := true
	var raw string
	switch typed := value.(type) {
	case json.Number:
		raw = typed.String()
		integer = !strings.ContainsAny(raw, ".eE")
		if !integer {
			number, err := typed.Float64()
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return nil, false, false
			}
			return new(big.Rat).SetFloat64(number), false, true
		}
	case int:
		raw = strconv.Itoa(typed)
	case int64:
		raw = strconv.FormatInt(typed, 10)
	case uint64:
		raw = strconv.FormatUint(typed, 10)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, false, false
		}
		return new(big.Rat).SetFloat64(typed), false, true
	default:
		rv := reflect.ValueOf(value)
		if !rv.IsValid() {
			return nil, false, false
		}
		switch rv.Kind() {
		case reflect.Int8, reflect.Int16, reflect.Int32:
			raw = strconv.FormatInt(rv.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
			raw = strconv.FormatUint(rv.Uint(), 10)
		default:
			return nil, false, false
		}
	}
	number, ok := new(big.Rat).SetString(raw)
	return number, integer, ok
}
func expressionInteger(value any) (int64, error) {
	number, integer, ok := expressionNumber(value)
	if !ok || !integer || !number.IsInt() || !number.Num().IsInt64() {
		return 0, expressionTypeError()
	}
	return number.Num().Int64(), nil
}

func expressionEquality(left, right any) (any, error) {
	if left == nil || right == nil {
		return nil, nil
	}
	if value, ok := left.(temporalValue); ok {
		other, ok := right.(temporalValue)
		return ok && value.kind == other.kind && value.String() == other.String(), nil
	}
	if expressionNaN(left) || expressionNaN(right) {
		return false, nil
	}
	if expressionNumeric(left) && expressionNumeric(right) {
		order, err := expressionNumericOrder(left, right)
		return order == 0, err
	}
	if a, _, ok := expressionNumber(left); ok {
		b, _, ok := expressionNumber(right)
		if !ok {
			return false, nil
		}
		return a.Cmp(b) == 0, nil
	}
	switch a := left.(type) {
	case string:
		b, ok := right.(string)
		return ok && a == b, nil
	case bool:
		b, ok := right.(bool)
		return ok && a == b, nil
	case []any:
		b, ok := right.([]any)
		if !ok || len(a) != len(b) {
			return false, nil
		}
		unknown := false
		for i := range a {
			equal, err := expressionEquality(a[i], b[i])
			if err != nil {
				return nil, err
			}
			if equal == false {
				return false, nil
			}
			unknown = unknown || equal == nil
		}
		if unknown {
			return nil, nil
		}
		return true, nil
	case map[string]any:
		b, ok := right.(map[string]any)
		if !ok || len(a) != len(b) {
			return false, nil
		}
		unknown := false
		for key, value := range a {
			other, exists := b[key]
			if !exists {
				return false, nil
			}
			equal, err := expressionEquality(value, other)
			if err != nil {
				return nil, err
			}
			if equal == false {
				return false, nil
			}
			unknown = unknown || equal == nil
		}
		if unknown {
			return nil, nil
		}
		return true, nil
	default:
		return reflect.DeepEqual(graphIdentity(left), graphIdentity(right)), nil
	}
}
func expressionCompare(left, right any) (int, error) {
	if value, ok := left.(temporalValue); ok {
		other, ok := right.(temporalValue)
		if !ok || value.kind != other.kind {
			return 0, errIncomparable
		}
		if value.kind == "duration" {
			total := func(value temporalValue) *big.Int {
				seconds := new(big.Int).Mul(big.NewInt(value.months), big.NewInt(2629746))
				seconds.Add(seconds, new(big.Int).Mul(big.NewInt(value.days), big.NewInt(86400)))
				seconds.Add(seconds, big.NewInt(value.seconds))
				seconds.Mul(seconds, big.NewInt(1000000000))
				return seconds.Add(seconds, big.NewInt(value.nanoseconds))
			}
			return total(value).Cmp(total(other)), nil
		}
		if value.instant.Before(other.instant) {
			return -1, nil
		}
		if value.instant.After(other.instant) {
			return 1, nil
		}
		return strings.Compare(value.zone, other.zone), nil
	}
	if left == nil {
		if right == nil {
			return 0, nil
		}
		return 1, nil
	}
	if right == nil {
		return -1, nil
	}
	if expressionNumeric(left) || expressionNumeric(right) {
		return expressionNumericOrder(left, right)
	}

	switch a := left.(type) {
	case string:
		b, ok := right.(string)
		if !ok {
			return 0, errIncomparable
		}
		return strings.Compare(a, b), nil
	case bool:
		b, ok := right.(bool)
		if !ok {
			return 0, errIncomparable
		}
		if a == b {
			return 0, nil
		}
		if a {
			return 1, nil
		}
		return -1, nil
	case []any:
		b, ok := right.([]any)
		if !ok {
			return 0, errIncomparable
		}
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] == nil || b[i] == nil {
				return 0, errIncomparable
			}
			order, err := expressionCompare(a[i], b[i])
			if err != nil || order != 0 {
				return order, err
			}
		}
		if len(a) < len(b) {
			return -1, nil
		}
		if len(a) > len(b) {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, errIncomparable
	}
}

// expressionIdentity is grouping equivalence, deliberately separate from the
// nullable '=' operator. Equal numeric representations share one group.
func expressionIdentity(value any) any {
	if number, _, ok := expressionNumber(value); ok {
		return struct {
			Number string `json:"number"`
		}{number.RatString()}
	}
	switch typed := graphIdentity(value).(type) {
	case temporalValue:
		return struct {
			Kind  string
			Value string
		}{typed.kind, typed.String()}
	case []any:
		values := make([]any, len(typed))
		for i, item := range typed {
			values[i] = expressionIdentity(item)
		}
		return values
	case map[string]any:
		// Tag maps to keep {number:'1'} distinct from the internal numeric key.
		fields := make([]any, 0, len(typed))
		for _, key := range sortedGraphKeys(typed) {
			fields = append(fields, []any{key, expressionIdentity(typed[key])})
		}
		return struct {
			Map []any `json:"map"`
		}{fields}
	default:
		return typed
	}
}
func expressionKey(value any) (string, error) {
	encoded, err := json.Marshal(expressionIdentity(value))
	return string(encoded), err
}

func evaluateOperation(plan *Plan, expression *GraphExpression, row map[string]any) (any, error) {
	operator := expression.Operation
	if operator == "OMITTED_BOUND" {
		return omittedSliceBound{}, nil
	}
	arguments := expression.Arguments
	eval := func(i int) (any, error) { return evaluateGraph(plan, arguments[i], row) }
	if operator == "CASE" {
		for i := 0; i+1 < len(arguments); i += 2 {
			condition, err := eval(i)
			if err != nil {
				return nil, err
			}
			if condition != nil {
				boolean, ok := condition.(bool)
				if !ok {
					return nil, expressionTypeError()
				}
				if boolean {
					return eval(i + 1)
				}
			}
		}
		return eval(len(arguments) - 1)
	}
	if operator == "FUNCTION:coalesce" {
		for i := range arguments {
			value, err := eval(i)
			if err != nil {
				return nil, err
			}
			if value != nil {
				return value, nil
			}
		}
		return nil, nil
	}
	values := make([]any, len(arguments))
	for i := range arguments {
		value, err := eval(i)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	if strings.HasPrefix(operator, "FUNCTION:") {
		name := strings.TrimPrefix(operator, "FUNCTION:")
		if isTemporalFunction(name) {
			state, _ := row[evaluationStateKey].(*expressionEvaluationState)
			if state != nil {
				return evaluateTemporalFunction(name, values, state.now)
			}
		}
		return evaluateScalarFunction(strings.TrimPrefix(operator, "FUNCTION:"), values)
	}
	switch operator {
	case "LIST":
		return values, nil
	case "IS NULL":
		return values[0] == nil, nil
	case "IS NOT NULL":
		return values[0] != nil, nil
	case "NOT":
		if values[0] == nil {
			return nil, nil
		}
		value, ok := values[0].(bool)
		if !ok {
			return nil, expressionTypeError()
		}
		return !value, nil
	case "AND", "OR", "XOR":
		var a, b bool
		var ok bool
		if values[0] != nil {
			a, ok = values[0].(bool)
			if !ok {
				return nil, expressionTypeError()
			}
		}
		if values[1] != nil {
			b, ok = values[1].(bool)
			if !ok {
				return nil, expressionTypeError()
			}
		}
		if operator == "AND" {
			if values[0] == false || values[1] == false {
				return false, nil
			}
			if values[0] == nil || values[1] == nil {
				return nil, nil
			}
			return a && b, nil
		}
		if operator == "OR" {
			if values[0] == true || values[1] == true {
				return true, nil
			}
			if values[0] == nil || values[1] == nil {
				return nil, nil
			}
			return a || b, nil
		}
		if values[0] == nil || values[1] == nil {
			return nil, nil
		}
		return a != b, nil
	case "=", "<>":
		equal, err := expressionEquality(values[0], values[1])
		if err != nil || equal == nil || operator == "=" {
			return equal, err
		}
		return !equal.(bool), nil
	case "HAS_LABEL":
		if values[0] == nil {
			return nil, nil
		}
		node, ok := values[0].(interfaces.CypherObjectValue)
		if !ok {
			return nil, expressionTypeError()
		}
		label, ok := values[1].(string)
		if !ok {
			return nil, expressionTypeError()
		}
		return node.ObjectTypeID == label, nil
	case "IN":
		if values[1] == nil {
			return nil, nil
		}
		list, ok := values[1].([]any)
		if !ok {
			return nil, expressionTypeError()
		}
		unknown := false
		for _, item := range list {
			equal, err := expressionEquality(values[0], item)
			if err != nil {
				return nil, err
			}
			if equal == true {
				return true, nil
			}
			unknown = unknown || equal == nil
		}
		if unknown {
			return nil, nil
		}
		return false, nil
	case "INDEX", "SLICE":
		if _, ok := values[0].(interfaces.CypherRelationValue); ok && operator == "INDEX" && expression.EmptyRelationProperty {
			return nil, nil
		}
		if object, ok := values[0].(interfaces.CypherObjectValue); ok && operator == "INDEX" && expression.MissingObjectTypes[object.ObjectTypeID] {
			return nil, nil
		}
		return evaluateListAccess(operator, values)
	}
	for _, value := range values {
		if value == nil {
			return nil, nil
		}
	}
	if result, handled, err := evaluateTemporalArithmetic(operator, values); handled {
		return result, err
	}
	switch operator {
	case "<", ">", "<=", ">=":
		if expressionNumeric(values[0]) && expressionNumeric(values[1]) && (expressionNaN(values[0]) || expressionNaN(values[1])) {
			return false, nil
		}
		order, err := expressionCompare(values[0], values[1])
		if errors.Is(err, errIncomparable) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		switch operator {
		case "<":
			return order < 0, nil
		case ">":
			return order > 0, nil
		case "<=":
			return order <= 0, nil
		default:
			return order >= 0, nil
		}
	case "STARTS WITH", "ENDS WITH", "CONTAINS":
		a, ok := values[0].(string)
		if !ok {
			return nil, nil
		}
		b, ok := values[1].(string)
		if !ok {
			return nil, nil
		}
		if operator == "STARTS WITH" {
			return strings.HasPrefix(a, b), nil
		}
		if operator == "ENDS WITH" {
			return strings.HasSuffix(a, b), nil
		}
		return strings.Contains(a, b), nil
	case "+":
		if left, ok := values[0].([]any); ok {
			result := append([]any{}, left...)
			if right, ok := values[1].([]any); ok {
				if len(result)+len(right) > graphMatchBudget {
					return nil, errGraphBudget
				}
				result = append(result, right...)
			} else {
				result = append(result, values[1])
			}
			return result, nil
		}
		if right, ok := values[1].([]any); ok {
			return append([]any{values[0]}, right...), nil
		}
		if a, ok := values[0].(string); ok {
			if b, ok := values[1].(string); ok {
				if len(a) > graphPayloadBudget-len(b) {
					return nil, errGraphBudget
				}
				return a + b, nil
			}
			return nil, expressionTypeError()
		}
	}
	return evaluateArithmetic(operator, values)
}

func evaluateArithmetic(operator string, values []any) (any, error) {
	for _, value := range values {
		if number, ok := value.(float64); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
			x, ok := expressionFloat(values[0])
			if !ok {
				return nil, expressionTypeError()
			}
			y := float64(0)
			if len(values) > 1 {
				var ok bool
				y, ok = expressionFloat(values[1])
				if !ok {
					return nil, expressionTypeError()
				}
			}
			return evaluateFloatingArithmetic(operator, x, y)
		}
	}
	a, integerA, ok := expressionNumber(values[0])
	if !ok {
		return nil, expressionTypeError()
	}
	b := new(big.Rat)
	integerB := true
	if len(values) > 1 {
		b, integerB, ok = expressionNumber(values[1])
		if !ok {
			return nil, expressionTypeError()
		}
	}
	if integerA && integerB && operator != "^" {
		if !a.Num().IsInt64() || !b.Num().IsInt64() {
			return nil, expressionArithmeticError("integer operand is out of range")
		}
		x, y := new(big.Int).Set(a.Num()), b.Num()
		result := new(big.Int)
		switch operator {
		case "+":
			result.Add(x, y)
		case "-":
			result.Sub(x, y)
		case "*":
			result.Mul(x, y)
		case "/":
			if y.Sign() == 0 {
				return nil, expressionArithmeticError("division by zero")
			}
			result.Quo(x, y)
		case "%":
			if y.Sign() == 0 {
				return nil, expressionArithmeticError("division by zero")
			}
			result.Rem(x, y)
		case "NEGATE":
			result.Neg(x)
		case "POSITIVE":
			result.Set(x)
		default:
			return nil, expressionTypeError()
		}
		if !result.IsInt64() {
			return nil, expressionArithmeticError("integer overflow")
		}
		return result.Int64(), nil
	}
	x, _ := a.Float64()
	y, _ := b.Float64()
	return evaluateFloatingArithmetic(operator, x, y)
}

func evaluateFloatingArithmetic(operator string, x, y float64) (any, error) {
	var result float64
	switch operator {
	case "+":
		result = x + y
	case "-":
		result = x - y
	case "*":
		result = x * y
	case "/":
		result = x / y
	case "%":
		result = math.Mod(x, y)
	case "^":
		result = math.Pow(x, y)
	case "NEGATE":
		result = -x
	case "POSITIVE":
		result = x
	default:
		return nil, expressionTypeError()
	}
	return result, nil
}
func evaluateListAccess(operator string, values []any) (any, error) {
	if values[0] == nil {
		return nil, nil
	}
	if operator == "INDEX" {
		if value, ok := values[0].(temporalValue); ok {
			key, ok := values[1].(string)
			if !ok {
				return nil, expressionTypeError()
			}
			return value.field(key)
		}
		if values[1] == nil {
			return nil, nil
		}
		if mapping, ok := values[0].(map[string]any); ok {
			key, ok := values[1].(string)
			if !ok {
				return nil, expressionTypeError()
			}
			return mapping[key], nil
		}
		if object, ok := values[0].(interfaces.CypherObjectValue); ok {
			key, ok := values[1].(string)
			if !ok {
				return nil, expressionTypeError()
			}
			if value, exists := object.EvaluationProperties[key]; exists {
				return value, nil
			}
			// Unplanned property reads cannot bypass scope-aware authorization.
			return nil, expressionTypeError()
		}
		list, ok := values[0].([]any)
		if !ok {
			return nil, expressionTypeError()
		}
		index, err := expressionInteger(values[1])
		if err != nil {
			return nil, err
		}
		if index < 0 {
			index += int64(len(list))
		}
		if index < 0 || index >= int64(len(list)) {
			return nil, nil
		}
		return list[index], nil
	}
	list, ok := values[0].([]any)
	if !ok {
		return nil, expressionTypeError()
	}
	start, end := int64(0), int64(len(list))
	var err error
	if values[1] == nil || values[2] == nil {
		return nil, nil
	}
	if _, omitted := values[1].(omittedSliceBound); !omitted {
		start, err = expressionInteger(values[1])
		if err != nil {
			return nil, err
		}
		if start < 0 {
			start += int64(len(list))
		}
	}
	if _, omitted := values[2].(omittedSliceBound); !omitted {
		end, err = expressionInteger(values[2])
		if err != nil {
			return nil, err
		}
		if end < 0 {
			end += int64(len(list))
		}
	}
	start = max(0, min(start, int64(len(list))))
	end = max(0, min(end, int64(len(list))))
	if start > end {
		return []any{}, nil
	}
	return append([]any{}, list[start:end]...), nil
}

func aggregateExpressionValues(function string, values []any) (any, error) {
	if function == "percentilecont" || function == "percentiledisc" {
		return aggregatePercentile(function, values)
	}
	if function == "stdev" || function == "stdevp" {
		return aggregateStandardDeviation(function, values)
	}
	if len(values) > 0 && (function == "sum" || function == "avg") {
		if first, ok := values[0].(temporalValue); ok && first.kind == "duration" {
			total := temporalValue{kind: "duration"}
			for _, item := range values {
				value, ok := item.(temporalValue)
				if !ok {
					return nil, expressionTypeError()
				}
				var err error
				total, err = addTemporalDuration(total, value, 1)
				if err != nil {
					return nil, err
				}
			}
			if function == "avg" {
				value, _, err := evaluateTemporalArithmetic("/", []any{total, int64(len(values))})
				return value, err
			}
			return total, nil
		}
	}

	if function == "collect" {
		return values, nil
	}
	if function == "count" {
		return int64(len(values)), nil
	}
	if len(values) == 0 {
		if function == "sum" {
			return int64(0), nil
		}
		return nil, nil
	}
	if function == "min" || function == "max" {
		best := values[0]
		for _, value := range values[1:] {
			comparison, err := expressionOrder(value, best)
			if err != nil {
				return nil, err
			}
			if function == "min" && comparison < 0 || function == "max" && comparison > 0 {
				best = value
			}
		}
		return best, nil
	}
	if function == "avg" {
		total := new(big.Rat)
		for _, value := range values {
			number, _, ok := expressionNumber(value)
			if !ok {
				return nil, expressionTypeError()
			}
			total.Add(total, number)
		}
		total.Quo(total, new(big.Rat).SetInt64(int64(len(values))))
		mean, _ := total.Float64()
		return mean, nil
	}
	var total any = int64(0)
	var err error
	for _, value := range values {
		total, err = evaluateArithmetic("+", []any{total, value})
		if err != nil {
			return nil, err
		}
	}
	if function == "sum" {
		return total, nil
	}
	return nil, expressionTypeError()
}

func sortExpressionRows(rows []map[string]any, sources []map[string]any, keys [][]any, orders []GraphOrder) error {
	indices := make([]int, len(rows))
	for i := range indices {
		indices[i] = i
	}
	var failure error
	sort.SliceStable(indices, func(i, j int) bool {
		for n, order := range orders {
			comparison, err := expressionOrder(keys[indices[i]][n], keys[indices[j]][n])
			if err != nil {
				failure = err
				return false
			}
			if comparison != 0 {
				if order.Descending {
					return comparison > 0
				}
				return comparison < 0
			}
		}
		return false
	})
	if failure != nil {
		return failure
	}
	copyRows := append([]map[string]any(nil), rows...)
	copySources := append([]map[string]any(nil), sources...)
	for i, index := range indices {
		rows[i] = copyRows[index]
		sources[i] = copySources[index]
	}
	return nil
}

func needsRuntimeParameter(expression ValueExpression, parameters map[string]any, parent string) bool {
	switch typed := expression.(type) {
	case ParameterExpression:
		value, exists := parameters[typed.Ref.Name]
		if !exists {
			return false
		}
		if value == nil {
			return true
		}
		kind := reflect.ValueOf(value).Kind()
		if kind == reflect.Map {
			return true
		}
		if kind == reflect.Slice || kind == reflect.Array {
			if parent != "IN" {
				return true
			}
			rv := reflect.ValueOf(value)
			if rv.Len() == 0 || rv.Len() > 500 {
				return true
			}
			category := ""
			for i := 0; i < rv.Len(); i++ {
				value := rv.Index(i).Interface()
				current := expressionValueType(value)
				if _, _, ok := expressionNumber(value); ok {
					current = "number"
				}
				if current == "null" || current == "map" || current == "list" || category != "" && category != current {
					return true
				}
				category = current
			}
		}
	case OperationExpression:
		for _, argument := range typed.Arguments {
			if needsRuntimeParameter(argument, parameters, typed.Operator) {
				return true
			}
		}
	case MapExpression:
		for _, field := range typed.Fields {
			if needsRuntimeParameter(field.Value, parameters, "") {
				return true
			}
		}
	}
	return false
}
