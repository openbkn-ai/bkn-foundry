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

	sq "github.com/Masterminds/squirrel"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// likeEscaper escapes backslashes, quotes, and LIKE wildcards in literal pattern values.
var likeEscaper = strings.NewReplacer(`\`, `\\\\`, `'`, `\'`, `%`, `\%`, `_`, `\_`)

// ConvertFilterCondition compiles a validated filter tree into parameterized SQL.
// Callers resolve field references and value sources before invoking the converter.
func (c *MariaDBConnector) ConvertFilterCondition(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
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
func (c *MariaDBConnector) ConvertFilterConditionAnd(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.AndCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.AndCond")
	}
	if len(cond.SubConds) == 0 {
		return nil, fmt.Errorf("AND requires child conditions")
	}
	convertedConds := sq.And{}
	for _, subCond := range cond.SubConds {
		convertedCond, err := c.ConvertFilterCondition(subCond)
		if err != nil {
			return nil, err
		}
		convertedConds = append(convertedConds, convertedCond)
	}
	return convertedConds, nil
}

// ConvertFilterConditionOr combines converted child conditions with OR.
func (c *MariaDBConnector) ConvertFilterConditionOr(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.OrCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OrCond")
	}
	if len(cond.SubConds) == 0 {
		return nil, fmt.Errorf("OR requires child conditions")
	}
	convertedConds := sq.Or{}
	for _, subCond := range cond.SubConds {
		convertedCond, err := c.ConvertFilterCondition(subCond)
		if err != nil {
			return nil, err
		}
		convertedConds = append(convertedConds, convertedCond)
	}
	return convertedConds, nil
}

// ConvertFilterConditionWithOpr dispatches a non-composite filter operation to its SQL converter.
func (c *MariaDBConnector) ConvertFilterConditionWithOpr(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
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
func (c *MariaDBConnector) ConvertFilterConditionEqual(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.EqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EqualCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Cfg.ValueFrom == interfaces.ValueFrom_Field {
		rhs := mariadbQuotedColumn(cond.Rfield)
		return sq.Expr(lhs + " = " + rhs), nil
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateCompareExpr(lhs, cond.Lfield, "=", cond.Value)
	}
	return sq.Eq{lhs: cond.Value}, nil
}

// ConvertFilterConditionNotEqual builds an inequality predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionNotEqual(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotEqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEqualCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Cfg.ValueFrom == interfaces.ValueFrom_Field {
		rhs := mariadbQuotedColumn(cond.Rfield)
		return sq.Expr(lhs + " <> " + rhs), nil
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateCompareExpr(lhs, cond.Lfield, "<>", cond.Value)
	}
	return sq.NotEq{lhs: cond.Value}, nil
}

// ConvertFilterConditionGt builds a greater-than predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionGt(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.GtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GtCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Cfg.ValueFrom == interfaces.ValueFrom_Field {
		rhs := mariadbQuotedColumn(cond.Rfield)
		return sq.Expr(lhs + " > " + rhs), nil
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateCompareExpr(lhs, cond.Lfield, ">", cond.Value)
	}
	return sq.Gt{lhs: cond.Value}, nil
}

// ConvertFilterConditionGte builds a greater-than-or-equal predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionGte(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.GteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GteCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Cfg.ValueFrom == interfaces.ValueFrom_Field {
		rhs := mariadbQuotedColumn(cond.Rfield)
		return sq.Expr(lhs + " >= " + rhs), nil
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateCompareExpr(lhs, cond.Lfield, ">=", cond.Value)
	}
	return sq.GtOrEq{lhs: cond.Value}, nil
}

// ConvertFilterConditionLt builds a less-than predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionLt(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.LtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LtCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Cfg.ValueFrom == interfaces.ValueFrom_Field {
		rhs := mariadbQuotedColumn(cond.Rfield)
		return sq.Expr(lhs + " < " + rhs), nil
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateCompareExpr(lhs, cond.Lfield, "<", cond.Value)
	}
	return sq.Lt{lhs: cond.Value}, nil
}

