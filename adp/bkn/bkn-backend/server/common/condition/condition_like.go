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

	dtype "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/data_type"
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
	if err := ValidateLikePattern(OperationLike, val); err != nil {
		return nil, err
	}

	return &LikeCond{
		mCfg:             cfg,
		mValue:           val,
		mFilterFieldName: getFilterFieldName(cfg.Field, fieldsMap, false),
	}, nil
}

func (cond *LikeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, words []string) ([]*VectorResp, error)) (string, error) {
	pattern, err := SQLLikeToWildcard(OperationLike, cond.mValue)
	if err != nil {
		return "", err
	}
	v := fmt.Sprintf("%q", pattern)
	dslStr := fmt.Sprintf(`
					{
						"wildcard": {
							"%s": %v
						}
					}`, cond.mFilterFieldName, v)

	return dslStr, nil
}

func (cond *LikeCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" LIKE '%s' ESCAPE '!'`, cond.mFilterFieldName, escapeSQLLikeLiteral(cond.mValue))

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
	if err := ValidateLikePattern(OperationLike, val); err != nil {
		return nil, fmt.Errorf("property '%s': %w", cfg.Field, err)
	}

	return map[string]any{
		"field":     cfg.Field,
		"operation": "like",
		// Forward the complete pattern, including escapes, to Vega.
		"value":      cfg.Value,
		"value_from": "const",
	}, nil
}

// ValidateLikePattern applies Vega's complete SQL LIKE pattern contract.
// A backslash escapes the following character; a dangling escape is invalid.
func ValidateLikePattern(operation, value string) error {
	escaped := false
	for _, r := range value {
		if escaped {
			escaped = false
		} else if r == '\\' {
			escaped = true
		}
	}
	if escaped {
		return fmt.Errorf("condition [%s] pattern ends with an incomplete escape", operation)
	}
	return nil
}

// SQLLikeToWildcard converts a complete SQL LIKE pattern to an index wildcard pattern.
func SQLLikeToWildcard(operation, pattern string) (string, error) {
	if err := ValidateLikePattern(operation, pattern); err != nil {
		return "", err
	}
	var result strings.Builder
	escaped := false
	for _, r := range pattern {
		if !escaped {
			switch r {
			case '\\':
				escaped = true
				continue
			case '%':
				result.WriteByte('*')
				continue
			case '_':
				result.WriteByte('?')
				continue
			}
		}
		if r == '*' || r == '?' || r == '\\' {
			result.WriteByte('\\')
		}
		result.WriteRune(r)
		escaped = false
	}
	return result.String(), nil
}

// SQLLikeToBangPattern preserves LIKE wildcards while translating backslash
// escapes to !, which the BKN SQL generator uses across database dialects.
func SQLLikeToBangPattern(pattern string) string {
	var result strings.Builder
	escaped := false
	for _, r := range pattern {
		if !escaped && r == '\\' {
			escaped = true
			continue
		}
		if escaped && (r == '%' || r == '_' || r == '!' || r == '[') ||
			!escaped && (r == '!' || r == '[') {
			result.WriteByte('!')
		}
		result.WriteRune(r)
		escaped = false
	}
	return result.String()
}

func escapeSQLLikeLiteral(pattern string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(SQLLikeToBangPattern(pattern))
}
