// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// Special 仅转义 LIKE 模式字符；参数绑定负责 SQL 字面量。
var Special = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")

// ConvertFilterCondition 将已验证的过滤条件转换为公共结构化节点。
func (c *PostgresqlConnector) ConvertFilterCondition(condition interfaces.FilterCondition) (sb.SqlNode, error) {
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
func (c *PostgresqlConnector) ConvertFilterConditionAnd(condition interfaces.FilterCondition) (sb.SqlNode, error) {
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
func (c *PostgresqlConnector) ConvertFilterConditionOr(condition interfaces.FilterCondition) (sb.SqlNode, error) {
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
func (c *PostgresqlConnector) ConvertFilterConditionWithOpr(condition interfaces.FilterCondition) (sb.SqlNode, error) {
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
func (c *PostgresqlConnector) ConvertFilterConditionEqual(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.EqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EqualCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "=")
}

// ConvertFilterConditionNotEqual builds an inequality predicate for a constant or another field.
func (c *PostgresqlConnector) ConvertFilterConditionNotEqual(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotEqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEqualCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "<>")
}

// ConvertFilterConditionGt builds a greater-than predicate for a constant or another field.
func (c *PostgresqlConnector) ConvertFilterConditionGt(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.GtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GtCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, ">")
}

// ConvertFilterConditionGte builds a greater-than-or-equal predicate for a constant or another field.
func (c *PostgresqlConnector) ConvertFilterConditionGte(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.GteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GteCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, ">=")
}

// ConvertFilterConditionLt builds a less-than predicate for a constant or another field.
func (c *PostgresqlConnector) ConvertFilterConditionLt(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.LtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LtCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "<")
}

// ConvertFilterConditionLte builds a less-than-or-equal predicate for a constant or another field.
func (c *PostgresqlConnector) ConvertFilterConditionLte(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.LteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LteCond")
	}

	return sqlComparison(cond.Lfield, cond.Rfield, cond.Value, "<=")
}

// ConvertFilterConditionIn builds a predicate matching one of the supplied values.
func (c *PostgresqlConnector) ConvertFilterConditionIn(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.InCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.InCond")
	}

	return sqlSetCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, false)
}

// ConvertFilterConditionNotIn builds a predicate excluding the supplied values.
func (c *PostgresqlConnector) ConvertFilterConditionNotIn(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotInCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotInCond")
	}

	return sqlSetCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, true)
}

// ConvertFilterConditionLike builds a substring LIKE predicate with escaped input.
func (c *PostgresqlConnector) ConvertFilterConditionLike(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.LikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LikeCond")
	}

	return &sb.Like{Expr: sqlResourceColumn(cond.Lfield), Pattern: &sb.Param{Value: "%" + Special.Replace(cond.Value) + "%"}}, nil
}

// ConvertFilterConditionNotLike builds a negated substring LIKE predicate with escaped input.
func (c *PostgresqlConnector) ConvertFilterConditionNotLike(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotLikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotLikeCond")
	}

	return &sb.Like{Expr: sqlResourceColumn(cond.Lfield), Pattern: &sb.Param{Value: "%" + Special.Replace(cond.Value) + "%"}, Negated: true}, nil
}

// ConvertFilterConditionRegex builds a regular-expression predicate.
func (c *PostgresqlConnector) ConvertFilterConditionRegex(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.RegexCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RegexCond")
	}

	return &sb.Regex{Expr: sqlResourceColumn(cond.Lfield), Pattern: &sb.Param{Value: cond.Value}}, nil
}

// ConvertFilterConditionContain requires every supplied item to occur in a comma-separated field.
func (c *PostgresqlConnector) ConvertFilterConditionContain(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.ContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ContainCond")
	}

	return sqlCSVCondition(cond.Lfield, cond.Value, false), nil
}

// ConvertFilterConditionNotContain matches when a supplied item is absent from a comma-separated field.
func (c *PostgresqlConnector) ConvertFilterConditionNotContain(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotContainCond")
	}

	return sqlCSVCondition(cond.Lfield, cond.Value, true), nil
}

// ConvertFilterConditionRange builds inclusive lower and upper bounds joined with AND.
func (c *PostgresqlConnector) ConvertFilterConditionRange(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.RangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RangeCond")
	}

	return sqlRangeCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, false)
}

// ConvertFilterConditionOutRange builds exclusive outer bounds joined with OR.
func (c *PostgresqlConnector) ConvertFilterConditionOutRange(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.OutRangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OutRangeCond")
	}

	return sqlRangeCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, true)
}

// ConvertFilterConditionBetween builds an inclusive predicate between two values.
func (c *PostgresqlConnector) ConvertFilterConditionBetween(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.BetweenCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BetweenCond")
	}

	return sqlRangeCondition(sqlResourceColumn(cond.Lfield), cond.Lfield, cond.Value, false)
}