// ConvertFilterConditionLte builds a less-than-or-equal predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionLte(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.LteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LteCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Cfg.ValueFrom == interfaces.ValueFrom_Field {
		rhs := mariadbQuotedColumn(cond.Rfield)
		return sq.Expr(lhs + " <= " + rhs), nil
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateCompareExpr(lhs, cond.Lfield, "<=", cond.Value)
	}
	return sq.LtOrEq{lhs: cond.Value}, nil
}

// ConvertFilterConditionIn builds a predicate matching one of the supplied values.
func (c *MariaDBConnector) ConvertFilterConditionIn(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.InCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.InCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateSetExpr(lhs, cond.Lfield, "IN", cond.Value)
	}
	return sq.Eq{lhs: cond.Value}, nil
}

// ConvertFilterConditionNotIn builds a predicate excluding the supplied values.
func (c *MariaDBConnector) ConvertFilterConditionNotIn(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotInCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotInCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateSetExpr(lhs, cond.Lfield, "NOT IN", cond.Value)
	}
	return sq.NotEq{lhs: cond.Value}, nil
}

// ConvertFilterConditionLike builds a substring LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionLike(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.LikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LikeCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	pattern := "%" + likeEscaper.Replace(cond.Value) + "%"
	return sq.Like{lhs: pattern}, nil
}

// ConvertFilterConditionNotLike builds a negated substring LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionNotLike(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotLikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotLikeCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	pattern := "%" + likeEscaper.Replace(cond.Value) + "%"
	return sq.NotLike{lhs: pattern}, nil
}

// ConvertFilterConditionRegex builds a regular-expression predicate.
func (c *MariaDBConnector) ConvertFilterConditionRegex(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.RegexCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RegexCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.Expr(lhs+" REGEXP ?", cond.Value), nil
}

// ConvertFilterConditionContain requires every supplied item to occur in a comma-separated field.
func (c *MariaDBConnector) ConvertFilterConditionContain(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.ContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ContainCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	exprs := make(sq.And, 0, len(cond.Value))
	for _, value := range cond.Value {
		exprs = append(exprs, sq.Expr("FIND_IN_SET(?, "+lhs+") > 0", value))
	}
	return exprs, nil
}

// ConvertFilterConditionNotContain matches when a supplied item is absent from a comma-separated field.
func (c *MariaDBConnector) ConvertFilterConditionNotContain(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotContainCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	exprs := make(sq.Or, 0, len(cond.Value))
	for _, value := range cond.Value {
		exprs = append(exprs, sq.Expr("FIND_IN_SET(?, "+lhs+") = 0", value))
	}
	return exprs, nil
}

// ConvertFilterConditionRange builds inclusive lower and upper bounds joined with AND.
func (c *MariaDBConnector) ConvertFilterConditionRange(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.RangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RangeCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		lower, err := dateCompareExpr(lhs, cond.Lfield, ">=", cond.Value[0])
		if err != nil {
			return nil, err
		}
		upper, err := dateCompareExpr(lhs, cond.Lfield, "<=", cond.Value[1])
		if err != nil {
			return nil, err
		}
		return sq.And{lower, upper}, nil
	}
	return sq.And{
		sq.GtOrEq{lhs: cond.Value[0]},
		sq.LtOrEq{lhs: cond.Value[1]},
	}, nil
}

// ConvertFilterConditionOutRange builds exclusive outer bounds joined with OR.
func (c *MariaDBConnector) ConvertFilterConditionOutRange(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.OutRangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OutRangeCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		lower, err := dateCompareExpr(lhs, cond.Lfield, "<", cond.Value[0])
		if err != nil {
			return nil, err
		}
		upper, err := dateCompareExpr(lhs, cond.Lfield, ">", cond.Value[1])
		if err != nil {
			return nil, err
		}
		return sq.Or{lower, upper}, nil
	}
	return sq.Or{
		sq.Lt{lhs: cond.Value[0]},
		sq.Gt{lhs: cond.Value[1]},
	}, nil
}

