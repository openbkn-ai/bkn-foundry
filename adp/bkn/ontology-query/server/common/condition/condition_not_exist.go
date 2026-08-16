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

type NotExistCond struct {
	mCfg       *CondCfg
	mfieldName string
}

func NewNotExistCond(cfg *CondCfg) (Condition, error) {
	return &NotExistCond{
		mCfg:       cfg,
		mfieldName: cfg.Name,
	}, nil
}

func (cond *NotExistCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	dslStr := `
	{
		"bool": {
			"must_not": [
				{
					"exists": {
						"field": "%s"
					}
				}
			]
		}
	}
	`

	return fmt.Sprintf(dslStr, cond.mfieldName), nil
}

func (cond *NotExistCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" IS NULL`, cond.mfieldName)
	return sqlStr, nil
}

func rewriteNotExistCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// 过滤条件中的属性字段换成映射的视图字段
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "not_exist", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
