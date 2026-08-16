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
// like 的契约是「子串包含」，不是 SQL LIKE 模式。各后端此前对 % 的处理并不一致——
// SQL 连接器把它转义成字面量再两端补 %，OpenSearch 却把它翻译成 .*——同一条 like 在
// 明细库与索引上给出不同结果，而 SQL 侧的失败形式是静默返回空集，调用方看不出算子
// 本身没生效。因此未转义的 % 显式报错，指向 regex；要匹配字面量写 \%。
//
// _ 不在拒绝之列：改动前除 OpenSearch 外的每条路都把它当字面量（Special.Replace 转义成
// \_，DSL 的 .* 正则里也不是元字符），不存在 % 那种「静默空集」，而检索词里带下划线又
// 极常见，拒了纯属误伤。\_ 仍按转义解析，写法与 \% 对称。
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
		case r == '%':
			return "", fmt.Errorf(
				"condition [%s] value is matched as a literal substring, so the wildcard '%%' is not supported; "+
					"use operation [regex] for pattern matching, or escape it as '\\%%' to match the character itself",
				operation)
		default:
			literal.WriteRune(r)
		}
	}
	if escaped {
		literal.WriteRune('\\')
	}

	return literal.String(), nil
}

// EscapeLegacyLikeWildcards 把条件树里 like / not_like 值中未转义的 % 就地转义成 \%，
// 返回改写过的条件数。
//
// 只用于视图定义里存着的过滤条件：那份配置是服务端数据，调用方改不了，新契约直接报错会
// 让一次升级把存量视图查废。改写后按字面量匹配，与改动前 SQL 连接器的行为一致（Special
// 本来就会把 % 转义掉）。调用方传进来的条件不走这里，仍然硬拒并给出改用 regex 的指引。
func EscapeLegacyLikeWildcards(cfg *interfaces.FilterCondCfg) int {
	if cfg == nil {
		return 0
	}

	rewrittenCount := 0
	for _, sub := range cfg.SubConds {
		rewrittenCount += EscapeLegacyLikeWildcards(sub)
	}

	if cfg.Operation != OperationLike && cfg.Operation != OperationNotLike {
		return rewrittenCount
	}
	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return rewrittenCount
	}
	value, ok := cfg.Value.(string)
	if !ok {
		return rewrittenCount
	}
	if _, err := ParseLikeValue(cfg.Operation, value); err == nil {
		return rewrittenCount
	}

	var rewritten strings.Builder
	keepNext := false
	for _, r := range value {
		switch {
		case keepNext:
			rewritten.WriteRune(r)
			keepNext = false
		case r == '\\':
			rewritten.WriteRune(r)
			keepNext = true
		case r == '%':
			rewritten.WriteString(`\%`)
		default:
			rewritten.WriteRune(r)
		}
	}
	cfg.Value = rewritten.String()

	return rewrittenCount + 1
}
