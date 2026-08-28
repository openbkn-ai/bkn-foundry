// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
)

type KnnCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
	mSubConds        []Condition
}

func NewKnnCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (Condition, error) {

	// Validate whether the name exists.
	name := getFilterFieldName(cfg.Name, fieldsMap, true)
	var field string
	// Return an error when querying *; it is unsupported because too many fields make vector search too slow.
	if name == AllField {
		return nil, fmt.Errorf(`the knn operation does not support the [*] query, please specify the field name explicitly`)
	} else {
		// This legacy OpenSearch path is retained only for compatibility. Resource-backed
		// queries use rewriteKnnCond and Vega resolves feature and model capability.
		fieldInfo := fieldsMap[name]
		if fieldInfo != nil {
			// Vector queries are allowed only for vectorized properties; otherwise return an error.
			field = "_vector_" + name
		} else {
			return nil, fmt.Errorf(`property [%s] cannot be used for [knn] filtering`, name)
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
	res, err := sonic.Marshal(vector[0].Vector)
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

	// The vector capability of resource fields is determined by the Vega Resource schema/features and build status.
	// ontology-query only rewrites object property names to resource field names and passes query terms or vector values through unchanged.
	return &CondCfg{
		Name:      cfg.NameField.MappedField.Name,
		Operation: OperationKNNVector,
		ValueOptCfg: ValueOptCfg{
			Value: cfg.Value,
		},
		RemainCfg: cfg.RemainCfg,
	}, nil
}
