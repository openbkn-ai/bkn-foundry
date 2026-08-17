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

type NotEmptyCond struct {
	Cfg    *interfaces.FilterCondCfg
	Lfield *interfaces.Property
}

func (c *NotEmptyCond) GetOperation() string { return OperationNotEmpty }

func (c *NotEmptyCond) SupportSubCond() bool       { return false }
func (c *NotEmptyCond) NeedName() bool             { return true }
func (c *NotEmptyCond) NeedValue() bool            { return false }
func (c *NotEmptyCond) NeedConstValue() bool       { return false }
func (c *NotEmptyCond) IsSingleValue() bool        { return false }
func (c *NotEmptyCond) IsFixedLenArrayValue() bool { return false }
func (c *NotEmptyCond) RequiredValueLen() int      { return -1 }

// The not_empty condition determines whether a field is not an empty string
func (c *NotEmptyCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg, fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("condition [not_empty] left field is empty")
	}
	field, ok := fieldsMap[cfg.Name]
	if !ok {
		return nil, fmt.Errorf("condition [not_empty] left field '%s' not found", cfg.Name)
	}

	// Only string types are allowed
	if !interfaces.DataType_IsString(field.Type) {
		return nil, fmt.Errorf("condition [not_empty] left field %s is not of string type, but %s", cfg.Name, field.Type)
	}

	return &NotEmptyCond{
		Cfg:    cfg,
		Lfield: field,
	}, nil

}
