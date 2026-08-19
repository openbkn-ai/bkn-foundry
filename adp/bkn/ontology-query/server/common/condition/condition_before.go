// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
	"os"

	dtype "ontology-query/interfaces/data_type"
)

type BeforeCond struct {
	mCfg             *CondCfg
	mValue           any
	mUnit            string
	mFilterFieldName string
}

func NewBeforeCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	// Check whether the value is a date/time type.
	simpleType := dtype.SimpleTypeMapping[cfg.NameField.Type]
	if simpleType != dtype.SimpleDate && simpleType != dtype.SimpleDatetime && simpleType != dtype.SimpleTime {
		return nil, fmt.Errorf("condition [before] left field is not a date/time field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	if cfg.ValueFrom != ValueFrom_Const {
		return nil, fmt.Errorf("condition [before] does not support value_from type '%s'", cfg.ValueFrom)
	}

	unit, exist := cfg.RemainCfg["unit"].(string)
	if !exist {
		return nil, fmt.Errorf("condition [before] unit is not specified")
	}

	return &BeforeCond{
		mCfg:             cfg,
		mValue:           cfg.Value,
		mUnit:            unit,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *BeforeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	// The before operator is mainly used for SQL; OpenSearch DSL is not implemented yet.
	unitMap := map[string]string{
		"year":   "y",
		"month":  "M",
		"week":   "w",
		"day":    "d",
		"hour":   "h",
		"minute": "m",
		"second": "s",
	}

	unit, ok := unitMap[cond.mUnit]
	if !ok {
		unit = cond.mUnit // Use the unit directly if it has already been abbreviated.
	}

	// Handle numeric types uniformly.
	var val = cond.mValue
	if f, ok := val.(float64); ok {
		val = int64(f)
	}

	return fmt.Sprintf(`{"range":{"%s":{"gte":"now-%v%s","lte":"now"}}}`,
		cond.mFilterFieldName, val, unit), nil
}

func (cond *BeforeCond) Convert2SQL(ctx context.Context) (string, error) {
	// Get the time zone, defaulting to UTC.
	tz := os.Getenv("TZ")
	if tz == "" {
		tz = "UTC"
	}

	sqlStr := fmt.Sprintf(`"%s" >= DATE_add('%s', -%v, CURRENT_TIMESTAMP AT TIME ZONE 'UTC' AT TIME ZONE '%s') 
		AND "%s" <= CURRENT_TIMESTAMP AT TIME ZONE 'UTC' AT TIME ZONE '%s'`,
		cond.mFilterFieldName, cond.mUnit, cond.mValue, tz, cond.mFilterFieldName, tz)
	return sqlStr, nil
}

func rewriteBeforeCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField == nil || cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "before", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
		RemainCfg:   cfg.RemainCfg,
	}, nil
}
