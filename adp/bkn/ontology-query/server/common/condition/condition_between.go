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

type BetweenCond struct {
	mCfg             *CondCfg
	mValue           []any
	mFilterFieldName string
}

func NewBetweenCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	// Check whether the value is a numeric or time type.
	simpleType := dtype.SimpleTypeMapping[cfg.NameField.Type]
	isNumeric := simpleType == dtype.SimpleInt || simpleType == dtype.SimpleFloat || simpleType == dtype.SimpleDecimal
	isTime := simpleType == dtype.SimpleDate || simpleType == dtype.SimpleDatetime || simpleType == dtype.SimpleTime

	if !isNumeric && !isTime {
		return nil, fmt.Errorf("condition [between] left field is not a numeric or date/time field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	if cfg.ValueFrom != ValueFrom_Const {
		return nil, fmt.Errorf("condition [between] does not support value_from type '%s'", cfg.ValueFrom)
	}

	val, ok := cfg.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("condition [between] right value should be an array of length 2")
	}

	if len(val) != 2 {
		return nil, fmt.Errorf("condition [between] right value should be an array of length 2")
	}

	if !IsSameType(val) {
		return nil, fmt.Errorf("condition [between] right value should be of the same type")
	}

	return &BetweenCond{
		mCfg:             cfg,
		mValue:           val,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *BetweenCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	start := cond.mValue[0]
	end := cond.mValue[1]

	// Handle string values.
	if _, ok := start.(string); ok {
		start = fmt.Sprintf("%q", start)
		end = fmt.Sprintf("%q", end)
	}

	var dslStr string
	if cond.mCfg.NameField.Type == dtype.DATATYPE_DATETIME || cond.mCfg.NameField.Type == dtype.DATATYPE_DATE {
		var format string
		switch start.(type) {
		case string:
			format = "strict_date_optional_time"
		case float64:
			format = "epoch_millis"
			start = int64(start.(float64))
			end = int64(end.(float64))
		case int:
			format = "epoch_millis"
		case int64:
			format = "epoch_millis"
		}

		dslStr = fmt.Sprintf(`
		{
			"range": {
				"%s": {
					"gte": %v,
					"lte": %v,
					"format": "%s"
				}
			}
		}`, cond.mFilterFieldName, start, end, format)

	} else {
		dslStr = fmt.Sprintf(`
		{
			"range": {
				"%s": {
					"gte": %v,
					"lte": %v
				}
			}
		}`, cond.mFilterFieldName, start, end)
	}

	return dslStr, nil
}

func (cond *BetweenCond) Convert2SQL(ctx context.Context) (string, error) {
	// between represents the closed interval [start, end].
	start := cond.mValue[0]
	end := cond.mValue[1]

	// Handle string values; wrap them in single quotes.
	startStr, ok := start.(string)
	if ok {
		startStr = Special.Replace(fmt.Sprintf("%q", startStr))
	} else {
		startStr = fmt.Sprintf("%v", start)
	}

	endStr, ok := end.(string)
	if ok {
		endStr = Special.Replace(fmt.Sprintf("%q", endStr))
	} else {
		endStr = fmt.Sprintf("%v", end)
	}

	// Build the SQL condition: field BETWEEN lower_bound AND upper_bound.
	sqlStr := fmt.Sprintf(`"%s" BETWEEN %s AND %s`, cond.mFilterFieldName, startStr, endStr)
	return sqlStr, nil
}

func rewriteBetweenCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "between", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
