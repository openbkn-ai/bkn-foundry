// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type KnnCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
	mSubConds        []Condition
}

func NewKnnCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*ViewField) (Condition, error) {
	if cfg.ValueFrom != ValueFrom_Const {
		return nil, fmt.Errorf("condition [knn] does not support value_from type '%s'", cfg.ValueFrom)
	}

	name := getFilterFieldName(cfg.Field, fieldsMap, true)
	var field string
	// Replace * with _vector when querying *.
	if name == AllField {
		field = "_vector"
	} else {
		field = name
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

func (cond *KnnCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, words []string) ([]*VectorResp, error)) (string, error) {
	v := fmt.Sprintf("%v", cond.mCfg.Value)

	vector, err := vectorizer(ctx, []string{v})
	if err != nil {
		// Ignore this KNN condition and return an empty string when DefaultSmallModelEnabled is false.
		var httpErr *rest.HTTPError
		if errors.As(err, &httpErr) && httpErr != nil &&
			httpErr.BaseError.ErrorDetails == DEFAULT_SMALL_MODEL_ENABLED_FALSE_ERROR {
			return "", nil
		}
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
		validSubDSLs := []string{}
		for _, subCond := range cond.mSubConds {
			dsl, err := subCond.Convert(ctx, vectorizer)
			if err != nil {
				return "", err
			}

			// Drop empty strings from ignored conditions.
			if dsl != "" && dsl != "{}" {
				validSubDSLs = append(validSubDSLs, dsl)
			}
		}

		// Add filter only when valid child conditions exist.
		if len(validSubDSLs) > 0 {
			for i, dsl := range validSubDSLs {
				if i != len(validSubDSLs)-1 {
					subCondStr += dsl + ","
				} else {
					subCondStr += dsl
				}
			}
			subDSL = fmt.Sprintf(subDSL, subCondStr)
		} else {
			// Do not add filter when all child conditions are ignored.
			subDSL = ""
		}
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

// convertKnnCondToDatasetFilterCondition converts KnnCond to dataset filter condition format
// Reference: ontology-query's rewriteKnnCond pattern - use vectorizer to convert text to vector
func convertKnnCondToDatasetFilterCondition(ctx context.Context, cfg *CondCfg,
	fieldsMap map[string]*ViewField,
	vectorizer func(ctx context.Context, word string) ([]*VectorResp, error)) (map[string]any, error) {
	// Convert text value to vector using vectorizer
	v := fmt.Sprintf("%v", cfg.Value)
	vectorResp, err := vectorizer(ctx, v)
	if err != nil {
		// Ignore this KNN condition and return an empty string when DefaultSmallModelEnabled is false.
		var httpErr *rest.HTTPError
		if errors.As(err, &httpErr) && httpErr != nil &&
			httpErr.BaseError.ErrorDetails == DEFAULT_SMALL_MODEL_ENABLED_FALSE_ERROR {
			return nil, nil
		}
		return nil, fmt.Errorf("condition [knn]: vectorizer [%s] failed, error: %s", v, err.Error())
	}
	if len(vectorResp) == 0 {
		return nil, fmt.Errorf("condition [knn]: vectorizer [%s] returned empty result", v)
	}

	name := getFilterFieldName(cfg.Field, fieldsMap, true)
	var field string
	// Replace * with _vector when querying *.
	if name == AllField {
		field = "_vector"
	} else {
		field = name
	}

	subConds := []map[string]any{}
	for _, subCond := range cfg.SubConds {
		cond, err := ConvertCondCfgToFilterCondition(ctx, subCond, fieldsMap, vectorizer)
		if err != nil {
			return nil, err
		}

		if cond != nil {
			subConds = append(subConds, cond)
		}

	}

	knnCond := map[string]any{
		"field":          field,
		"operation":      "knn_vector",
		"value":          vectorResp[0].Vector, // Vector value after conversion
		"value_from":     "const",
		"sub_conditions": subConds,
	}
	for k, v := range cfg.RemainCfg {
		knnCond[k] = v
	}
	return knnCond, nil
}
