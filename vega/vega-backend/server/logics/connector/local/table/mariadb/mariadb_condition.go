// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// ConvertFilterCondition 将已验证的条件转换为类型化节点；MariaDB/MySQL 共用此入口。
func (c *MariaDBConnector) ConvertFilterCondition(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	switch condition.GetOperation() {
	case filter_condition.OperationAnd:
		return c.ConvertFilterConditionAnd(condition)
	case filter_condition.OperationOr:
		return c.ConvertFilterConditionOr(condition)
	default:
		return c.ConvertFilterConditionWithOpr(condition)
	}
}

// ConvertFilterConditionAnd combines converted child conditions with AND.
func (c *MariaDBConnector) ConvertFilterConditionAnd(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.AndCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.AndCond")
	}
	convertedConds := sb.And{}
	for _, subCond := range cond.SubConds {
		convertedCond, err := c.ConvertFilterCondition(subCond)
		if err != nil {
			return nil, err
		}
		convertedConds = append(convertedConds, convertedCond)
	}
	return &convertedConds, nil
}

// ConvertFilterConditionOr combines converted child conditions with OR.
func (c *MariaDBConnector) ConvertFilterConditionOr(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.OrCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OrCond")
	}
	convertedConds := sb.Or{}
	for _, subCond := range cond.SubConds {
		convertedCond, err := c.ConvertFilterCondition(subCond)
		if err != nil {
			return nil, err
		}
		convertedConds = append(convertedConds, convertedCond)
	}
	return &convertedConds, nil
}

// ConvertFilterConditionWithOpr dispatches a non-composite filter operation to its SQL converter.
func (c *MariaDBConnector) ConvertFilterConditionWithOpr(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	switch condition.GetOperation() {
	case filter_condition.OperationEqual, filter_condition.OperationEqual2:
		return c.ConvertFilterConditionEqual(condition)
	case filter_condition.OperationNotEqual, filter_condition.OperationNotEqual2:
		return c.ConvertFilterConditionNotEqual(condition)
	case filter_condition.OperationGt, filter_condition.OperationGt2:
		return c.ConvertFilterConditionGt(condition)
	case filter_condition.OperationGte, filter_condition.OperationGte2:
		return c.ConvertFilterConditionGte(condition)
	case filter_condition.OperationLt, filter_condition.OperationLt2:
		return c.ConvertFilterConditionLt(condition)
	case filter_condition.OperationLte, filter_condition.OperationLte2:
		return c.ConvertFilterConditionLte(condition)
	case filter_condition.OperationIn:
		return c.ConvertFilterConditionIn(condition)
	case filter_condition.OperationNotIn:
		return c.ConvertFilterConditionNotIn(condition)
	case filter_condition.OperationLike:
		return c.ConvertFilterConditionLike(condition)
	case filter_condition.OperationNotLike:
		return c.ConvertFilterConditionNotLike(condition)
	case filter_condition.OperationRegex:
		return c.ConvertFilterConditionRegex(condition)
	case filter_condition.OperationContain:
		return c.ConvertFilterConditionContain(condition)
	case filter_condition.OperationNotContain:
		return c.ConvertFilterConditionNotContain(condition)
	case filter_condition.OperationRange:
		return c.ConvertFilterConditionRange(condition)
	case filter_condition.OperationOutRange:
		return c.ConvertFilterConditionOutRange(condition)
	case filter_condition.OperationBetween:
		return c.ConvertFilterConditionBetween(condition)
	case filter_condition.OperationNull:
		return c.ConvertFilterConditionNull(condition)
	case filter_condition.OperationNotNull:
		return c.ConvertFilterConditionNotNull(condition)
	case filter_condition.OperationEmpty:
		return c.ConvertFilterConditionEmpty(condition)
	case filter_condition.OperationNotEmpty:
		return c.ConvertFilterConditionNotEmpty(condition)
	case filter_condition.OperationPrefix:
		return c.ConvertFilterConditionPrefix(condition)
	case filter_condition.OperationNotPrefix:
		return c.ConvertFilterConditionNotPrefix(condition)
	case filter_condition.OperationExist:
		return c.ConvertFilterConditionExist(condition)
	case filter_condition.OperationNotExist:
		return c.ConvertFilterConditionNotExist(condition)
	case filter_condition.OperationTrue:
		return c.ConvertFilterConditionTrue(condition)
	case filter_condition.OperationFalse:
		return c.ConvertFilterConditionFalse(condition)
	case filter_condition.OperationBefore:
		return c.ConvertFilterConditionBefore(condition)
	case filter_condition.OperationCurrent:
		return c.ConvertFilterConditionCurrent(condition)
	default:
		cause := fmt.Errorf("the sql query channel does not support this operation")
		switch condition.GetOperation() {
		case filter_condition.OperationMatch, filter_condition.OperationMatchPhrase, filter_condition.OperationMultiMatch:
			cause = fmt.Errorf("the sql query channel does not support this operation; full-text operations need a local index on the resource: build one, or use like/not_like/contain/prefix/regex on this channel")
		case filter_condition.OperationKnnVector:
			cause = fmt.Errorf("the sql query channel does not support this operation; vector search needs a local index with a vector feature on the field: build one first")
		}
		return nil, interfaces.NewUnsupportedOperationError(condition.GetOperation(), cause)
	}
}

