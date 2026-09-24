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

var Special = strings.NewReplacer(`\`, `\\\\`, `'`, `\'`, `%`, `\%`, `_`, `\_`)

// ConvertFilterCondition dispatches a filter condition to its SQL converter.
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
		return nil, filter_condition.NewUnsupportedOperationError(condition.GetOperation(), filter_condition.QueryChannelSQL)
	}
}

// ConvertFilterConditionEqual builds an equality predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionEqual(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.EqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EqualCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if interfaces.DataType_IsDate(cond.Lfield.Type) {
			return dateCompareExpr(cond.Lfield, "=", cond.Value)
		}
		return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
	case interfaces.ValueFrom_Field:
		return sq.Expr(quoteColumnName(cond.Lfield.OriginalName) + " = " + quoteColumnName(cond.Rfield.OriginalName)), nil
	default:
		return nil, fmt.Errorf("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionNotEqual builds an inequality predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionNotEqual(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotEqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEqualCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if interfaces.DataType_IsDate(cond.Lfield.Type) {
			return dateCompareExpr(cond.Lfield, "<>", cond.Value)
		}
		return sq.NotEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
	case interfaces.ValueFrom_Field:
		return sq.Expr(quoteColumnName(cond.Lfield.OriginalName) + " <> " + quoteColumnName(cond.Rfield.OriginalName)), nil
	default:
		return nil, fmt.Errorf("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionGt builds a greater-than predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionGt(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.GtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GtCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if interfaces.DataType_IsDate(cond.Lfield.Type) {
			return dateCompareExpr(cond.Lfield, ">", cond.Value)
		}
		return sq.Gt{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
	case interfaces.ValueFrom_Field:
		return sq.Expr(quoteColumnName(cond.Lfield.OriginalName) + " > " + quoteColumnName(cond.Rfield.OriginalName)), nil
	default:
		return nil, fmt.Errorf("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionGte builds a greater-than-or-equal predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionGte(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.GteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GteCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if interfaces.DataType_IsDate(cond.Lfield.Type) {
			return dateCompareExpr(cond.Lfield, ">=", cond.Value)
		}
		return sq.GtOrEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
	case interfaces.ValueFrom_Field:
		return sq.Expr(quoteColumnName(cond.Lfield.OriginalName) + " >= " + quoteColumnName(cond.Rfield.OriginalName)), nil
	default:
		return nil, fmt.Errorf("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionLt builds a less-than predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionLt(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.LtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LtCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if interfaces.DataType_IsDate(cond.Lfield.Type) {
			return dateCompareExpr(cond.Lfield, "<", cond.Value)
		}
		return sq.Lt{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
	case interfaces.ValueFrom_Field:
		return sq.Expr(quoteColumnName(cond.Lfield.OriginalName) + " < " + quoteColumnName(cond.Rfield.OriginalName)), nil
	default:
		return nil, fmt.Errorf("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionLte builds a less-than-or-equal predicate for a constant or another field.
func (c *MariaDBConnector) ConvertFilterConditionLte(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.LteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LteCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if interfaces.DataType_IsDate(cond.Lfield.Type) {
			return dateCompareExpr(cond.Lfield, "<=", cond.Value)
		}
		return sq.LtOrEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
	case interfaces.ValueFrom_Field:
		return sq.Expr(quoteColumnName(cond.Lfield.OriginalName) + " <= " + quoteColumnName(cond.Rfield.OriginalName)), nil
	default:
		return nil, fmt.Errorf("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionIn builds a predicate matching one of the supplied values.
func (c *MariaDBConnector) ConvertFilterConditionIn(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.InCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.InCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [in] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateSetExpr(cond.Lfield, "IN", cond.Value)
	}

	return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
}

// ConvertFilterConditionNotIn builds a predicate excluding the supplied values.
func (c *MariaDBConnector) ConvertFilterConditionNotIn(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotInCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotInCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [not_in] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		return dateSetExpr(cond.Lfield, "NOT IN", cond.Value)
	}

	return sq.NotEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value}, nil
}

// ConvertFilterConditionLike builds a substring LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionLike(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.LikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LikeCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [like] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	vStr := "%" + Special.Replace(cond.Value) + "%"
	return sq.Like{quoteColumnName(cond.Lfield.OriginalName): vStr}, nil
}

// ConvertFilterConditionNotLike builds a negated substring LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionNotLike(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotLikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotLikeCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [not_like] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	vStr := "%" + Special.Replace(cond.Value) + "%"
	return sq.NotLike{quoteColumnName(cond.Lfield.OriginalName): vStr}, nil
}

