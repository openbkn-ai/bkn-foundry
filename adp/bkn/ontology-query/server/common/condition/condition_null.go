// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
)

type NullCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
}

func NewNullCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	return &NullCond{
		mCfg:             cfg,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

// Check whether the field value IS NULL. OpenSearch does not index null values by default,
// so IS NULL is equivalent to finding documents where the field has no indexed value. The query matches:
// 1. The document does not contain this field.
// 2. The field is explicitly set to null in JSON.
// 3. The field is an empty array.
func (cond *NullCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	dslStr := fmt.Sprintf(`
	{
		"bool": {
			"must_not": {
				"exists": {
					"field": "%s"
				}
			}
		}
	}`, cond.mFilterFieldName)

	return dslStr, nil
}

func (cond *NullCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" IS NULL`, cond.mFilterFieldName)
	return sqlStr, nil
}

func rewriteNullCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "null", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