// ConvertFilterConditionNull builds an IS NULL predicate.
func (c *PostgresqlConnector) ConvertFilterConditionNull(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NullCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: false}, nil
}

// ConvertFilterConditionNotNull builds an IS NOT NULL predicate.
func (c *PostgresqlConnector) ConvertFilterConditionNotNull(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotNullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotNullCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: true}, nil
}

// ConvertFilterConditionEmpty matches an empty string.
func (c *PostgresqlConnector) ConvertFilterConditionEmpty(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.EmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EmptyCond")
	}

	return &sb.Eq{Left: sqlResourceColumn(cond.Lfield), Right: &sb.Param{Value: ""}}, nil
}

// ConvertFilterConditionNotEmpty excludes empty strings.
func (c *PostgresqlConnector) ConvertFilterConditionNotEmpty(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotEmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEmptyCond")
	}

	return &sb.NotEq{Left: sqlResourceColumn(cond.Lfield), Right: &sb.Param{Value: ""}}, nil
}

// ConvertFilterConditionPrefix builds a prefix LIKE predicate with escaped input.
func (c *PostgresqlConnector) ConvertFilterConditionPrefix(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.PrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.PrefixCond")
	}

	return sqlPrefixCondition(cond.Lfield, cond.Value, false), nil
}

// ConvertFilterConditionNotPrefix builds a negated prefix LIKE predicate with escaped input.
func (c *PostgresqlConnector) ConvertFilterConditionNotPrefix(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotPrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotPrefixCond")
	}

	return sqlPrefixCondition(cond.Lfield, cond.Value, true), nil
}

// ConvertFilterConditionExist matches non-NULL field values.
func (c *PostgresqlConnector) ConvertFilterConditionExist(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.ExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ExistCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: true}, nil
}

// ConvertFilterConditionNotExist matches NULL field values.
func (c *PostgresqlConnector) ConvertFilterConditionNotExist(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.NotExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotExistCond")
	}

	return &sb.IsNull{Expr: sqlResourceColumn(cond.Lfield), Negated: false}, nil
}

// ConvertFilterConditionTrue matches a true Boolean or nonzero TINYINT(1) value.
func (c *PostgresqlConnector) ConvertFilterConditionTrue(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.TrueCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.TrueCond")
	}

	return sqlBooleanCondition(cond.Lfield, true)
}

// ConvertFilterConditionFalse matches a false Boolean or zero TINYINT(1) value.
func (c *PostgresqlConnector) ConvertFilterConditionFalse(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.FalseCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.FalseCond")
	}

	return sqlBooleanCondition(cond.Lfield, false)
}

// ConvertFilterConditionBefore matches values before the specified interval relative to now.
func (c *PostgresqlConnector) ConvertFilterConditionBefore(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.BeforeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BeforeCond")
	}
	if len(cond.Value) != 2 {
		return nil, interfaces.NewConditionBuildError("before condition requires exactly 2 values")
	}
	n, ok := common.NumberAsInt64(cond.Value[0])
	if !ok || n <= 0 {
		return nil, interfaces.NewConditionBuildError("before interval must be a positive integer")
	}
	unit, ok := cond.Value[1].(string)
	if !ok {
		return nil, interfaces.NewConditionBuildError("before unit must be a string")
	}
	pgUnit, err := intervalUnit(unit)
	if err != nil {
		return nil, err
	}
	cutoff := &sb.Arithmetic{Left: &sb.Function{Name: "TRANSACTION_TIMESTAMP"}, Op: sb.Subtract, Right: &sb.Interval{Value: &sb.Param{Value: n}, Unit: sb.IntervalUnit(pgUnit)}}
	return &sb.Lt{Left: sqlResourceColumn(cond.Lfield), Right: cutoff}, nil
}

// ConvertFilterConditionCurrent matches values in the current calendar interval.
func (c *PostgresqlConnector) ConvertFilterConditionCurrent(condition interfaces.FilterCondition) (sb.SqlNode, error) {
	cond, ok := condition.(*filter_condition.CurrentCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.CurrentCond")
	}
	unit := ""
	switch cond.Value {
	case filter_condition.CurrentYear:
		unit = "year"
	case filter_condition.CurrentMonth:
		unit = "month"
	case filter_condition.CurrentWeek:
		unit = "week"
	case filter_condition.CurrentDay:
		unit = "day"
	case filter_condition.CurrentHour:
		unit = "hour"
	case filter_condition.CurrentMinute:
		unit = "minute"
	default:
		return nil, interfaces.NewConditionBuildError("current condition has unsupported format")
	}
	left := &sb.Function{Name: "DATE_TRUNC", Args: []sb.SqlNode{&sb.Param{Value: unit}, sqlResourceColumn(cond.Lfield)}}
	right := &sb.Function{Name: "DATE_TRUNC", Args: []sb.SqlNode{&sb.Param{Value: unit}, &sb.Function{Name: "TRANSACTION_TIMESTAMP"}}}
	return &sb.Eq{Left: left, Right: right}, nil
}

