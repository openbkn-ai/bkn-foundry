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
	// When LegacyWildcards is true, Value uses the unparsed legacy form (% as a wildcard).
	// Each connector renders it with its pre-change semantics instead of literal substring semantics.
	LegacyWildcards bool
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
	if cfg.LegacyLikeWildcards {
		return &LikeCond{Cfg: cfg, Lfield: field, Value: val, LegacyWildcards: true}, nil
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

// ParseLikeValue validates and parses a like/not_like value and returns the literal substring to match.
//
// The like contract means substring containment, not SQL LIKE syntax. Backends previously handled %
// inconsistently: SQL connectors escaped it as a literal and wrapped the value in %, while OpenSearch
// translated it to .*. The same condition therefore produced different database and index results,
// with SQL silently returning an empty set. Reject an unescaped % and direct callers to regex; use \%
// to match a literal percent sign.
//
// Do not reject _: every pre-change path except OpenSearch treated it literally (Special.Replace
// escaped it as \_, and it is not a metacharacter in the DSL .* regex). It never caused the silent
// empty-set behavior of %, and underscores are common in search terms. Parse \_ as an escape,
// symmetrically with \%.
func ParseLikeValue(operation, value string) (string, error) {
	var literal strings.Builder
	escaped := false

	for _, r := range value {
		switch {
		case escaped:
			// Only % _ \ have escape semantics; preserve the backslash for other characters.
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

// MarkLegacyLikeWildcards marks like/not_like conditions that use % as a wildcard as legacy
// and returns the number of marked conditions.
//
// This applies only to filters stored in view definitions. Callers cannot change that server-side
// data, so enforcing the new contract would break legacy views after an upgrade. Once marked, each
// connector renders the condition with its own pre-change semantics: SQL treats % literally while
// index connectors translate it to a regex wildcard. This preserves every legacy view result.
// Normalizing all paths to literal matching would silently empty results that index paths previously
// matched as wildcards.
//
// Caller-supplied conditions do not use this path; they are rejected with guidance to use regex.
func MarkLegacyLikeWildcards(cfg *interfaces.FilterCondCfg) int {
	if cfg == nil {
		return 0
	}

	markedCount := 0
	for _, sub := range cfg.SubConds {
		markedCount += MarkLegacyLikeWildcards(sub)
	}

	if cfg.Operation != OperationLike && cfg.Operation != OperationNotLike {
		return markedCount
	}
	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return markedCount
	}
	value, ok := cfg.Value.(string)
	if !ok {
		return markedCount
	}
	if _, err := ParseLikeValue(cfg.Operation, value); err == nil {
		return markedCount
	}

	cfg.LegacyLikeWildcards = true

	return markedCount + 1
}
