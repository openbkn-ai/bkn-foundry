// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	"ontology-query/common"
)

type GteCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
}

func NewGteCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {

	if common.IsSlice(cfg.Value) {
		return nil, fmt.Errorf("condition [eq] only supports single value")
	}

	return &GteCond{
		mCfg:             cfg,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil

}

// Note: term performs containment rather than equality, so exact equality is not possible when a field value is an array.
func (cond *GteCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	v := cond.mCfg.Value
	vStr, ok := v.(string)
	if ok {
		v = fmt.Sprintf("%q", vStr)
	}
	dslStr := fmt.Sprintf(`
					{
						"range": {
							"%s": {
								"gte": %v
							}
						}
					}`, cond.mFilterFieldName, v)

	return dslStr, nil
}

func (cond *GteCond) Convert2SQL(ctx context.Context) (string, error) {
	v := cond.mCfg.Value
	vStr, ok := v.(string)
	if ok {
		v = fmt.Sprintf(`'%v'`, vStr)
	}
	sqlStr := fmt.Sprintf(`"%s" >= %v`, cond.mFilterFieldName, v)

	return sqlStr, nil
}

func rewriteGteCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": ">=", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
