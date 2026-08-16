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

// 检查字段值是否 IS NULL， OpenSearch 默认不会对 null 值进行索引，
// 因此 IS NULL 的逻辑等同于查找"该字段不存在索引值"的文档，查询会匹配以下情况：
// 1. 文档中完全没有这个字段
// 2. 该字段在 JSON 中被显示设为 null
// 3. 该字段是一个空数组
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
	// 过滤条件中的属性字段换成映射的视图字段
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "null", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