// ConvertFilterConditionEqual builds an equality predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionEqual(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.EqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EqualCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "=")
}

// ConvertFilterConditionNotEqual builds an inequality predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionNotEqual(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotEqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEqualCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "<>")
}

// ConvertFilterConditionGt builds a greater-than predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionGt(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.GtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GtCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, ">")
}

// ConvertFilterConditionGte builds a greater-than-or-equal predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionGte(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.GteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GteCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, ">=")
}

// ConvertFilterConditionLt builds a less-than predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionLt(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.LtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LtCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "<")
}

// ConvertFilterConditionLte builds a less-than-or-equal predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionLte(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.LteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LteCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "<=")
}

// ConvertFilterConditionIn builds a predicate matching one of the supplied values.
func (c *MariaDBConnector) ConvertFilterConditionIn(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.InCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.InCond")
	}

	return sqlSetCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, false)
}

// ConvertFilterConditionNotIn builds a predicate excluding the supplied values.
func (c *MariaDBConnector) ConvertFilterConditionNotIn(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotInCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotInCond")
	}

	return sqlSetCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, true)
}

// ConvertFilterConditionLike builds a substring LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionLike(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.LikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LikeCond")
	}

	return &sb.Contains{Expr: sqlResourceColumn(cond.Lfield), Value: &sb.Param{Value: cond.Value}}, nil
}

// ConvertFilterConditionNotLike builds a negated substring LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionNotLike(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotLikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotLikeCond")
	}

	return &sb.Not{Expr: &sb.Contains{Expr: sqlResourceColumn(cond.Lfield), Value: &sb.Param{Value: cond.Value}}}, nil
}

// ConvertFilterConditionRegex builds a regular-expression predicate.
func (c *MariaDBConnector) ConvertFilterConditionRegex(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.RegexCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RegexCond")
	}

	return &sb.Regex{Expr: sqlResourceColumn(cond.Lfield), Pattern: &sb.Param{Value: cond.Value}}, nil
}

// ConvertFilterConditionContain requires every supplied item to occur in a comma-separated field.
func (c *MariaDBConnector) ConvertFilterConditionContain(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.ContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ContainCond")
	}

	return sqlCSVCondition(cond.Lfield, cond.Value, false), nil
}

// ConvertFilterConditionNotContain matches when a supplied item is absent from a comma-separated field.
func (c *MariaDBConnector) ConvertFilterConditionNotContain(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotContainCond")
	}

	return sqlCSVCondition(cond.Lfield, cond.Value, true), nil
}

// ConvertFilterConditionRange builds inclusive lower and upper bounds joined with AND.
func (c *MariaDBConnector) ConvertFilterConditionRange(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.RangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RangeCond")
	}

	return sqlRangeCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, false)
}

// ConvertFilterConditionOutRange builds exclusive outer bounds joined with OR.
func (c *MariaDBConnector) ConvertFilterConditionOutRange(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.OutRangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OutRangeCond")
	}

	return sqlRangeCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, true)
}

// ConvertFilterConditionBetween builds an inclusive predicate between two values.
func (c *MariaDBConnector) ConvertFilterConditionBetween(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.BetweenCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BetweenCond")
	}

	return sqlRangeCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, false)
}