// ConvertFilterConditionRegex builds a regular-expression predicate.
func (c *MariaDBConnector) ConvertFilterConditionRegex(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.RegexCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RegexCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [regex] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	return sq.Expr(quoteColumnName(cond.Lfield.OriginalName)+" REGEXP ?", cond.Value), nil
}

// ConvertFilterConditionContain requires every supplied item to occur in a comma-separated field.
func (c *MariaDBConnector) ConvertFilterConditionContain(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.ContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ContainCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [contain] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	exprs := make(sq.And, 0, len(cond.Value))
	column := quoteColumnName(cond.Lfield.OriginalName)
	for _, value := range cond.Value {
		exprs = append(exprs, sq.Expr("FIND_IN_SET(?, "+column+") > 0", value))
	}
	return exprs, nil
}

// ConvertFilterConditionNotContain matches when a supplied item is absent from a comma-separated field.
func (c *MariaDBConnector) ConvertFilterConditionNotContain(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotContainCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [not_contain] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	exprs := make(sq.Or, 0, len(cond.Value))
	column := quoteColumnName(cond.Lfield.OriginalName)
	for _, value := range cond.Value {
		exprs = append(exprs, sq.Expr("FIND_IN_SET(?, "+column+") = 0", value))
	}
	return exprs, nil
}

// ConvertFilterConditionRange builds an inclusive lower-and-upper-bound predicate.
func (c *MariaDBConnector) ConvertFilterConditionRange(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.RangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RangeCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [range] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	if len(cond.Value) != 2 {
		return nil, fmt.Errorf("range condition requires exactly 2 values")
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		lower, err := dateCompareExpr(cond.Lfield, ">=", cond.Value[0])
		if err != nil {
			return nil, err
		}
		upper, err := dateCompareExpr(cond.Lfield, "<=", cond.Value[1])
		if err != nil {
			return nil, err
		}
		return sq.And{lower, upper}, nil
	}

	return sq.And{
		sq.GtOrEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value[0]},
		sq.LtOrEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value[1]},
	}, nil
}

// ConvertFilterConditionOutRange builds a predicate outside the supplied bounds.
func (c *MariaDBConnector) ConvertFilterConditionOutRange(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.OutRangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OutRangeCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [out_range] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	if len(cond.Value) != 2 {
		return nil, fmt.Errorf("out_range condition requires exactly 2 values")
	}

	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		lower, err := dateCompareExpr(cond.Lfield, "<", cond.Value[0])
		if err != nil {
			return nil, err
		}
		upper, err := dateCompareExpr(cond.Lfield, ">", cond.Value[1])
		if err != nil {
			return nil, err
		}
		return sq.Or{lower, upper}, nil
	}

	return sq.Or{
		sq.Lt{quoteColumnName(cond.Lfield.OriginalName): cond.Value[0]},
		sq.Gt{quoteColumnName(cond.Lfield.OriginalName): cond.Value[1]},
	}, nil
}

