// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
	"strings"

	dtype "bkn-backend/interfaces/data_type"
)

type LikeCond struct {
	mCfg             *CondCfg
	mValue           string
	mFilterFieldName string
}

func NewLikeCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*ViewField) (Condition, error) {
	if !dtype.DataType_IsString(cfg.NameField.Type) &&
		dtype.SimpleTypeMapping[cfg.NameField.Type] != dtype.SimpleChar {
		return nil, fmt.Errorf("condition [like] left field is not a string field: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	if cfg.ValueFrom != ValueFrom_Const {
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
		mCfg:             cfg,
		mValue:           literal,
		mFilterFieldName: getFilterFieldName(cfg.Field, fieldsMap, false),
	}, nil
}

func (cond *LikeCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, words []string) ([]*VectorResp, error)) (string, error) {
	v := fmt.Sprintf("%q", LikeContainsPattern(cond.mValue))
	dslStr := fmt.Sprintf(`
					{
						"wildcard": {
							"%s": %v
						}
					}`, cond.mFilterFieldName, v)

	return dslStr, nil
}

func (cond *LikeCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" LIKE '%s'`, cond.mFilterFieldName, "%"+Special.Replace(cond.mValue)+"%")

	return sqlStr, nil
}

// convertLikeCondToDatasetFilterCondition converts LikeCond to dataset filter condition format
func convertLikeCondToDatasetFilterCondition(cfg *CondCfg) (map[string]any, error) {
	// 这条路不构造 LikeCond，值直接透传给 vega，因此契约校验要在这里也做一次：
	// 报错带上对象类属性名，比等 vega 报资源字段名更好定位。
	val, ok := cfg.Value.(string)
	if !ok {
		return nil, fmt.Errorf("condition [like] right value is not a string value: %v", cfg.Value)
	}
	if _, err := ParseLikeValue(OperationLike, val); err != nil {
		return nil, fmt.Errorf("property '%s': %w", cfg.Field, err)
	}

	return map[string]any{
		"field":      cfg.Field,
		"operation":  "like",
		// 透传原值（转义未解开）：vega 会再解析一次，提前解开会把字面量 % 当成通配符
		"value":      cfg.Value,
		"value_from": "const",
	}, nil
}

// ParseLikeValue 校验并解析 like / not_like 的值，返回要匹配的字面子串。
//
// like 的契约是「子串包含」，不是 SQL LIKE 模式：% 和 _ 不作通配符解释。此前各条路
// 的处理并不一致——DSL 路径把值原样塞进 .*value.* 正则，SQL 路径连两端的 % 都不补，
// 而下游 vega 的 SQL 连接器又会把 % 转义成字面量，于是 "%foo%" 这种 SQL 写法在任何
// 一条路上都恒返回空集且不报错。
//
// 因此显式拒绝未转义的 % 与 _：需要模式匹配用 regex，需要匹配字面量则写 \% \_。
// 与 vega 的 filter_condition.ParseLikeValue 同一套规则。
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

// LikeContainsPattern 把字面子串转成 OpenSearch 的 wildcard 模式，只需转义 * ? \。
func LikeContainsPattern(literal string) string {
	var escaped strings.Builder
	for _, r := range literal {
		if r == '*' || r == '?' || r == '\\' {
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(r)
	}
	return "*" + escaped.String() + "*"
}
