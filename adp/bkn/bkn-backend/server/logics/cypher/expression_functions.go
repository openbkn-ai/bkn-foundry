// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type expressionFunction struct {
	Min, Max int
	Type     string
}

var expressionFunctions = map[string]expressionFunction{
	"coalesce": {1, 128, "any"}, "head": {1, 1, "any"}, "last": {1, 1, "any"}, "tail": {1, 1, "list"}, "size": {1, 1, "integer"}, "keys": {1, 1, "list"}, "properties": {1, 1, "map"}, "labels": {1, 1, "list"}, "type": {1, 1, "string"}, "startnode": {1, 1, "object"}, "endnode": {1, 1, "object"},
	"range": {2, 3, "list"}, "reverse": {1, 1, "any"}, "tolower": {1, 1, "string"}, "toupper": {1, 1, "string"}, "trim": {1, 1, "string"}, "ltrim": {1, 1, "string"}, "rtrim": {1, 1, "string"}, "replace": {3, 3, "string"}, "split": {2, 2, "list"}, "substring": {2, 3, "string"}, "left": {2, 2, "string"}, "right": {2, 2, "string"},
	"tostring": {1, 1, "string"}, "tointeger": {1, 1, "integer"}, "tofloat": {1, 1, "float"}, "toboolean": {1, 1, "boolean"}, "exists": {1, 1, "boolean"},
	"abs": {1, 1, "any"}, "ceil": {1, 1, "float"}, "floor": {1, 1, "float"}, "round": {1, 1, "float"}, "sign": {1, 1, "integer"}, "sqrt": {1, 1, "float"}, "log": {1, 1, "float"}, "log10": {1, 1, "float"}, "exp": {1, 1, "float"}, "sin": {1, 1, "float"}, "cos": {1, 1, "float"}, "tan": {1, 1, "float"}, "asin": {1, 1, "float"}, "acos": {1, 1, "float"}, "atan": {1, 1, "float"}, "atan2": {2, 2, "float"}, "degrees": {1, 1, "float"}, "radians": {1, 1, "float"}, "pi": {0, 0, "float"}, "e": {0, 0, "float"},
}