// ConvertFilterConditionBetween builds an inclusive predicate between two values.
func (c *MariaDBConnector) ConvertFilterConditionBetween(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.BetweenCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BetweenCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [between] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	if len(cond.Value) != 2 {
		return nil, fmt.Errorf("between condition requires exactly 2 values")
	}

	// Check if the field is of time type. If it is, convert the long type value to a timestamp
	if interfaces.DataType_IsDate(cond.Lfield.Type) {
		lower, err := dateCompareExpr(cond.Lfield, ">=", cond.Value[0])
		if err != nil {
			return nil, err
		}
		upper, err := dateCompareExpr(cond.Lfield, "<=", cond.Value[1])
		if err != nil {
			return nil, err
		}
		return sq.And{lower, upper}, nil
	}

	// For non-time type fields, parameterized queries can be directly used
	return sq.And{
		sq.GtOrEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value[0]},
		sq.LtOrEq{quoteColumnName(cond.Lfield.OriginalName): cond.Value[1]},
	}, nil
}

// ConvertFilterConditionNull builds an IS NULL predicate.
func (c *MariaDBConnector) ConvertFilterConditionNull(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NullCond")
	}

	return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): nil}, nil
}

// ConvertFilterConditionNotNull builds an IS NOT NULL predicate.
func (c *MariaDBConnector) ConvertFilterConditionNotNull(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotNullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotNullCond")
	}

	return sq.NotEq{quoteColumnName(cond.Lfield.OriginalName): nil}, nil
}

// ConvertFilterConditionEmpty matches an empty string.
func (c *MariaDBConnector) ConvertFilterConditionEmpty(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.EmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EmptyCond")
	}

	return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): ""}, nil
}

// ConvertFilterConditionNotEmpty excludes empty strings.
func (c *MariaDBConnector) ConvertFilterConditionNotEmpty(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotEmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEmptyCond")
	}

	return sq.NotEq{quoteColumnName(cond.Lfield.OriginalName): ""}, nil
}

// ConvertFilterConditionPrefix builds a prefix LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionPrefix(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.PrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.PrefixCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [prefix] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	vStr := Special.Replace(cond.Value) + "%"
	return sq.Like{quoteColumnName(cond.Lfield.OriginalName): vStr}, nil
}

// ConvertFilterConditionNotPrefix builds a negated prefix LIKE predicate with escaped input.
func (c *MariaDBConnector) ConvertFilterConditionNotPrefix(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotPrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotPrefixCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [not_prefix] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	vStr := Special.Replace(cond.Value) + "%"
	return sq.NotLike{quoteColumnName(cond.Lfield.OriginalName): vStr}, nil
}

// ConvertFilterConditionExist matches non-NULL field values.
func (c *MariaDBConnector) ConvertFilterConditionExist(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.ExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ExistCond")
	}

	return sq.NotEq{quoteColumnName(cond.Lfield.OriginalName): nil}, nil
}

// ConvertFilterConditionNotExist matches NULL field values.
func (c *MariaDBConnector) ConvertFilterConditionNotExist(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.NotExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotExistCond")
	}

	return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): nil}, nil
}

// ConvertFilterConditionTrue matches a true Boolean or nonzero TINYINT(1) value.
func (c *MariaDBConnector) ConvertFilterConditionTrue(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.TrueCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.TrueCond")
	}

	if cond.Lfield.Type == interfaces.DataType_Boolean {
		return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): true}, nil
	}

	if !isNumericBoolean(cond.Lfield) {
		return nil, fmt.Errorf("mariadb true condition requires BOOLEAN or TINYINT(1): %s", cond.Lfield.Name)
	}

	return sq.NotEq{quoteColumnName(cond.Lfield.OriginalName): 0}, nil
}

// ConvertFilterConditionFalse matches a false Boolean or zero TINYINT(1) value.
func (c *MariaDBConnector) ConvertFilterConditionFalse(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.FalseCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.FalseCond")
	}

	if cond.Lfield.Type == interfaces.DataType_Boolean {
		return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): false}, nil
	}

	if !isNumericBoolean(cond.Lfield) {
		return nil, fmt.Errorf("mariadb false condition requires BOOLEAN or TINYINT(1): %s", cond.Lfield.Name)
	}

	return sq.Eq{quoteColumnName(cond.Lfield.OriginalName): 0}, nil
}

