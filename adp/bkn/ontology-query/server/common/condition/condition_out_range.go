// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	dtype "ontology-query/interfaces/data_type"
)

type OutRangeCond struct {
	mCfg             *CondCfg
	mValue           []any
	mFilterFieldName string
}

func NewOutRangeCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {

	val, ok := cfg.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("condition [out_range] right value should be an array of length 2")
	}

	if len(val) != 2 {
		return nil, fmt.Errorf("condition [out_range] right value should be an array of length 2")
	}

	if !IsSameType(val) {
		return nil, fmt.Errorf("condition [out_range] right value should be of the same type")
	}

	return &OutRangeCond{
		mCfg:             cfg,
		mValue:           val,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

// out_range  (-inf, value[0]) || [value[1], +inf)
func (cond *OutRangeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	lt := cond.mValue[0]
	gte := cond.mValue[1]

	if _, ok := lt.(string); ok {
		lt = fmt.Sprintf("%q", lt)
		gte = fmt.Sprintf("%q", gte)
	}

	var dslStr string
	if cond.mCfg.NameField.Type == dtype.DATATYPE_DATETIME {
		format := ""
		switch lt.(type) {
		case string:
			format = "strict_date_optional_time"
		case float64:
			format = "epoch_millis"
			lt = int64(lt.(float64))
			gte = int64(gte.(float64))
		}

		dslStr = fmt.Sprintf(`
					{
						"bool": {
							"should": [
								{
									"range": {
										"%s": {
											"lt": %v,
											"format": "%s"
										}
									}
								},
								{
									"range": {
										"%s": {
											"gte":  %v,
											"format": "%s"
										}
									}
								}
							]
						}
					}`, cond.mFilterFieldName, lt, format, cond.mFilterFieldName, gte, format)

	} else {

		dslStr = fmt.Sprintf(`
		{
			"bool": {
				"should": [
					{
						"range": {
							"%s": {
								"lt": %v
							}
						}
					},
					{
						"range": {
							"%s": {
								"gte":  %v
							}
						}
					}
				]
			}
		}`, cond.mFilterFieldName, lt, cond.mFilterFieldName, gte)

	}

	return dslStr, nil
}

func (cond *OutRangeCond) Convert2SQL(ctx context.Context) (string, error) {
	// out_range represents (-inf, value[0]) || [value[1], +inf).
	lt := cond.mValue[0]
	gte := cond.mValue[1]

	// Handle string values; wrap them in single quotes.
	ltStr, ok := lt.(string)
	if ok {
		ltStr = Special.Replace(fmt.Sprintf("%q", ltStr))
	} else {
		ltStr = fmt.Sprintf("%v", lt)
	}

	gteStr, ok := gte.(string)
	if ok {
		gteStr = Special.Replace(fmt.Sprintf("%q", gteStr))
	} else {
		gteStr = fmt.Sprintf("%v", gte)
	}

	// Build the SQL condition: field < lower_bound OR field >= upper_bound.
	sqlStr := fmt.Sprintf("(\"%s\" < %s OR \"%s\" >= %s)", cond.mFilterFieldName, ltStr, cond.mFilterFieldName, gteStr)
	return sqlStr, nil
}

func rewriteOutRangeCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "out_range", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
