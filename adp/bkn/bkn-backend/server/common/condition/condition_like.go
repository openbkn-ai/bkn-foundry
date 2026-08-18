// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
	"strings"

	dtype "bkn-backend/interfaces/data_type"
)

type LikeCond struct {
	mCfg             *CondCfg
	mValue           string
	mFilterFieldName string
}

func NewLikeCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*ViewField) (Condition, error) {
	if !dtype.DataType_IsString(cfg.NameField.Type) &&
		dtype.SimpleTypeMapping[cfg.NameField.Type] != dtype.SimpleChar {
		return nil, fmt.Errorf("condition [like] left field is not a string field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	if cfg.ValueFrom != ValueFrom_Const {
		return nil, fmt.Errorf("condition [like] does not support value_from type '%s'", cfg.ValueFrom)
	}

	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [like] right value is not a string value: %v", cfg.Value)
	}
	literal, err := ParseLikeValue(OperationLike, val)
	if err != nil {
		return nil, err
	}

	return &LikeCond{
		mCfg:             cfg,
		mValue:           literal,
		mFilterFieldName: getFilterFieldName(cfg.Field, fieldsMap, false),
	}, nil
}

func (cond *LikeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, words []string) ([]*VectorResp, error)) (string, error) {
	v := fmt.Sprintf("%q", LikeContainsPattern(cond.mValue))
	dslStr := fmt.Sprintf(`
					{
						"wildcard": {
							"%s": %v
						}
					}`, cond.mFilterFieldName, v)

	return dslStr, nil
}

func (cond *LikeCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" LIKE '%s'`, cond.mFilterFieldName, "%"+Special.Replace(cond.mValue)+"%")

	return sqlStr, nil
}

// convertLikeCondToDatasetFilterCondition converts LikeCond to dataset filter condition format
func convertLikeCondToDatasetFilterCondition(cfg *CondCfg) (map[string]any, error) {
	// This path never builds a LikeCond and forwards the value straight to vega, so the contract
	// has to be checked here as well: naming the object type property beats waiting for vega to
	// report a resource field name the caller never mentioned.
	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [like] right value is not a string value: %v", cfg.Value)
	}
	if _, err := ParseLikeValue(OperationLike, val); err != nil {
		return nil, fmt.Errorf("property '%s': %w", cfg.Field, err)
	}

	return map[string]any{
		"field":      cfg.Field,
		"operation":  "like",
		// Forward the value with its escapes intact: vega parses it again, and unescaping early
		// would turn a literal % into a wildcard
		"value":      cfg.Value,
		"value_from": "const",
	}, nil
}

// ParseLikeValue validates a like / not_like value and returns the literal substring to match.
//
// like matches a literal substring; it is not a SQL LIKE pattern. The paths used to disagree
// about %: the DSL builder dropped the raw value into a .*value.* regexp, the SQL builder did
// not wrap the value at all, and vega's SQL connectors downstream escaped % into a literal — so
// a SQL-style "%foo%" returned an empty set on every path without ever raising an error. An
// unescaped % is therefore rejected and pointed at regex; write \% to match the character.
//
// _ is not rejected: every path already treated it literally (Special.Replace escapes it to \_,
// and it is not a metacharacter inside the DSL .* regexp), so it never had the silent-empty-set
// failure that justifies rejecting %, and underscores in search terms are common.
// Same rules as filter_condition.ParseLikeValue in vega.
func ParseLikeValue(operation, value string) (string, error) {
	var literal strings.Builder
	escaped := false

	for _, r := range value {
		switch {
		case escaped:
			// Only % _ \ carry an escape meaning; anything else keeps the backslash
			if r != '%' && r != '_' && r != '\\' {
				literal.WriteRune('\\')
			}
			literal.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '%':
			return "", fmt.Errorf(
				"condition [%s] value is matched as a literal substring, so the wildcard '%%' is not supported; "+
					"use operation [regex] for pattern matching, or escape it as '\\%%' to match the character itself",
				operation)
		default:
			literal.WriteRune(r)
		}
	}
	if escaped {
		literal.WriteRune('\\')
	}

	return literal.String(), nil
}

// LikeContainsPattern turns a literal substring into an OpenSearch wildcard pattern; only * ? \ need escaping.
func LikeContainsPattern(literal string) string {
	var escaped strings.Builder
	for _, r := range literal {
		if r == '*' || r == '?' || r == '\\' {
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(r)
	}
	return "*" + escaped.String() + "*"
}