// sqlResourceColumn 沿用现有结构化接口的 OriginalName 回退和别名限定规则。
func sqlResourceColumn(field *interfaces.Property) sb.SqlNode {
	name := field.OriginalName
	if name == "" {
		name = field.Name
	}
	return sqlColumnName(name)
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
		// 结构化条件由左侧源字段推导参数类型，保留 JSON 数字原文，避免科学计数法被提升为浮点数。
		if number, ok := value.(json.Number); ok {
			value = number.String()
		}
		return &sb.Param{Value: value}, nil
	}
	if field.Type == interfaces.DataType_Date || field.Type == interfaces.DataType_Datetime || field.Type == interfaces.DataType_Time {
		parsed, err := filter_condition.ParseTemporalValue(field.Type, value, common.ServerTimeZone())
		if err != nil {
			return nil, err
		}
		value = parsed
	}
	if err := validateDateValue(field, value); err != nil {
		return nil, err
	}
	return dateValueExpr(field, normalizeTimestampValue(value)), nil
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
	return &sb.Like{Expr: sqlResourceColumn(field), Pattern: &sb.Param{Value: Special.Replace(value) + "%"}, Negated: negated}
}

func sqlCSVCondition(field *interfaces.Property, values []any, negated bool) sb.SqlNode {
	nodes := make([]sb.SqlNode, 0, len(values))
	for _, value := range values {
		found := &sb.Function{Name: "FIND_IN_SET", Args: []sb.SqlNode{&sb.Param{Value: fmt.Sprint(value)}, sqlResourceColumn(field)}}
		var node sb.SqlNode = &sb.Gt{Left: found, Right: &sb.Param{Value: 0}}
		if negated {
			node = &sb.Eq{Left: found, Right: &sb.Param{Value: 0}}
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
	if field.Type != interfaces.DataType_Boolean {
		return nil, interfaces.NewConditionBuildError("postgresql boolean condition requires BOOLEAN")
	}
	return &sb.Eq{Left: sqlResourceColumn(field), Right: &sb.Param{Value: value}}, nil
}

// intervalUnit maps calendar units to PostgreSQL interval unit names.
func intervalUnit(unit string) (string, error) {
	u := strings.ToUpper(strings.TrimSpace(unit))
	switch u {
	case "YEAR", "YEARS":
		return "year", nil
	case "MONTH", "MONTHS":
		return "month", nil
	case "DAY", "DAYS":
		return "day", nil
	case "HOUR", "HOURS":
		return "hour", nil
	case "MINUTE", "MINUTES":
		return "minute", nil
	case "SECOND", "SECONDS":
		return "second", nil
	default:
		return "", fmt.Errorf("unsupported interval unit for postgresql: %s", unit)
	}
}

// normalizeTimestampValue normalizes supported timestamp values for bound SQL parameters.
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

// dateValueExpr 保留 DATE/TIME、无时区墙上时间和带时区瞬间的绑定语义。
func dateValueExpr(field *interfaces.Property, value any) sb.SqlNode {
	param := &sb.Param{Value: value}
	if field.Type == interfaces.DataType_Date {
		return &sb.Cast{Expr: param, Type: sb.CastDate}
	}
	if field.Type == interfaces.DataType_Time {
		return param
	}
	original := strings.ToLower(strings.TrimSpace(field.OriginalType))
	if _, native := value.(time.Time); native {
		if field.Type == interfaces.DataType_Datetime && original != "timestamptz" && !strings.Contains(original, "with time zone") {
			return &sb.Cast{Expr: param, Type: sb.CastDateTime}
		}
		if original == "timestamp" || original == "timestamp without time zone" || original == "date" {
			return &sb.Cast{Expr: param, Type: sb.CastDateTime}
		}
		return param
	}
	switch original {
	case "time", "timetz", "time without time zone", "time with time zone":
		return param
	}
	instant := &sb.Function{Name: "FROM_UNIXTIME", Args: []sb.SqlNode{&sb.Arithmetic{Left: param, Op: sb.Divide, Right: &sb.Param{Value: 1000}}}}
	if original == "timestamp" || original == "timestamp without time zone" {
		return &sb.Cast{Expr: instant, Type: sb.CastDateTime}
	}
	return instant
}

// validateDateValue checks that a value is compatible with the field's date or time type.
func validateDateValue(field *interfaces.Property, value any) error {
	if field.Type == interfaces.DataType_Date || field.Type == interfaces.DataType_Time {
		_, err := filter_condition.ParseTemporalValue(field.Type, value, common.ServerTimeZone())
		return err
	}

	return nil
}
