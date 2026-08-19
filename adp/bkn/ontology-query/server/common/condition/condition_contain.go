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
)

type ContainCond struct {
	mCfg             *CondCfg
	IsSliceValue     bool
	mValue           any
	mSliceValue      []any
	mFilterFieldName string
}

// contain means the left property value is an array, and the right value is a single value or array. If it is an array, all right-side values must be contained in the property value.
func NewContainCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	if cfg.ValueFrom != ValueFrom_Const {
		return nil, fmt.Errorf("condition [contain] does not support value_from type '%s'", cfg.ValueFrom)
	}

	containCond := &ContainCond{
		mCfg:             cfg,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}

	if IsSlice(cfg.Value) {
		val, ok := cfg.Value.([]any)
		if !ok {
			return nil, fmt.Errorf("condition [contain] right value is not a valid array")
		}
		if len(val) == 0 {
			return nil, fmt.Errorf("condition [contain] right value is an empty array")
		}

		containCond.IsSliceValue = true
		containCond.mSliceValue = val

	} else {
		containCond.IsSliceValue = false
		containCond.mValue = cfg.Value
	}

	return containCond, nil
}

/*
If the right side is an array, generate the following DSL:

	{
	  "bool": {
	    "filter": [
	      {
	        "term": {
	          "<field>": {
	            "value": <value1>
	          }
	        }
	      },
	      {
	        "term": {
	          "<field>": {
	            "value": <value2>
	          }
	        }
	      }
	    ]
	  }
	}

If the right side is a single value, generate the following DSL:

	{
	  "term": {
	    "<field>": {
	      "value": <value>
	    }
	  }
	}
*/
func (cond *ContainCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	var dslStr string
	if cond.IsSliceValue {
		subStrs := []string{}
		for _, val := range cond.mSliceValue {
			vStr, ok := val.(string)
			if ok {
				val = fmt.Sprintf("%q", vStr)
			}

			subStr := fmt.Sprintf(`
			{
				"term": {
					"%s": {
						"value": %v
					}
				}
			}`, cond.mFilterFieldName, val)

			subStrs = append(subStrs, subStr)

		}

		dslStr = fmt.Sprintf(`
		{
			"bool": {
				"filter": [
					%s
				]
			}
		}
		`, strings.Join(subStrs, ","))

	} else {
		val := cond.mValue
		vStr, ok := val.(string)
		if ok {
			val = fmt.Sprintf("%q", vStr)
		}

		dslStr = fmt.Sprintf(`
		{
			"term": {
				"%s": {
					"value": %v
				}
			}
		}`, cond.mFilterFieldName, val)
	}

	return dslStr, nil
}

func (cond *ContainCond) Convert2SQL(ctx context.Context) (string, error) {
	// Use the json_array_contains function to implement contain.
	// The left property value is an array, and the right value is a single value or array.
	// If the right side is an array, all values in it must be contained in the property value.
	var sqlStr string

	if cond.IsSliceValue {
		// When the right side is an array, all values must be in the left array.
		// Generate a json_array_contains condition for each value and join them with AND.
		conditions := []string{}
		for _, val := range cond.mSliceValue {
			var condition string
			vStr, ok := val.(string)
			if ok {
				// Handle string values and escape single quotes.
				escapedVal := strings.ReplaceAll(vStr, "'", "''")
				condition = fmt.Sprintf(`json_array_contains("%s", '%s')`, cond.mFilterFieldName, escapedVal)
			} else {
				// Handle non-string values.
				condition = fmt.Sprintf(`json_array_contains("%s", %v)`, cond.mFilterFieldName, val)
			}
			conditions = append(conditions, condition)
		}

		// Join all conditions with AND to ensure every right-side value is in the left array.
		sqlStr = strings.Join(conditions, " AND ")

	} else {
		// The right side is a single value.
		val := cond.mValue
		vStr, ok := val.(string)
		if ok {
			// Handle string values and escape single quotes.
			escapedVal := strings.ReplaceAll(vStr, "'", "''")
			sqlStr = fmt.Sprintf(`json_array_contains("%s", '%s')`, cond.mFilterFieldName, escapedVal)
		} else {
			// Handle non-string values.
			sqlStr = fmt.Sprintf(`json_array_contains("%s", %v)`, cond.mFilterFieldName, val)
		}
	}

	return sqlStr, nil
}

func rewriteContainCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "contain", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