// ConvertFilterConditionBefore matches values before the specified interval relative to now.
func (c *MariaDBConnector) ConvertFilterConditionBefore(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.BeforeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BeforeCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [before] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	if len(cond.Value) != 2 {
		return nil, fmt.Errorf("before condition requires exactly 2 values")
	}

	interval, ok := common.NumberAsFloat64(cond.Value[0])
	if !ok {
		return nil, fmt.Errorf("condition [before] interval value should be a number")
	}
	unit, ok := cond.Value[1].(string)
	if !ok {
		return nil, fmt.Errorf("condition [before] unit value should be a string")
	}

	return sq.Expr(quoteColumnName(cond.Lfield.OriginalName)+" < DATE_SUB(NOW(), INTERVAL ? "+unit+")", int(interval)), nil
}

// ConvertFilterConditionCurrent matches values in the current calendar interval.
func (c *MariaDBConnector) ConvertFilterConditionCurrent(condition interfaces.FilterCondition) (sq.Sqlizer, error) {
	cond, ok := condition.(*filter_condition.CurrentCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.CurrentCond")
	}

	if cond.Cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("condition [current] only supports ValueFrom_Const, got %s", cond.Cfg.ValueFrom)
	}

	col := quoteColumnName(cond.Lfield.OriginalName)
	var dateFormat string
	switch cond.Value {
	case filter_condition.CurrentYear:
		dateFormat = "%Y"
	case filter_condition.CurrentMonth:
		dateFormat = "%Y-%m"
	case filter_condition.CurrentWeek:
		dateFormat = "%Y-%u"
	case filter_condition.CurrentDay:
		dateFormat = "%Y-%m-%d"
	case filter_condition.CurrentHour:
		dateFormat = "%Y-%m-%d %H"
	case filter_condition.CurrentMinute:
		dateFormat = "%Y-%m-%d %H:%i"
	default:
		return nil, fmt.Errorf("condition [current] unsupported format: %s", cond.Value)
	}

	return sq.Expr("DATE_FORMAT(" + col + ", '" + dateFormat + "') = DATE_FORMAT(NOW(), '" + dateFormat + "')"), nil
}

// isNumericBoolean recognizes the numeric type stored for BOOL aliases.
func isNumericBoolean(field *interfaces.Property) bool {
	return field.Type == interfaces.DataType_Integer && strings.EqualFold(strings.TrimSpace(field.OriginalType), "tinyint(1)")
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

// dateValueExpr selects a placeholder expression for a MariaDB date or time value.
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

// validateDateValue checks that a value is compatible with the field's date or time type.
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
func dateCompareExpr(field *interfaces.Property, op string, value any) (sq.Sqlizer, error) {
	if err := validateDateValue(field, value); err != nil {
		return nil, err
	}
	return sq.Expr(
		quoteColumnName(field.OriginalName)+" "+op+" "+dateValueExpr(field, value),
		normalizeTimestampValue(value),
	), nil
}

// dateSetExpr builds an IN or NOT IN expression for bound date or time values.
func dateSetExpr(field *interfaces.Property, op string, values []any) (sq.Sqlizer, error) {
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
		quoteColumnName(field.OriginalName)+" "+op+" ("+strings.Join(valueExprs, ", ")+")",
		args...,
	), nil
}

// quoteColumnName converts column names to SQL identifiers; Support "alias.col" -> "alias.col"
func quoteColumnName(name string) string {
	if name == "" {
		return "``"
	}
	if idx := strings.Index(name, "."); idx >= 0 {
		alias := strings.TrimSpace(name[:idx])
		col := strings.TrimSpace(name[idx+1:])
		return "`" + strings.ReplaceAll(alias, "`", "``") + "`." + "`" + strings.ReplaceAll(col, "`", "``") + "`"
	}
	return "`" + strings.ReplaceAll(strings.TrimSpace(name), "`", "``") + "`"
}

// qualTable converts a resource source identifier into a backtick-qualified table name;
// it supports "db.table" -> "`db`.`table`".
func qualTable(sourceIdentifier string) string {
	return quoteColumnName(sourceIdentifier)
}
