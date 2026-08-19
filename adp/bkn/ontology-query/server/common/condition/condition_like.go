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
	dtype "ontology-query/interfaces/data_type"
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

	return &LikeCond{
		mCfg:             cfg,
		mValue:           val,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *LikeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	// Replace wildcards in like.
	v := common.ReplaceLikeWildcards(cond.mValue)
	v = fmt.Sprintf("%q", v)
	dslStr := fmt.Sprintf(`
					{
						"regexp": {
							"%s": %v
						}
					}`, cond.mFilterFieldName, v)

	return dslStr, nil
}

func (cond *LikeCond) Convert2SQL(ctx context.Context) (string, error) {
	v := cond.mCfg.Value
	vStr, ok := v.(string)
	if ok {
		v = Special.Replace(fmt.Sprintf("%v", vStr))
	}

	vStr = fmt.Sprintf("%v", v)
	sqlStr := fmt.Sprintf(`"%s" LIKE '%s'`, cond.mFilterFieldName, "%"+vStr+"%")

	return sqlStr, nil
}

func rewriteLikeCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "like", "field": cfg.Name})
	}
	return &CondCfg{
		Name:      cfg.NameField.MappedField.Name,
		Operation: cfg.Operation,
		ValueOptCfg: ValueOptCfg{
			Value:     cfg.Value,
			RealValue: cfg.Value, // Pass the ontology like value to the real_value of the view like filter.
		},
	}, nil
}