// ConvertFilterConditionNull builds an IS NULL predicate.
func (c *MariaDBConnector) ConvertFilterConditionNull(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NullCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: false}, nil
}

// ConvertFilterConditionNotNull builds an IS NOT NULL predicate.
func (c *MariaDBConnector) ConvertFilterConditionNotNull(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotNullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotNullCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: true}, nil
}

// ConvertFilterConditionEmpty matches an empty string.
func (c *MariaDBConnector) ConvertFilterConditionEmpty(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.EmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EmptyCond")
	}

	return &sb.Eq{Left: sqlResourceColumn(cond.Lfield), Right: &sb.Literal{Value: ""}}, nil
}

// ConvertFilterConditionNotEmpty excludes empty strings.
func (c *MariaDBConnector) ConvertFilterConditionNotEmpty(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotEmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEmptyCond")
	}

	return &sb.NotEq{Left: sqlResourceColumn(cond.Lfield), Right: &sb.Literal{Value: ""}}, nil
}

// ConvertFilterConditionPrefix builds a prefix LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionPrefix(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.PrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.PrefixCond")
	}

	return sqlPrefixCondition(cond.Lfield, cond.Value, false), nil
}

// ConvertFilterConditionNotPrefix builds a negated prefix LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionNotPrefix(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotPrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotPrefixCond")
	}

	return sqlPrefixCondition(cond.Lfield, cond.Value, true), nil
}

// ConvertFilterConditionExist matches non-NULL field values.
func (c *MariaDBConnector) ConvertFilterConditionExist(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.ExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ExistCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: true}, nil
}

// ConvertFilterConditionNotExist matches NULL field values.
func (c *MariaDBConnector) ConvertFilterConditionNotExist(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotExistCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: false}, nil
}

// ConvertFilterConditionTrue matches a true Boolean or nonzero TINYINT(1) value.
func (c *MariaDBConnector) ConvertFilterConditionTrue(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.TrueCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.TrueCond")
	}

	return sqlBooleanCondition(cond.Lfield, true)
}

// ConvertFilterConditionFalse matches a false Boolean or zero TINYINT(1) value.
func (c *MariaDBConnector) ConvertFilterConditionFalse(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.FalseCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.FalseCond")
	}

	return sqlBooleanCondition(cond.Lfield, false)
}

// ConvertFilterConditionBefore matches values before the specified interval relative to now.
func (c *MariaDBConnector) ConvertFilterConditionBefore(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.BeforeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BeforeCond")
	}

	interval, ok := common.NumberAsInt64(cond.Value[0])
	if !ok || interval <= 0 {
		return nil, interfaces.NewConditionBuildError("before interval must be a positive integer")
	}
	unit, ok := cond.Value[1].(string)
	if !ok || !calendarUnit(unit) {
		return nil, interfaces.NewConditionBuildError("invalid before unit")
	}
	return &sb.Lt{Left: sqlResourceColumn(cond.Lfield), Right: &sb.Function{Name: "DATE_SUB", Args: []sb.SqlNode{&sb.Function{Name: "NOW"}, &sb.Interval{Value: &sb.Param{Value: interval}, Unit: sb.IntervalUnit(unit)}}}}, nil
}

// ConvertFilterConditionCurrent matches values in the current calendar interval.
func (c *MariaDBConnector) ConvertFilterConditionCurrent(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.CurrentCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.CurrentCond")
	}

	format := ""
	switch cond.Value {
	case filter_condition.CurrentYear:
		format = "%Y"
	case filter_condition.CurrentMonth:
		format = "%Y-%m"
	case filter_condition.CurrentWeek:
		format = "%x-%v"
	case filter_condition.CurrentDay:
		format = "%Y-%m-%d"
	case filter_condition.CurrentHour:
		format = "%Y-%m-%d %H"
	case filter_condition.CurrentMinute:
		format = "%Y-%m-%d %H:%i"
	default:
		return nil, interfaces.NewConditionBuildError("unsupported current interval")
	}
	return &sb.Eq{Left: sqlDateFormat(sqlResourceColumn(cond.Lfield), format), Right: sqlDateFormat(&sb.Function{Name: "NOW"}, format)}, nil
}

