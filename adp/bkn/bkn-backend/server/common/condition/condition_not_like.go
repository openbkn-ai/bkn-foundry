// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	dtype "bkn-backend/interfaces/data_type"
)

type NotLikeCond struct {
	mCfg             *CondCfg
	mValue           string
	mFilterFieldName string
}

func NewNotLikeCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*ViewField) (Condition, error) {
	if !dtype.DataType_IsString(cfg.NameField.Type) &&
		dtype.SimpleTypeMapping[cfg.NameField.Type] != dtype.SimpleChar {
		return nil, fmt.Errorf("condition [not_like] left field is not a string field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	if cfg.ValueFrom != ValueFrom_Const {
		return nil, fmt.Errorf("condition [not_like] does not support value_from type '%s'", cfg.ValueFrom)
	}

	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [not_like] right value is not a string value: %v", cfg.Value)
	}
	literal, err := ParseLikeValue(OperationNotLike, val)
	if err != nil {
		return nil, err
	}

	return &NotLikeCond{
		mCfg:             cfg,
		mValue:           literal,
		mFilterFieldName: getFilterFieldName(cfg.Field, fieldsMap, false),
	}, nil
}

func (cond *NotLikeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, words []string) ([]*VectorResp, error)) (string, error) {
	v := fmt.Sprintf("%q", LikeContainsPattern(cond.mValue))

	dslStr := fmt.Sprintf(`
					{
						"bool": {
							"must_not": [
								{
									"wildcard": {
										"%s": %v
									}
								}
							]
						}
					}`, cond.mFilterFieldName, v)

	return dslStr, nil
}

func (cond *NotLikeCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" NOT LIKE '%s'`, cond.mFilterFieldName, "%"+Special.Replace(cond.mValue)+"%")

	return sqlStr, nil
}

// convertNotLikeCondToDatasetFilterCondition converts NotLikeCond to dataset filter condition format
func convertNotLikeCondToDatasetFilterCondition(cfg *CondCfg) (map[string]any, error) {
	// 同 like：这条路不构造 NotLikeCond，值直接透传给 vega，契约校验要在这里也做一次
	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [not_like] right value is not a string value: %v", cfg.Value)
	}
	if _, err := ParseLikeValue(OperationNotLike, val); err != nil {
		return nil, fmt.Errorf("property '%s': %w", cfg.Field, err)
	}

	return map[string]any{
		"field":      cfg.Field,
		"operation":  "not_like",
		"value":      cfg.Value,
		"value_from": "const",
	}, nil
}
