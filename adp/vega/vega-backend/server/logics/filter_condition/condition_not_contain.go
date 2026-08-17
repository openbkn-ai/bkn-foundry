// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"context"
	"fmt"

	"vega-backend/interfaces"
)

type NotContainCond struct {
	Cfg    *interfaces.FilterCondCfg
	Lfield *interfaces.Property
	Value  []any
}

func (c *NotContainCond) GetOperation() string { return OperationNotContain }

func (c *NotContainCond) SupportSubCond() bool       { return false }
func (c *NotContainCond) NeedName() bool             { return true }
func (c *NotContainCond) NeedValue() bool            { return true }
func (c *NotContainCond) NeedConstValue() bool       { return true }
func (c *NotContainCond) IsSingleValue() bool        { return false }
func (c *NotContainCond) IsFixedLenArrayValue() bool { return false }
func (c *NotContainCond) RequiredValueLen() int      { return -1 }

// It does not contain not_contain. The left property value is an array, and the right property value is an array. All values within the group should be outside the property value
func (c *NotContainCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg.Name == "" {
		return nil, fmt.Errorf("condition [not_contain] left field is empty")
	}
	field, ok := fieldsMap[cfg.Name]
	if !ok {
		return nil, fmt.Errorf("condition [not_contain] left field '%s' not found", cfg.Name)
	}

	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [not_contain] does not support value_from type '%s'", cfg.ValueFrom)
	}
	val, ok := cfg.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("condition [not_contain] right value should be an array")
	}
	if len(val) == 0 {
		return nil, fmt.Errorf("condition [not_contain] right value should be an array of length >= 1")
	}

	return &NotContainCond{
		Cfg:    cfg,
		Lfield: field,
		Value:  val,
	}, nil
}
