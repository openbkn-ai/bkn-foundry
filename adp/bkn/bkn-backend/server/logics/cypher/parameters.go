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
		return Literal{}, fmt.Errorf("is a %T; parameters must be a string, a number or a boolean", value)
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
