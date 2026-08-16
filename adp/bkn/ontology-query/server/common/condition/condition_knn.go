// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"encoding/json"
	"fmt"
)

type KnnCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
	mSubConds        []Condition
}

func NewKnnCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (Condition, error) {

	// 校验名称是否存在
	name := getFilterFieldName(cfg.Name, fieldsMap, true)
	var field string
	// 如果指定*查询,报错，不支持，因为字段太多，向量耗时太长
	if name == AllField {
		return nil, fmt.Errorf(`the knn operation does not support the [*] query, please specify the field name explicitly`)
	} else {
		// 向量字段做knn查询时需要把向量字段换成 "_vector_"+property.Name
		// 字段是否做了knn
		fieldInfo := fieldsMap[name]
		if fieldInfo.IndexConfig != nil && fieldInfo.IndexConfig.VectorConfig.Enabled {
			// 配置了向量化的属性,可以做向量化查询,否则报错,不能进行向量化查询
			field = "_vector_" + name
		} else {
			return nil, fmt.Errorf(`the index of property [%s] is not configured for vectorization and cannot be used for [knn] filtering. Please check the index configuration of the object type and the current request`, name)
		}
	}

	subConds := []Condition{}
	for _, subCond := range cfg.SubConds {
		cond, err := NewCondition(ctx, subCond, fieldScope, fieldsMap)
		if err != nil {
			return nil, err
		}

		if cond != nil {
			subConds = append(subConds, cond)
		}
	}

	return &KnnCond{
		mCfg:             cfg,
		mFilterFieldName: field,
		mSubConds:        subConds,
	}, nil
}

func (cond *KnnCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	v := fmt.Sprintf("%v", cond.mCfg.Value)

	vector, err := vectorizer(ctx, cond.mCfg.NameField, v)
	if err != nil {
		return "", fmt.Errorf("condition [knn]: vectorizer [%s] failed, error: %s", v, err.Error())
	}
	res, err := json.Marshal(vector[0].Vector)
	if err != nil {
		return "", fmt.Errorf("condition [in] json marshal right value failed, %s", err.Error())
	}

	// sub condition
	subDSL := ""
	if len(cond.mSubConds) > 0 {
		subDSL = `
		,
		"filter": {
			"bool": {
				"must": [
					%s
				]
			}
		}
		`

		subCondStr := ""
		for i, subCond := range cond.mSubConds {
			dsl, err := subCond.Convert(ctx, vectorizer)
			if err != nil {
				return "", err
			}

			if i != len(cond.mSubConds)-1 {
				dsl += ","
			}

			subCondStr += dsl

		}
		subDSL = fmt.Sprintf(subDSL, subCondStr)
	}

	// Use default values when limit_key and limit_value are omitted.
	key := cond.mCfg.RemainCfg["limit_key"]
	value := cond.mCfg.RemainCfg["limit_value"]
	if key == nil || key == "" {
		key = KNN_LIMIT_KEY_DEFAULT
	}
	if value == nil {
		value = KNN_LIMIT_VALUE_DEFAULT
	}

	dslStr := fmt.Sprintf(`
					{
						"knn": {
							"%s":{
								"%s": %v,
								"vector": %v
								%s
							}
						}
					}`, cond.mFilterFieldName, key, value,
		string(res), subDSL)

	return dslStr, nil
}

func (cond *KnnCond) Convert2SQL(ctx context.Context) (string, error) {
	return "", nil
}

func rewriteKnnCond(ctx context.Context, cfg *CondCfg,
	vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (*CondCfg, error) {

	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "knn", "field": cfg.Name})
	}

	// 资源字段的向量能力由 Vega Resource schema/features 与构建状态决定。
	// ontology-query 只负责把对象属性名改写为资源字段名，查询词或向量值原样下传。
	return &CondCfg{
		Name:      cfg.NameField.MappedField.Name,
		Operation: OperationKNNVector,
		ValueOptCfg: ValueOptCfg{
			Value: cfg.Value,
		},
		RemainCfg: cfg.RemainCfg,
	}, nil
}
