// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type BeforeCond struct {
	Cfg    *interfaces.FilterCondCfg
	Lfield *interfaces.Property
	Value  []any
}

// NormalizeBeforeInterval 校验正整数间隔，并保留 JSON 整数的精度。
func NormalizeBeforeInterval(values []any) error {
	if len(values) == 0 {
		return nil
	}
	if number, isJSON := values[0].(json.Number); isJSON {
		// 先排除非正数及极端指数，再精确判断整数性，避免浮点舍入改变输入。
		approximate, ok := common.NumberAsFloat64(number)
		if !ok || approximate <= 0 {
			return fmt.Errorf("condition [before]'s interval must be a positive integer within int64 range")
		}
		exact, valid := new(big.Rat).SetString(string(number))
		if !valid || !exact.IsInt() || !exact.Num().IsInt64() {
			return fmt.Errorf("condition [before]'s interval must be a positive integer within int64 range")
		}
		values[0] = exact.Num().Int64()
		return nil
	}
	interval, ok := common.NumberAsInt64(values[0])
	if !ok || interval <= 0 {
		return fmt.Errorf("condition [before]'s interval must be a positive integer within int64 range")
	}
	return nil
}

func (c *BeforeCond) GetOperation() string { return OperationBefore }

func (c *BeforeCond) SupportSubCond() bool       { return false }
func (c *BeforeCond) NeedName() bool             { return true }
func (c *BeforeCond) NeedValue() bool            { return true }
func (c *BeforeCond) NeedConstValue() bool       { return true }
func (c *BeforeCond) IsSingleValue() bool        { return false }
func (c *BeforeCond) IsFixedLenArrayValue() bool { return true }
func (c *BeforeCond) RequiredValueLen() int      { return 2 }

// The "before" condition determines whether a field is before a certain time
func (c *BeforeCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg.Name == "" {
		return nil, fmt.Errorf("condition [before] left field is empty")
	}
	field, ok := fieldsMap[cfg.Name]
	if !ok {
		return nil, fmt.Errorf("condition [before] left field '%s' not found", cfg.Name)
	}
	if !interfaces.DataType_IsDate(field.Type) {
		return nil, fmt.Errorf("condition [before] left field is not a date field: %s:%s", cfg.Name, field.Type)
	}

	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [before] does not support value_from type '%s'", cfg.ValueFrom)
	}
	val, ok := cfg.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("condition [before] right value should be an array")
	}
	if len(val) != 2 {
		return nil, fmt.Errorf("condition [before] right value should be an array of length 2")
	}
	// 运行时归一化使用副本，不改写待保存的定义。
	val = append([]any(nil), val...)
	if err := NormalizeBeforeInterval(val); err != nil {
		return nil, err
	}
	unit, ok := val[1].(string)
	if !ok {
		return nil, fmt.Errorf("condition [before]'s unit must be a string")
	}
	// 公共层允许已有连接器支持的单位；具体子集由实际查询通道校验。
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "microsecond", "second", "seconds", "minute", "minutes", "hour", "hours",
		"day", "days", "week", "weeks", "month", "months", "quarter", "year", "years":
	default:
		return nil, fmt.Errorf("condition [before]'s unit is unsupported: %q", unit)
	}

	return &BeforeCond{
		Cfg:    cfg,
		Lfield: field,
		Value:  val,
	}, nil
}
