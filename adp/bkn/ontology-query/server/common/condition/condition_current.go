// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	dtype "ontology-query/interfaces/data_type"
)

type CurrentCond struct {
	mCfg             *CondCfg
	mValue           string
	mFilterFieldName string
}

func NewCurrentCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	// Check whether the value is a date/time type.
	simpleType := dtype.SimpleTypeMapping[cfg.NameField.Type]
	if simpleType != dtype.SimpleDate && simpleType != dtype.SimpleDatetime && simpleType != dtype.SimpleTime {
		return nil, fmt.Errorf("condition [current] left field is not a date/time field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	if cfg.ValueFrom != ValueFrom_Const {
		return nil, fmt.Errorf("condition [current] does not support value_from type '%s'", cfg.ValueFrom)
	}

	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [current] right value should be string")
	}

	// Validate the unit value.
	validUnits := map[string]bool{
		"year":   true,
		"month":  true,
		"week":   true,
		"day":    true,
		"hour":   true,
		"minute": true,
	}
	if !validUnits[val] {
		return nil, errors.New(`condition [current] right value should be one of ["year", "month", "week", "day", "hour", "minute"], actual is ` + val)
	}

	return &CurrentCond{
		mCfg:             cfg,
		mValue:           val,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil
}

func (cond *CurrentCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	// The current operator is mainly used for SQL; OpenSearch DSL is not implemented yet.
	return "", nil
}

func (cond *CurrentCond) Convert2SQL(ctx context.Context) (string, error) {
	// Get the time zone, defaulting to UTC.
	tz := os.Getenv("TZ")
	if tz == "" {
		tz = "UTC"
	}

	// Load the time zone.
	location, err := time.LoadLocation(tz)
	if err != nil {
		location = time.UTC
	}

	now := time.Now().In(location)
	var start, end time.Time

	switch cond.mValue {
	case "year":
		start = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, location)
		end = start.AddDate(1, 0, 0)
	case "month":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
		end = start.AddDate(0, 1, 0)
	case "week":
		// Calculate Monday of the current week.
		weekday := now.Weekday()
		offset := int(time.Monday - weekday)
		if offset > 0 {
			offset -= 7 // If today is Sunday, subtract 7 days.
		}
		start = time.Date(now.Year(), now.Month(), now.Day()+offset, 0, 0, 0, 0, location)
		end = start.AddDate(0, 0, 7)
	case "day":
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		end = start.AddDate(0, 0, 1)
	case "hour":
		start = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, location)
		end = start.Add(time.Hour)
	case "minute":
		start = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, location)
		end = start.Add(time.Minute)
	default:
		return "", fmt.Errorf("unsupported unit: %s", cond.mValue)
	}

	sqlStr := fmt.Sprintf(`"%s" BETWEEN from_unixtime(%d) AND from_unixtime(%d)`,
		cond.mFilterFieldName, start.Unix(), end.Unix())
	return sqlStr, nil
}

func rewriteCurrentCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {
	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "current", "field": cfg.Name})
	}
	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
