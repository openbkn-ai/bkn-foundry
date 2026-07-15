// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dsl

import (
	"context"

	"vega-backend/interfaces"
)

func dslConditionCfg(name string, operation string, valueFrom string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Name:      name,
		Operation: operation,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: valueFrom,
			Value:     value,
		},
	}
}

func testDSLAdditionalFieldMap() map[string]*interfaces.Property {
	fields := testFieldMap()
	fields["score"] = &interfaces.Property{Name: "score", OriginalName: "score", Type: interfaces.DataType_Integer}
	fields["tags"] = &interfaces.Property{Name: "tags", OriginalName: "tags", Type: interfaces.DataType_Text}
	fields["created_at"] = &interfaces.Property{Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime}
	fields["embedding"] = &interfaces.Property{Name: "embedding", OriginalName: "embedding", Type: interfaces.DataType_Vector}
	return fields
}

type unsupportedDSLCondition struct {
	operation string
}

func (u unsupportedDSLCondition) GetOperation() string {
	if u.operation != "" {
		return u.operation
	}
	return "unsupported"
}

func (unsupportedDSLCondition) SupportSubCond() bool       { return false }
func (unsupportedDSLCondition) NeedName() bool             { return false }
func (unsupportedDSLCondition) NeedValue() bool            { return false }
func (unsupportedDSLCondition) NeedConstValue() bool       { return false }
func (unsupportedDSLCondition) IsSingleValue() bool        { return false }
func (unsupportedDSLCondition) IsFixedLenArrayValue() bool { return false }
func (unsupportedDSLCondition) RequiredValueLen() int      { return -1 }
func (unsupportedDSLCondition) New(context.Context, *interfaces.FilterCondCfg, map[string]*interfaces.Property) (interfaces.FilterCondition, error) {
	return nil, nil
}