// sqlResourceColumn 沿用现有结构化接口的 OriginalName 回退和别名限定规则。
func sqlResourceColumn(field *interfaces.Property) sb.SqlNode {
	return sqlColumnName(mariadbOriginalName(field))
}

func sqlColumnName(name string) sb.SqlNode {
	qualifier, column, qualified := strings.Cut(name, ".")
	if qualified {
		return &sb.Column{Qualifier: strings.TrimSpace(qualifier), Name: strings.TrimSpace(column)}
	}
	return &sb.Column{Name: strings.TrimSpace(name)}
}

func sqlComparison(field, rightField *interfaces.Property, value any, op string) (sb.SqlNode, error) {
	left := sqlResourceColumn(field)
	if rightField != nil {
		return sqlComparisonNode(left, sqlResourceColumn(rightField), op)
	}
	if value == nil && (op == "=" || op == "<>") {
		return &sb.IsNull{Expr: left, Negated: op == "<>"}, nil
	}
	right, err := sqlConditionValue(field, value)
	if err != nil {
		return nil, err
	}
	return sqlComparisonNode(left, right, op)
}

func sqlComparisonNode(left, right sb.SqlNode, op string) (sb.SqlNode, error) {
	switch op {
	case "=", "==":
		return &sb.Eq{Left: left, Right: right}, nil
	case "<>", "!=":
		return &sb.NotEq{Left: left, Right: right}, nil
	case ">":
		return &sb.Gt{Left: left, Right: right}, nil
	case ">=":
		return &sb.GtOrEq{Left: left, Right: right}, nil
	case "<":
		return &sb.Lt{Left: left, Right: right}, nil
	case "<=":
		return &sb.LtOrEq{Left: left, Right: right}, nil
	default:
		return nil, interfaces.NewConditionBuildError("unsupported comparison operation")
	}
}

func sqlConditionValue(field *interfaces.Property, value any) (sb.SqlNode, error) {
	if field == nil || !interfaces.DataType_IsDate(field.Type) {
		return &sb.Param{Value: value}, nil
	}
	if field.Type != interfaces.DataType_Timestamp {
		parsed, err := filter_condition.ParseTemporalValue(field.Type, value, common.ServerTimeZone())
		if err != nil {
			return nil, err
		}
		return &sb.Param{Value: parsed}, nil
	}
	if err := validateDateValue(field, value); err != nil {
		return nil, err
	}
	normalized := normalizeTimestampValue(value)
	if _, native := normalized.(time.Time); native {
		return &sb.Param{Value: normalized}, nil
	}
	return &sb.Function{Name: "FROM_UNIXTIME", Args: []sb.SqlNode{&sb.Arithmetic{Left: &sb.Param{Value: normalized}, Op: sb.Divide, Right: &sb.Literal{Value: 1000}}}}, nil
}

func sqlSetCondition(expr sb.SqlNode, field *interfaces.Property, values []any, negated bool) (sb.SqlNode, error) {
	if len(values) == 0 {
		if negated {
			return &sb.And{}, nil
		}
		return &sb.Or{}, nil
	}
	nodes := make([]sb.SqlNode, 0, len(values))
	for _, value := range values {
		node, err := sqlConditionValue(field, value)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return &sb.In{Expr: expr, Values: nodes, Negated: negated}, nil
}

func sqlRangeCondition(expr sb.SqlNode, field *interfaces.Property, values []any, outside bool) (sb.SqlNode, error) {
	if len(values) != 2 {
		return nil, interfaces.NewConditionBuildError("range requires two values")
	}
	lower, err := sqlConditionValue(field, values[0])
	if err != nil {
		return nil, fmt.Errorf("failed to convert range lower bound: %w", err)
	}
	upper, err := sqlConditionValue(field, values[1])
	if err != nil {
		return nil, fmt.Errorf("failed to convert range upper bound: %w", err)
	}
	if outside {
		return &sb.Or{&sb.Lt{Left: expr, Right: lower}, &sb.Gt{Left: expr, Right: upper}}, nil
	}
	return &sb.And{&sb.GtOrEq{Left: expr, Right: lower}, &sb.LtOrEq{Left: expr, Right: upper}}, nil
}

func sqlPrefixCondition(field *interfaces.Property, value string, negated bool) sb.SqlNode {
	pattern := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value) + "%"
	escape := "!"
	return &sb.Like{Expr: sqlResourceColumn(field), Pattern: &sb.Param{Value: pattern}, Escape: &escape, Negated: negated}
}

