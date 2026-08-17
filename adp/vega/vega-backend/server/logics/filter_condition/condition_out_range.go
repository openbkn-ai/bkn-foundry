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

type OutRangeCond struct {
	Cfg    *interfaces.FilterCondCfg
	Lfield *interfaces.Property
	Value  []any
}

func (c *OutRangeCond) GetOperation() string { return OperationOutRange }

func (c *OutRangeCond) SupportSubCond() bool       { return false }
func (c *OutRangeCond) NeedName() bool             { return true }
func (c *OutRangeCond) NeedValue() bool            { return true }
func (c *OutRangeCond) NeedConstValue() bool       { return true }
func (c *OutRangeCond) IsSingleValue() bool        { return false }
func (c *OutRangeCond) IsFixedLenArrayValue() bool { return true }
func (c *OutRangeCond) RequiredValueLen() int      { return 2 }

// The out_range condition determines whether a field is not within a certain range
func (c *OutRangeCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg.Name == "" {
		return nil, fmt.Errorf("condition [out_range] left field is empty")
	}
	field, ok := fieldsMap[cfg.Name]
	if !ok {
		// If the field is not defined in the Schema, create a temporary Property object
		field = &interfaces.Property{
			Name:         cfg.Name,
			OriginalName: cfg.Name,
		}
	}
	// For fields not defined in the Schema, skip type checking
	if ok && !interfaces.DataType_IsDate(field.Type) && !interfaces.DataType_IsNumber(field.Type) {
		return nil, fmt.Errorf("condition [out_range] left field is not a date/number field: %s:%s", cfg.Name, field.Type)
	}

	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [out_range] does not support value_from type '%s'", cfg.ValueFrom)
	}
	val, ok := cfg.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("condition [out_range] right value should be an array")
	}
	if len(val) != 2 {
		return nil, fmt.Errorf("condition [out_range] right value should be an array of length 2")
	}

	return &OutRangeCond{
		Cfg:    cfg,
		Lfield: field,
		Value:  val,
	}, nil
}
