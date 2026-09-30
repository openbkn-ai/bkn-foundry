// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"context"
	"fmt"
	"reflect"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// Concatenate the filter conditions to the query section of the dsl request
func NewFilterCondition(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg == nil {
		return nil, nil //nolint:nilnil // Nil result represents an expected absence condition.
	}

	// Determine whether the filter is an empty object {}
	if cfg.Name == "" && cfg.Operation == "" && len(cfg.SubConds) == 0 && cfg.ValueFrom == "" && cfg.Value == nil {
		return nil, nil //nolint:nilnil // Nil result represents an expected absence condition.
	}

	condFactory, exists := OperationMap[cfg.Operation]
	if !exists {
		return nil, fmt.Errorf("unsupported operation: %s", cfg.Operation)
	}

	cond, err := condFactory.New(ctx, cfg, fieldsMap)
	if err != nil {
		return nil, err
	}
	return cond, nil
}

// NormalizeValueFrom applies the default value source and rejects sources that
// the condition converters cannot execute. It also walks nested conditions.
func NormalizeValueFrom(cfg *interfaces.FilterCondCfg) error {
	if cfg == nil {
		return nil
	}
	if cfg.Name == "" && cfg.Operation == "" && len(cfg.SubConds) == 0 && cfg.ValueFrom == "" && cfg.Value == nil {
		return nil
	}
	factory, ok := OperationMap[cfg.Operation]
	if !ok {
		return fmt.Errorf("unsupported operation: %s", cfg.Operation)
	}
	for _, child := range cfg.SubConds {
		if err := NormalizeValueFrom(child); err != nil {
			return err
		}
	}
	if !factory.NeedValue() {
		return nil
	}
	if cfg.ValueFrom == "" {
		cfg.ValueFrom = interfaces.ValueFrom_Const
	}
	if cfg.ValueFrom != interfaces.ValueFrom_Const &&
		(factory.NeedConstValue() || cfg.ValueFrom != interfaces.ValueFrom_Field) {
		return fmt.Errorf("operation %q does not support value_from %q", cfg.Operation, cfg.ValueFrom)
	}
	return nil
}

func IsSlice(i any) bool {
	kind := reflect.ValueOf(i).Kind()
	return kind == reflect.Slice || kind == reflect.Array
}

func IsSameType(arr []any) bool {
	if len(arr) == 0 {
		return true
	}

	firstType := reflect.TypeOf(arr[0])
	for _, v := range arr {
		if reflect.TypeOf(v) != firstType {
			return false
		}
	}

	return true
}
