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

type GtCond struct {
	Cfg    *interfaces.FilterCondCfg
	Lfield *interfaces.Property
	Rfield *interfaces.Property
	Value  any
}

func (c *GtCond) GetOperation() string { return OperationGt }

func (c *GtCond) SupportSubCond() bool       { return false }
func (c *GtCond) NeedName() bool             { return true }
func (c *GtCond) NeedValue() bool            { return true }
func (c *GtCond) NeedConstValue() bool       { return false }
func (c *GtCond) IsSingleValue() bool        { return true }
func (c *GtCond) IsFixedLenArrayValue() bool { return false }
func (c *GtCond) RequiredValueLen() int      { return -1 }

// The gt condition determines whether a field is greater than a certain value
func (c *GtCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg.Name == "" {
		return nil, fmt.Errorf("condition [gt] left field is empty")
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
		return nil, fmt.Errorf("condition [gt] only supports single value")
	}

	cond := &GtCond{
		Cfg:    cfg,
		Lfield: field,
		Value:  cfg.Value,
	}

	if cfg.ValueFrom == interfaces.ValueFrom_Field {
		valueStr, ok := cfg.Value.(string)
		if !ok {
			return nil, fmt.Errorf("condition [gt] right value should be a string field name")
		}
		rfield, ok := fieldsMap[valueStr]
		if !ok {
			return nil, fmt.Errorf("condition [gt] right field '%s' not found", valueStr)
		}
		cond.Rfield = rfield
	}

	return cond, nil
}
