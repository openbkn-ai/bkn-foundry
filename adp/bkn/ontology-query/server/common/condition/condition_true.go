// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	dtype "ontology-query/interfaces/data_type"
)

type TrueCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
}

// The bool type is true.
func NewTrueCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	if cfg.NameField.Type != dtype.DATATYPE_BOOLEAN &&
		dtype.SimpleTypeMapping[cfg.NameField.Type] != dtype.SimpleBool {
		return nil, fmt.Errorf("condition [true] left field is not a boolean field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	return &TrueCond{
		mCfg:             cfg,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *TrueCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	dslStr := fmt.Sprintf(`
	{
		"term": {
			"%s": true
		}
	}`, cond.mFilterFieldName)

	return dslStr, nil
}

func (cond *TrueCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" = true`, cond.mFilterFieldName)
	return sqlStr, nil
}

func rewriteTrueCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "true", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