// ConvertFilterConditionBetween builds an inclusive predicate between two values.
func (c *MariaDBConnector) ConvertFilterConditionBetween(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.BetweenCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BetweenCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	// Date bounds use the same value conversion as scalar date comparisons.
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		lower, err := dateCompareExpr(lhs, cond.Lfield, ">=", cond.Value[0])
		if err != nil {
			return nil, err
		}
		upper, err := dateCompareExpr(lhs, cond.Lfield, "<=", cond.Value[1])
		if err != nil {
			return nil, err
		}
		return sq.And{lower, upper}, nil
	}
	// Bind non-date bounds directly without changing their source values.
	return sq.And{
		sq.GtOrEq{lhs: cond.Value[0]},
		sq.LtOrEq{lhs: cond.Value[1]},
	}, nil
}

// ConvertFilterConditionNull builds an IS NULL predicate.
func (c *MariaDBConnector) ConvertFilterConditionNull(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NullCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.Eq{lhs: nil}, nil
}

// ConvertFilterConditionNotNull builds an IS NOT NULL predicate.
func (c *MariaDBConnector) ConvertFilterConditionNotNull(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotNullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotNullCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.NotEq{lhs: nil}, nil
}

// ConvertFilterConditionEmpty matches an empty string.
func (c *MariaDBConnector) ConvertFilterConditionEmpty(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.EmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EmptyCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.Eq{lhs: ""}, nil
}

// ConvertFilterConditionNotEmpty excludes empty strings.
func (c *MariaDBConnector) ConvertFilterConditionNotEmpty(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotEmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEmptyCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.NotEq{lhs: ""}, nil
}

// ConvertFilterConditionPrefix builds a prefix LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionPrefix(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.PrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.PrefixCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	pattern := likeEscaper.Replace(cond.Value) + "%"
	return sq.Like{lhs: pattern}, nil
}

// ConvertFilterConditionNotPrefix builds a negated prefix LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionNotPrefix(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotPrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotPrefixCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	pattern := likeEscaper.Replace(cond.Value) + "%"
	return sq.NotLike{lhs: pattern}, nil
}

// ConvertFilterConditionExist matches non-NULL field values.
func (c *MariaDBConnector) ConvertFilterConditionExist(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.ExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ExistCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.NotEq{lhs: nil}, nil
}

// ConvertFilterConditionNotExist matches NULL field values.
func (c *MariaDBConnector) ConvertFilterConditionNotExist(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotExistCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.Eq{lhs: nil}, nil
}

// ConvertFilterConditionTrue matches a true Boolean or nonzero TINYINT(1) value.
func (c *MariaDBConnector) ConvertFilterConditionTrue(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.TrueCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.TrueCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Lfield.Type == interfaces.DataType_Boolean {
		return sq.Eq{lhs: true}, nil
	}
	if !isNumericBoolean(cond.Lfield) {
		return nil, fmt.Errorf("mariadb true condition requires BOOLEAN or TINYINT(1): %s", cond.Lfield.Name)
	}
	// TINYINT(1) uses zero for false and any nonzero value for true.
	return sq.NotEq{lhs: 0}, nil
}

// ConvertFilterConditionFalse matches a false Boolean or zero TINYINT(1) value.
func (c *MariaDBConnector) ConvertFilterConditionFalse(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.FalseCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.FalseCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	if cond.Lfield.Type == interfaces.DataType_Boolean {
		return sq.Eq{lhs: false}, nil
	}
	if !isNumericBoolean(cond.Lfield) {
		return nil, fmt.Errorf("mariadb false condition requires BOOLEAN or TINYINT(1): %s", cond.Lfield.Name)
	}
	return sq.Eq{lhs: 0}, nil
}