func sqlCSVCondition(field *interfaces.Property, values []any, negated bool) sb.SqlNode {
	nodes := make([]sb.SqlNode, 0, len(values))
	for _, value := range values {
		found := &sb.Function{Name: "FIND_IN_SET", Args: []sb.SqlNode{&sb.Param{Value: value}, sqlResourceColumn(field)}}
		var node sb.SqlNode = &sb.Gt{Left: found, Right: &sb.Literal{Value: 0}}
		if negated {
			node = &sb.Eq{Left: found, Right: &sb.Literal{Value: 0}}
		}
		nodes = append(nodes, node)
	}
	if negated {
		result := sb.Or(nodes)
		return &result
	}
	result := sb.And(nodes)
	return &result
}

func sqlBooleanCondition(field *interfaces.Property, value bool) (sb.SqlNode, error) {
	if field.Type == interfaces.DataType_Boolean {
		return &sb.Eq{Left: sqlResourceColumn(field), Right: &sb.Literal{Value: value}}, nil
	}
	if !isNumericBoolean(field) {
		return nil, interfaces.NewConditionBuildError("boolean condition requires BOOLEAN or TINYINT(1)")
	}
	if value {
		return &sb.NotEq{Left: sqlResourceColumn(field), Right: &sb.Literal{Value: 0}}, nil
	}
	return &sb.Eq{Left: sqlResourceColumn(field), Right: &sb.Literal{Value: 0}}, nil
}

func sqlDateFormat(expr sb.SqlNode, format string) sb.SqlNode {
	return &sb.Function{Name: "DATE_FORMAT", Args: []sb.SqlNode{expr, &sb.Literal{Value: format}}}
}

// isNumericBoolean recognizes integer-mapped TINYINT(1) fields for Boolean predicates.
func isNumericBoolean(field *interfaces.Property) bool {
	return field.Type == interfaces.DataType_Integer && strings.EqualFold(strings.TrimSpace(field.OriginalType), "tinyint(1)")
}

// normalizeTimestampValue converts numeric epoch milliseconds and RFC3339 text for SQL binding.
// time.Time values and other representations are preserved.
func normalizeTimestampValue(value any) any {
	switch v := value.(type) {
	case json.Number:
		if integer, err := v.Int64(); err == nil {
			return integer
		}
		if number, err := v.Float64(); err == nil {
			return int64(number)
		}
		return value
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case uint:
		if uint64(v) > math.MaxInt64 {
			return v
		}
		return int64(v)
	case uint32:
		return int64(v)
	case time.Time:
		return v
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(v)); err == nil {
			return parsed
		}
		return value
	default:
		return value
	}
}

// validateDateValue checks supported date and time value representations.
// TIME requires a string; other date types accept epoch milliseconds, RFC3339 text, or time.Time.
func validateDateValue(field *interfaces.Property, value any) error {
	if field.Type == interfaces.DataType_Date || field.Type == interfaces.DataType_Time {
		_, err := filter_condition.ParseTemporalValue(field.Type, value, common.ServerTimeZone())
		return err
	}

	if value == nil {
		return nil
	}

	switch value := value.(type) {
	case json.Number:
		if _, err := value.Float64(); err != nil {
			return fmt.Errorf("MariaDB date field %q requires epoch milliseconds, got %q", field.Name, value)
		}
		return nil
	case string:
		trimmed := strings.TrimSpace(value)
		if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return nil
		}
		if _, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return nil
		}
		return fmt.Errorf("MariaDB date field %q requires epoch milliseconds, got %q", field.Name, value)
	case time.Time:
		return nil
	case float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return nil
	default:
		return fmt.Errorf("MariaDB date field %q requires epoch milliseconds, got %T", field.Name, value)
	}
}

// calendarUnit allows MariaDB base interval units to enter SQL syntax.
// Composite units require string quantities and are outside the positive-integer contract.
func calendarUnit(unit string) bool {
	switch strings.ToLower(unit) {
	case "microsecond", "second", "minute", "hour", "day", "week", "month", "quarter", "year":
		return true
	default:
		return false
	}
}