func validateExpressionOperation(operator string, arity int) error {
	if strings.HasPrefix(operator, "FUNCTION:") {
		name := strings.TrimPrefix(operator, "FUNCTION:")
		function, ok := expressionFunctions[name]
		if !ok {
			return fmt.Errorf("function %q is not supported", name)
		}
		if arity < function.Min || arity > function.Max {
			return fmt.Errorf("function %q requires %d..%d arguments", name, function.Min, function.Max)
		}
		return nil
	}
	switch operator {
	case "LIST":
		return nil
	case "OMITTED_BOUND":
		if arity == 0 {
			return nil
		}
	case "CASE":
		if arity >= 3 && arity%2 == 1 {
			return nil
		}
	case "INDEX", "+", "-", "*", "/", "%", "^", "=", "<>", "<", ">", "<=", ">=", "AND", "OR", "XOR", "IN", "STARTS WITH", "ENDS WITH", "CONTAINS":
		if arity == 2 {
			return nil
		}
	case "NOT", "NEGATE", "POSITIVE", "IS NULL", "IS NOT NULL":
		if arity == 1 {
			return nil
		}
	case "SLICE":
		if arity == 3 {
			return nil
		}
	}
	return fmt.Errorf("unsupported expression operation %q", operator)
}
func operationResultType(operator string) string {
	if strings.HasPrefix(operator, "FUNCTION:") {
		return expressionFunctions[strings.TrimPrefix(operator, "FUNCTION:")].Type
	}
	switch operator {
	case "LIST", "SLICE":
		return "list"
	case "=", "<>", "<", ">", "<=", ">=", "AND", "OR", "XOR", "NOT", "IN", "IS NULL", "IS NOT NULL", "STARTS WITH", "ENDS WITH", "CONTAINS":
		return "boolean"
	default:
		return "any"
	}
}
func evaluateScalarFunction(name string, arguments []any) (any, error) {
	if name == "pi" {
		return math.Pi, nil
	}
	if name == "e" {
		return math.E, nil
	}
	if name == "exists" {
		return arguments[0] != nil, nil
	}
	for _, value := range arguments {
		if value == nil {
			return nil, nil
		}
	}
	value := arguments[0]
	switch name {
	case "size":
		if text, ok := value.(string); ok {
			return int64(utf8.RuneCountInString(text)), nil
		}
		if list, ok := value.([]any); ok {
			return int64(len(list)), nil
		}
		return nil, expressionTypeError()
	case "head", "last", "tail":
		list, ok := value.([]any)
		if !ok {
			return nil, expressionTypeError()
		}
		if name == "tail" {
			if len(list) == 0 {
				return []any{}, nil
			}
			return append([]any{}, list[1:]...), nil
		}
		if len(list) == 0 {
			return nil, nil
		}
		if name == "head" {
			return list[0], nil
		}
		return list[len(list)-1], nil
	case "keys":
		var keys []string
		switch typed := value.(type) {
		case map[string]any:
			keys = sortedGraphKeys(typed)
		case interfaces.CypherObjectValue:
			keys = sortedGraphKeys(typed.Properties)
		default:
			return nil, expressionTypeError()
		}
		result := make([]any, len(keys))
		for i, key := range keys {
			result[i] = key
		}
		return result, nil
	case "properties":
		switch typed := value.(type) {
		case map[string]any:
			return typed, nil
		case interfaces.CypherObjectValue:
			if typed.Properties == nil {
				return map[string]any{}, nil
			}
			return typed.Properties, nil
		default:
			return nil, expressionTypeError()
		}
	case "labels":
		if object, ok := value.(interfaces.CypherObjectValue); ok {
			return []any{object.ObjectTypeID}, nil
		}
		return nil, expressionTypeError()
	case "type":
		if relation, ok := value.(interfaces.CypherRelationValue); ok {
			return relation.RelationTypeID, nil
		}
		return nil, expressionTypeError()
	case "startnode", "endnode":
		if relation, ok := value.(interfaces.CypherRelationValue); ok {
			if name == "startnode" {
				return relation.Source, nil
			}
			return relation.Target, nil
		}
		return nil, expressionTypeError()
	case "range":
		start, err := expressionInteger(value)
		if err != nil {
			return nil, err
		}
		end, err := expressionInteger(arguments[1])
		if err != nil {
			return nil, err
		}
		step := int64(1)
		if len(arguments) == 3 {
			step, err = expressionInteger(arguments[2])
			if err != nil {
				return nil, err
			}
		}
		if step == 0 {
			return nil, expressionArithmeticError("range step must not be zero")
		}
		result := []any{}
		for current := start; (step > 0 && current <= end) || (step < 0 && current >= end); {
			if len(result) >= graphMatchBudget {
				return nil, errGraphBudget
			}
			result = append(result, current)
			next, err := evaluateArithmetic("+", []any{current, step})
			if err != nil {
				break
			}
			current = next.(int64)
		}
		return result, nil
	case "reverse":
		if list, ok := value.([]any); ok {
			result := append([]any{}, list...)
			for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
				result[i], result[j] = result[j], result[i]
			}
			return result, nil
		}
	case "tointeger", "tofloat":
		if number, _, ok := expressionNumber(value); ok {
			if name == "tointeger" {
				integer := new(big.Int).Quo(number.Num(), number.Denom())
				if !integer.IsInt64() {
					return nil, nil
				}
				return integer.Int64(), nil
			}
			f, _ := number.Float64()
			return f, nil
		}
		if text, ok := value.(string); ok {
			if name == "tointeger" {
				number, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
				if err != nil {
					return nil, nil
				}
				return number, nil
			}
			number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return nil, nil
			}
			return number, nil
		}
		return nil, expressionTypeError()
	case "toboolean":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
		if text, ok := value.(string); ok {
			switch strings.ToLower(strings.TrimSpace(text)) {
			case "true":
				return true, nil
			case "false":
				return false, nil
			default:
				return nil, nil
			}
		}
		return nil, expressionTypeError()
	case "tostring":
		switch typed := value.(type) {
		case string:
			return typed, nil
		case bool:
			return strconv.FormatBool(typed), nil
		default:
			if number, integer, ok := expressionNumber(value); ok {
				if integer {
					return number.Num().String(), nil
				}
				f, _ := number.Float64()
				return strconv.FormatFloat(f, 'g', -1, 64), nil
			}
			return nil, expressionTypeError()
		}
	case "abs", "sign", "ceil", "floor", "round", "sqrt", "log", "log10", "exp", "sin", "cos", "tan", "asin", "acos", "atan", "atan2", "degrees", "radians":
		number, integer, ok := expressionNumber(value)
		if !ok {
			return nil, expressionTypeError()
		}
		f, _ := number.Float64()
		if name == "abs" && integer {
			if number.Sign() < 0 {
				return evaluateArithmetic("NEGATE", []any{value})
			}
			return value, nil
		}
		if name == "sign" {
			return int64(number.Sign()), nil
		}
		var result float64
		switch name {
		case "abs":
			result = math.Abs(f)
		case "ceil":
			result = math.Ceil(f)
		case "floor":
			result = math.Floor(f)
		case "round":
			result = math.Floor(f + 0.5)
		case "sqrt":
			result = math.Sqrt(f)
		case "log":
			result = math.Log(f)
		case "log10":
			result = math.Log10(f)
		case "exp":
			result = math.Exp(f)
		case "sin":
			result = math.Sin(f)
		case "cos":
			result = math.Cos(f)
		case "tan":
			result = math.Tan(f)
		case "asin":
			result = math.Asin(f)
		case "acos":
			result = math.Acos(f)
		case "atan":
			result = math.Atan(f)
		case "atan2":
			other, _, ok := expressionNumber(arguments[1])
			if !ok {
				return nil, expressionTypeError()
			}
			y, _ := other.Float64()
			result = math.Atan2(f, y)
		case "degrees":
			result = f * 180 / math.Pi
		case "radians":
			result = f * math.Pi / 180
		}
		if math.IsNaN(result) || math.IsInf(result, 0) {
			return nil, expressionArithmeticError("non-finite function result cannot be serialized")
		}
		return result, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, expressionTypeError()
	}
	runes := []rune(text)
	switch name {
	case "tolower":
		return strings.ToLower(text), nil
	case "toupper":
		return strings.ToUpper(text), nil
	case "trim":
		return strings.TrimSpace(text), nil
	case "ltrim":
		return strings.TrimLeftFunc(text, unicode.IsSpace), nil
	case "rtrim":
		return strings.TrimRightFunc(text, unicode.IsSpace), nil
	case "reverse":
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes), nil
	case "replace":
		old, ok := arguments[1].(string)
		if !ok {
			return nil, expressionTypeError()
		}
		replacement, ok := arguments[2].(string)
		if !ok {
			return nil, expressionTypeError()
		}
		if old == "" {
			return text, nil
		}
		if len(replacement) > len(old) && len(text) > 0 && len(text)/max(1, len(old)) > graphPayloadBudget/max(1, len(replacement)) {
			return nil, errGraphBudget
		}
		return strings.ReplaceAll(text, old, replacement), nil
	case "split":
		separator, ok := arguments[1].(string)
		if !ok {
			return nil, expressionTypeError()
		}
		parts := strings.Split(text, separator)
		if len(parts) > graphMatchBudget {
			return nil, errGraphBudget
		}
		result := make([]any, len(parts))
		for i, part := range parts {
			result[i] = part
		}
		return result, nil
	case "left", "right", "substring":
		index, err := expressionInteger(arguments[1])
		if err != nil {
			return nil, err
		}
		if index < 0 {
			return nil, expressionArithmeticError("string offset or length must be non-negative")
		}
		if name == "left" {
			return string(runes[:min(index, int64(len(runes)))]), nil
		}
		if name == "right" {
			return string(runes[max(0, int64(len(runes))-index):]), nil
		}
		start := min(index, int64(len(runes)))
		end := int64(len(runes))
		if len(arguments) == 3 {
			length, err := expressionInteger(arguments[2])
			if err != nil {
				return nil, err
			}
			if length < 0 {
				return nil, expressionArithmeticError("substring length must be non-negative")
			}
			end = start + min(length, int64(len(runes))-start)
		}
		return string(runes[start:end]), nil
	}
	return nil, expressionTypeError()
}

func validatePlannedExpression(expression *GraphExpression) error {
	allowed := func(index int, kinds ...string) bool {
		kind := expression.Arguments[index].Type.Kind
		if kind == "any" || kind == "null" {
			return true
		}
		for _, candidate := range kinds {
			if kind == candidate {
				return true
			}
		}
		return false
	}
	switch expression.Operation {
	case "AND", "OR", "XOR":
		if !allowed(0, "boolean") || !allowed(1, "boolean") {
			return expressionTypeError()
		}
	case "NOT":
		if !allowed(0, "boolean") {
			return expressionTypeError()
		}
	case "IN":
		if !allowed(1, "list") {
			return fmt.Errorf("IN requires a list")
		}
	case "STARTS WITH", "ENDS WITH", "CONTAINS":
		if !allowed(0, "string", "text", "keyword") || !allowed(1, "string", "text", "keyword") {
			return expressionTypeError()
		}
	case "CASE":
		for i := 0; i+1 < len(expression.Arguments); i += 2 {
			if !allowed(i, "boolean") {
				return expressionTypeError()
			}
		}
	}
	return nil
}
