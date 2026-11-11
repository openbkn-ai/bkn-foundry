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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
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

// The like condition determines whether a field matches a certain string pattern
func (c *LikeCond) New(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg.Field == "" {
		return nil, fmt.Errorf("condition [like] left field is empty")
	}
	field, ok := fieldsMap[cfg.Field]
	if !ok {
		return nil, fmt.Errorf("condition [like] left field '%s' not found", cfg.Field)
	}
	if !interfaces.DataType_IsString(field.Type) {
		return nil, fmt.Errorf("condition [like] left field '%s' is not a string field", cfg.Field)
	}

	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [like] does not support value_from type '%s'", cfg.ValueFrom)
	}
	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [like] right value is not a string value: %v", cfg.Value)
	}
	if err := ValidateLikePattern(OperationLike, val); err != nil {
		return nil, err
	}

	return &LikeCond{
		Cfg:    cfg,
		Lfield: field,
		Value:  val,
	}, nil
}

// ValidateLikePattern 校验 SQL LIKE 模式；反斜杠转义下一字符，孤立的结尾转义无效。
func ValidateLikePattern(operation, pattern string) error {
	escaped := false
	for _, r := range pattern {
		if escaped {
			escaped = false
		} else if r == '\\' {
			escaped = true
		}
	}
	if escaped {
		return interfaces.NewConditionBuildError("condition [%s] pattern ends with an incomplete escape", operation)
	}
	return nil
}

// NormalizeSQLLikePattern 为只允许转义模式元字符的 SQL 通道规范化普通字符转义。
// 不补充通配符，保留百分号、下划线和反斜杠自身的转义。
func NormalizeSQLLikePattern(operation, pattern string) (string, error) {
	if err := ValidateLikePattern(operation, pattern); err != nil {
		return "", err
	}
	var result strings.Builder
	escaped := false
	for _, r := range pattern {
		if !escaped && r == '\\' {
			escaped = true
			continue
		}
		if escaped && (r == '%' || r == '_' || r == '\\') {
			result.WriteByte('\\')
		}
		result.WriteRune(r)
		escaped = false
	}
	return result.String(), nil
}

// SQLLikeToWildcard 将完整 SQL LIKE 模式转为索引 wildcard 模式，不自动补充通配符。
func SQLLikeToWildcard(operation, pattern string) (string, error) {
	if err := ValidateLikePattern(operation, pattern); err != nil {
		return "", err
	}
	var result strings.Builder
	escaped := false
	for _, r := range pattern {
		if !escaped {
			switch r {
			case '\\':
				escaped = true
				continue
			case '%':
				result.WriteByte('*')
				continue
			case '_':
				result.WriteByte('?')
				continue
			}
		}
		if r == '*' || r == '?' || r == '\\' {
			result.WriteByte('\\')
		}
		result.WriteRune(r)
		escaped = false
	}
	return result.String(), nil
}
