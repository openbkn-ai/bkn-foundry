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

	"github.com/bytedance/sonic"

	"ontology-query/common"
)

type InCond struct {
	mCfg             *CondCfg
	mValue           []any
	mFilterFieldName string
}

func NewInCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {

	if !common.IsSlice(cfg.Value) {
		return nil, fmt.Errorf("condition [in] right value should be an array")
	}

	if !common.IsSameType(cfg.Value.([]any)) {
		return nil, fmt.Errorf("condition [in] right value should be an array composed of elements of same type")
	}

	return &InCond{
		mCfg:             cfg,
		mValue:           cfg.Value.([]any),
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *InCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	res, err := sonic.Marshal(cond.mValue)
	if err != nil {
		return "", fmt.Errorf("condition [in] json marshal right value failed, %s", err.Error())
	}

	dslStr := fmt.Sprintf(`
					{
						"terms": {
							"%s": %v
						}
					}`, cond.mFilterFieldName, string(res))

	return dslStr, nil

}

func (cond *InCond) Convert2SQL(ctx context.Context) (string, error) {
	_, err := sonic.Marshal(cond.mValue)
	if err != nil {
		return "", fmt.Errorf("condition [in] json marshal right value failed, %s", err.Error())
	}

	valueList := make([]string, len(cond.mValue))
	for i, v := range cond.mValue {
		vStr, ok := v.(string)
		if ok {
			valueList[i] = fmt.Sprintf(`'%v'`, vStr)
		} else {
			valueList[i] = fmt.Sprintf(`%v`, v)
		}
	}
	value := strings.Join(valueList, ",")
	sqlStr := fmt.Sprintf(`"%s" IN %s`, cond.mFilterFieldName, "("+value+")")

	return sqlStr, nil
}

func rewriteInCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "in", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
