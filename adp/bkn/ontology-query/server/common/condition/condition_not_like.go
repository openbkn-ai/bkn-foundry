// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	dtype "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/data_type"
)

type NotLikeCond struct {
	mCfg             *CondCfg
	mValue           string
	mFilterFieldName string
}

func NewNotLikeCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	_, ok := fieldsMap[cfg.Name]
	if !ok {
		return nil, validationError(ctx, "ConditionFieldNotFound", map[string]any{"field": cfg.Name})
	}

	if !dtype.DataType_IsString(cfg.NameField.Type) &&
		dtype.SimpleTypeMapping[cfg.NameField.Type] != dtype.SimpleChar {
		return nil, fmt.Errorf("condition [not_like] left field is not a string field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [not_like] right value is not a string value: %v", cfg.Value)
	}
	if err := common.ValidateSQLLikePattern(val); err != nil {
		return nil, fmt.Errorf("condition [not_like]: %w", err)
	}

	return &NotLikeCond{
		mCfg:             cfg,
		mValue:           val,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *NotLikeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	pattern, err := common.SQLLikeToWildcard(cond.mValue)
	if err != nil {
		return "", err
	}
	v := fmt.Sprintf("%q", pattern)

	dslStr := fmt.Sprintf(`
					{
						"bool": {
						"must": [{"exists": {"field": "%s"}}],
							"must_not": [
								{
								"wildcard": {
										"%s": %v
									}
								}
							]
						}
					}`, cond.mFilterFieldName, cond.mFilterFieldName, v)

	return dslStr, nil
}

func (cond *NotLikeCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" NOT LIKE '%s' ESCAPE '!'`, cond.mFilterFieldName, escapeSQLLikeLiteral(cond.mValue))

	return sqlStr, nil
}

func rewriteNotLikeCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	value, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [not_like] right value is not a string value: %v", cfg.Value)
	}
	if err := common.ValidateSQLLikePattern(value); err != nil {
		return nil, fmt.Errorf("property '%s': %w", cfg.Name, err)
	}

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "not_like", "field": cfg.Name})
	}
	return &CondCfg{
		Name:      cfg.NameField.MappedField.Name,
		Operation: cfg.Operation,
		Value:     cfg.Value,
	}, nil
}
