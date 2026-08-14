// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"context"
	"fmt"
	"strings"

	"vega-backend/interfaces"
)

type LikeCond struct {
	Cfg    *interfaces.FilterCondCfg
	Lfield *interfaces.Property
	Value  string
}

func (c *LikeCond) GetOperation() string { return OperationLike }

func (c *LikeCond) SupportSubCond() bool       { return false }
func (c *LikeCond) NeedName() bool             { return true }
func (c *LikeCond) NeedValue() bool            { return true }
func (c *LikeCond) NeedConstValue() bool       { return true }
func (c *LikeCond) IsSingleValue() bool        { return true }
func (c *LikeCond) IsFixedLenArrayValue() bool { return false }
func (c *LikeCond) RequiredValueLen() int      { return -1 }

// like 条件, 判断字段是否匹配某个字符串模式
func (c *LikeCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg.Name == "" {
		return nil, fmt.Errorf("condition [like] left field is empty")
	}
	field, ok := fieldsMap[cfg.Name]
	if !ok {
		return nil, fmt.Errorf("condition [like] left field '%s' not found", cfg.Name)
	}
	if !interfaces.DataType_IsString(field.Type) {
		return nil, fmt.Errorf("condition [like] left field '%s' is not a string field", cfg.Name)
	}

	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [like] does not support value_from type '%s'", cfg.ValueFrom)
	}
	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [like] right value is not a string value: %v", cfg.Value)
	}
	literal, err := ParseLikeValue(OperationLike, val)
	if err != nil {
		return nil, err
	}

	return &LikeCond{
		Cfg:    cfg,
		Lfield: field,
		Value:  literal,
	}, nil
}

// ParseLikeValue 校验并解析 like / not_like 的值，返回要匹配的字面子串。
//
// like 的契约是「子串包含」，不是 SQL LIKE 模式：% 和 _ 不作通配符解释。各后端此前
// 对通配符的处理并不一致——SQL 连接器把 % 转义成字面量再两端补 %，OpenSearch 却把 %
// 翻译成 .*——同一条 like 在明细库与索引上给出不同结果，而失败形式是静默返回空集，
// 调用方看不出算子本身没生效。
//
// 因此这里显式拒绝未转义的 % 与 _：需要模式匹配用 regex，需要匹配字面量则写 \% \_。
func ParseLikeValue(operation, value string) (string, error) {
	var literal strings.Builder
	escaped := false

	for _, r := range value {
		switch {
		case escaped:
			// 只有 % _ \ 有转义意义，其余保留反斜杠本身
			if r != '%' && r != '_' && r != '\\' {
				literal.WriteRune('\\')
			}
			literal.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '%' || r == '_':
			return "", fmt.Errorf(
				"condition [%s] value is matched as a literal substring, so the wildcard '%s' is not supported; "+
					"use operation [regex] for pattern matching, or escape it as '\\%s' to match the character itself",
				operation, string(r), string(r))
		default:
			literal.WriteRune(r)
		}
	}
	if escaped {
		literal.WriteRune('\\')
	}

	return literal.String(), nil
}