// ConvertFilterConditionBefore matches values before the specified interval relative to now.
func (c *MariaDBConnector) ConvertFilterConditionBefore(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.BeforeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BeforeCond")
	}

	interval, ok := common.NumberAsInt64(cond.Value[0])
	if !ok {
		return nil, fmt.Errorf("condition [before] interval value should be an integer")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("condition [before] interval must be a positive integer")
	}
	unit, ok := cond.Value[1].(string)
	if !ok {
		return nil, fmt.Errorf("condition [before] unit value should be a string")
	}
	if !calendarUnit(unit) {
		return nil, fmt.Errorf("invalid before unit")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)
	return sq.Expr(lhs+" < DATE_SUB(NOW(), INTERVAL ? "+strings.ToUpper(unit)+")", interval), nil
}

// ConvertFilterConditionCurrent matches values in the current calendar interval.
func (c *MariaDBConnector) ConvertFilterConditionCurrent(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.CurrentCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.CurrentCond")
	}

	lhs := mariadbQuotedColumn(cond.Lfield)

	var dateFormat string
	switch cond.Value {
	case filter_condition.CurrentYear:
		dateFormat = "%Y"
	case filter_condition.CurrentMonth:
		dateFormat = "%Y-%m"
	case filter_condition.CurrentWeek:
		// Pair the week-based year with the Monday-based week to preserve cross-year weeks.
		dateFormat = "%x-%v"
	case filter_condition.CurrentDay:
		dateFormat = "%Y-%m-%d"
	case filter_condition.CurrentHour:
		dateFormat = "%Y-%m-%d %H"
	case filter_condition.CurrentMinute:
		dateFormat = "%Y-%m-%d %H:%i"
	default:
		return nil, fmt.Errorf("condition [current] unsupported format: %s", cond.Value)
	}

	return sq.Expr("DATE_FORMAT(" + lhs + ", '" + dateFormat + "') = DATE_FORMAT(NOW(), '" + dateFormat + "')"), nil
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

// dateValueExpr selects the SQL expression for a bound date or time value.
// Epoch milliseconds use FROM_UNIXTIME(?/1000); TIME and parsed timestamps use a plain placeholder.
func dateValueExpr(field *interfaces.Property, value any) string {
	if field.Type == interfaces.DataType_Time {
		return "?"
	}
	if _, ok := value.(time.Time); ok {
		return "?"
	}
	if value, ok := value.(string); ok {
		if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
			return "?"
		}
	}
	return "FROM_UNIXTIME(?/1000)"
}

// validateDateValue checks supported date and time value representations.
// TIME requires a string; other date types accept epoch milliseconds, RFC3339 text, or time.Time.
func validateDateValue(field *interfaces.Property, value any) error {
	if value == nil {
		return nil
	}
	if field.Type == interfaces.DataType_Time {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("MariaDB time field %q requires a time string, got %T", field.Name, value)
		}
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

// dateCompareExpr builds a comparison against a bound date or time value.
func dateCompareExpr(lhs string, field *interfaces.Property, op string, value any) (sq.Sqlizer, error) {
	if err := validateDateValue(field, value); err != nil {
		return nil, err
	}
	return sq.Expr(
		lhs+" "+op+" "+dateValueExpr(field, value),
		normalizeTimestampValue(value),
	), nil
}

// dateSetExpr builds an IN or NOT IN expression for bound date or time values.
func dateSetExpr(lhs string, field *interfaces.Property, op string, values []any) (sq.Sqlizer, error) {
	valueExprs := make([]string, len(values))
	args := make([]any, len(values))
	for i, value := range values {
		if err := validateDateValue(field, value); err != nil {
			return nil, err
		}
		valueExprs[i] = dateValueExpr(field, value)
		args[i] = normalizeTimestampValue(value)
	}
	return sq.Expr(
		lhs+" "+op+" ("+strings.Join(valueExprs, ", ")+")",
		args...,
	), nil
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
