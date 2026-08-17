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

type NotEqualCond struct {
	Cfg    *interfaces.FilterCondCfg
	Lfield *interfaces.Property
	Rfield *interfaces.Property
	Value  any
}

func (c *NotEqualCond) GetOperation() string { return OperationNotEqual }

func (c *NotEqualCond) SupportSubCond() bool       { return false }
func (c *NotEqualCond) NeedName() bool             { return true }
func (c *NotEqualCond) NeedValue() bool            { return true }
func (c *NotEqualCond) NeedConstValue() bool       { return false }
func (c *NotEqualCond) IsSingleValue() bool        { return true }
func (c *NotEqualCond) IsFixedLenArrayValue() bool { return false }
func (c *NotEqualCond) RequiredValueLen() int      { return -1 }

// Not equal to not_eq, determine whether the field is not equal to the value on the right
func (c *NotEqualCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg.Name == "" {
		return nil, fmt.Errorf("condition [not_eq] left field is empty")
	}
	field, ok := fieldsMap[cfg.Name]
	if !ok {
		// If the field is not defined in the Schema, create a temporary Property object
		field = &interfaces.Property{
			Name:         cfg.Name,
			OriginalName: cfg.Name,
		}
	}

	if IsSlice(cfg.Value) {
		return nil, fmt.Errorf("condition [not_eq] only supports single value")
	}

	cond := &NotEqualCond{
		Cfg:    cfg,
		Lfield: field,
		Value:  cfg.Value,
	}

	if cfg.ValueFrom == interfaces.ValueFrom_Field {
		valueStr, ok := cfg.Value.(string)
		if !ok {
			return nil, fmt.Errorf("condition [not_eq] right value should be a string field name")
		}
		rfield, ok := fieldsMap[valueStr]
		if !ok {
			return nil, fmt.Errorf("condition [not_eq] right field '%s' not found", valueStr)
		}
		cond.Rfield = rfield
	}

	return cond, nil
}
