// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var errIncomparable = errors.New("expression values are incomparable")

func expressionNaN(value any) bool { number, ok := value.(float64); return ok && math.IsNaN(number) }
func expressionNumeric(value any) bool {
	if _, ok := value.(float64); ok {
		return true
	}
	_, _, ok := expressionNumber(value)
	return ok
}
func expressionFloat(value any) (float64, bool) {
	if number, ok := value.(float64); ok {
		return number, true
	}
	number, _, ok := expressionNumber(value)
	if !ok {
		return 0, false
	}
	f, _ := number.Float64()
	return f, true
}

func expressionNumericOrder(left, right any) (int, error) {
	a, aOK := expressionFloat(left)
	b, bOK := expressionFloat(right)
	if !aOK || !bOK {
		return 0, errIncomparable
	}
	if math.IsNaN(a) {
		if math.IsNaN(b) {
			return 0, nil
		}
		return 1, nil
	}
	if math.IsNaN(b) {
		return -1, nil
	}
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		if a < b {
			return -1, nil
		}
		if a > b {
			return 1, nil
		}
		return 0, nil
	}
	x, _, _ := expressionNumber(left)
	y, _, _ := expressionNumber(right)
	return x.Cmp(y), nil
}

// ORDER BY and min/max have a total type order; relational predicates use a
// partial comparison and yield null for incomparable types instead.
func expressionTypeRank(value any) int {
	if value == nil {
		return 8
	}
	if expressionNumeric(value) {
		return 7
	}
	switch value.(type) {
	case map[string]any:
		return 0
	case interfaces.CypherObjectValue:
		return 1
	case interfaces.CypherRelationValue:
		return 2
	case []any:
		return 3
	case interfaces.CypherPathValue:
		return 4
	case string:
		return 5
	case bool:
		return 6
	default:
		return 9
	}
}
func expressionOrder(left, right any) (int, error) {
	a, b := expressionTypeRank(left), expressionTypeRank(right)
	if a < b {
		return -1, nil
	}
	if a > b {
		return 1, nil
	}
	if left == nil {
		return 0, nil
	}
	if a == 7 {
		return expressionNumericOrder(left, right)
	}
	switch x := left.(type) {
	case []any:
		y := right.([]any)
		for i := 0; i < len(x) && i < len(y); i++ {
			order, err := expressionOrder(x[i], y[i])
			if err != nil || order != 0 {
				return order, err
			}
		}
		if len(x) < len(y) {
			return -1, nil
		}
		if len(x) > len(y) {
			return 1, nil
		}
		return 0, nil
	case map[string]any:
		y := right.(map[string]any)
		xKeys, yKeys := sortedGraphKeys(x), sortedGraphKeys(y)
		for i := 0; i < len(xKeys) && i < len(yKeys); i++ {
			if order := strings.Compare(xKeys[i], yKeys[i]); order != 0 {
				return order, nil
			}
			order, err := expressionOrder(x[xKeys[i]], y[yKeys[i]])
			if err != nil || order != 0 {
				return order, err
			}
		}
		if len(xKeys) < len(yKeys) {
			return -1, nil
		}
		if len(xKeys) > len(yKeys) {
			return 1, nil
		}
		return 0, nil
	case interfaces.CypherObjectValue, interfaces.CypherRelationValue, interfaces.CypherPathValue:
		xKey, err := expressionKey(left)
		if err != nil {
			return 0, err
		}
		y, err := expressionKey(right)
		if err != nil {
			return 0, err
		}
		return strings.Compare(xKey, y), nil
	default:
		return expressionCompare(left, right)
	}
}

func marshalExpressionValue(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	var failure *json.UnsupportedValueError
	if errors.As(err, &failure) && (strings.Contains(failure.Str, "NaN") || strings.Contains(failure.Str, "Inf")) {
		return nil, expressionArithmeticError("non-finite output cannot be serialized")
	}
	return raw, err
}
