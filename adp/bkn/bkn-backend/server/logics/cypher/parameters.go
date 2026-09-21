// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"fmt"
	"math"
)

// MaxListParameterLength bounds how many values one list parameter may carry
// into IN. The binding is the semantic query descriptor rather than the SQL:
// every value adds a hash to it, and the descriptor is capped at
// maxSemanticDescriptorBytes. 500 values stay well inside that cap together
// with the rest of a query; a query that still overflows it is refused as too
// large.
const MaxListParameterLength = 500

// Parameters are values, never identifiers. A parameter can change which rows
// come back; it can never change which resource or column is read, because it
// is bound into a literal and escaped by the same rules as one written in the
// query. That is the whole reason to offer them: a caller with values to pass
// no longer has to build query text.
//
// literalFromParameter maps one supplied value onto the literal kinds the
// generator knows how to write. A type it cannot map is refused by name rather
// than rendered as whatever fmt makes of it.
func literalFromParameter(value any) (Literal, error) {
	switch typed := value.(type) {
	case string:
		return Literal{Kind: LiteralString, String: typed}, nil
	case bool:
		return Literal{Kind: LiteralBoolean, Boolean: typed}, nil
	case int:
		return Literal{Kind: LiteralInteger, Integer: int64(typed)}, nil
	case int64:
		return Literal{Kind: LiteralInteger, Integer: typed}, nil
	case json.Number:
		return literalFromJSONNumber(typed)
	case float64:
		// JSON has one number type, so an integer arrives as a float unless
		// the decoder was told otherwise. A value that is exactly an integer
		// is carried as one: writing 42 as 42.0 changes what an equality
		// against an integer column matches.
		if typed == math.Trunc(typed) && math.Abs(typed) <= math.MaxInt64 {
			return Literal{Kind: LiteralInteger, Integer: int64(typed)}, nil
		}
		return Literal{Kind: LiteralFloat, Float: typed}, nil
	case nil:
		return Literal{}, fmt.Errorf("is null; write IS NULL instead of comparing to a null parameter")
	default:
		return Literal{}, fmt.Errorf("is %s; parameters must be a string, a number or a boolean", describeParameterValue(value))
	}
}

func literalFromJSONNumber(number json.Number) (Literal, error) {
	if integer, err := number.Int64(); err == nil {
		return Literal{Kind: LiteralInteger, Integer: integer}, nil
	}
	float, err := number.Float64()
	if err != nil {
		return Literal{}, fmt.Errorf("is not a number this interface can carry")
	}
	return Literal{Kind: LiteralFloat, Float: float}, nil
}

// literalsFromListParameter maps a parameter used as the whole right side of
// IN onto literals. Each element goes through literalFromParameter, so it is
// bound and escaped exactly like a scalar parameter.
//
// The list has to be non-empty, at most MaxListParameterLength long, and hold
// scalars of one kind. Integers and floats count as one kind, numbers, as they
// do in a list literal such as [1, 2.5]: JSON does not tell them apart, and an
// integral float already arrives as an integer. Strings, numbers and booleans
// may not be mixed: such a list is almost always a mistake, and the column it
// is compared with has one type.
func literalsFromListParameter(value any) ([]Literal, error) {
	if value == nil {
		return nil, fmt.Errorf("is null; IN needs a list of values")
	}
	// Parameters arrive decoded from JSON, where a list is always []any.
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("is %s; IN $name needs the parameter to be a list of values", describeParameterValue(value))
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("is an empty list; IN over nothing matches nothing, so leave the condition out instead")
	}
	if len(list) > MaxListParameterLength {
		return nil, fmt.Errorf("has %d values; a list parameter may carry at most %d", len(list), MaxListParameterLength)
	}
	literals := make([]Literal, 0, len(list))
	var firstKind literalCategory
	for i, element := range list {
		if element == nil {
			return nil, fmt.Errorf("has null at index %d; a null in IN matches nothing and hides a mistake", i)
		}
		literal, err := literalFromParameter(element)
		if err != nil {
			return nil, fmt.Errorf("element %d %v", i, err)
		}
		kind := categoryOf(literal.Kind)
		if i == 0 {
			firstKind = kind
		} else if kind != firstKind {
			return nil, fmt.Errorf("mixes %s and %s values (index %d); a list parameter must hold one kind of value",
				firstKind, kind, i)
		}
		literals = append(literals, literal)
	}
	return literals, nil
}

// describeParameterValue names a decoded JSON value in the caller's terms, so
// an error never shows a Go type.
func describeParameterValue(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case int, int64, float64, json.Number:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	default:
		return "an unsupported value"
	}
}

// literalCategory groups literal kinds the way a list parameter's elements
// must agree: integers and floats are both numbers.
type literalCategory string

const (
	categoryString  literalCategory = "string"
	categoryNumber  literalCategory = "number"
	categoryBoolean literalCategory = "boolean"
)

func categoryOf(kind LiteralKind) literalCategory {
	switch kind {
	case LiteralInteger, LiteralFloat:
		return categoryNumber
	case LiteralBoolean:
		return categoryBoolean
	default:
		return categoryString
	}
}
