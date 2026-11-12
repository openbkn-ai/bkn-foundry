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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	dtype "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/data_type"
)

type LikeCond struct {
	mCfg             *CondCfg
	mValue           string
	mFilterFieldName string
}

func NewLikeCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	_, ok := fieldsMap[cfg.Name]
	if !ok {
		return nil, validationError(ctx, "ConditionFieldNotFound", map[string]any{"field": cfg.Name})
	}

	if !dtype.DataType_IsString(cfg.NameField.Type) &&
		dtype.SimpleTypeMapping[cfg.NameField.Type] != dtype.SimpleChar {
		return nil, fmt.Errorf("condition [like] left field is not a string field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [like] right value is not a string value: %v", cfg.Value)
	}
	if err := common.ValidateSQLLikePattern(val); err != nil {
		return nil, fmt.Errorf("condition [like]: %w", err)
	}

	return &LikeCond{
		mCfg:             cfg,
		mValue:           val,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *LikeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	pattern, err := common.SQLLikeToWildcard(cond.mValue)
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

func rewriteLikeCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	value, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [like] right value is not a string value: %v", cfg.Value)
	}
	if err := common.ValidateSQLLikePattern(value); err != nil {
		return nil, fmt.Errorf("property '%s': %w", cfg.Name, err)
	}

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "like", "field": cfg.Name})
	}
	return &CondCfg{
		Name:      cfg.NameField.MappedField.Name,
		Operation: cfg.Operation,
		Value:     cfg.Value,
	}, nil
}

func escapeSQLLikeLiteral(pattern string) string {
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
	return strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(result.String())
}
