// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"context"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`, `[`, `\[`)

func (c *SQLServerConnector) convertFilterCondition(ctx context.Context, condition interfaces.FilterCondition,
	fields map[string]*interfaces.Property) (sq.Sqlizer, error) {
	switch value := condition.(type) {
	case *filter_condition.AndCond:
		return c.convertSubConditions(ctx, value.SubConds, fields, true)
	case *filter_condition.OrCond:
		return c.convertSubConditions(ctx, value.SubConds, fields, false)
	case *filter_condition.EqualCond:
		return binaryCondition(value.Cfg, value.Lfield, value.Rfield, value.Value, "=")
	case *filter_condition.NotEqualCond:
		return binaryCondition(value.Cfg, value.Lfield, value.Rfield, value.Value, "<>")
	case *filter_condition.GtCond:
		return binaryCondition(value.Cfg, value.Lfield, value.Rfield, value.Value, ">")
	case *filter_condition.GteCond:
		return binaryCondition(value.Cfg, value.Lfield, value.Rfield, value.Value, ">=")
	case *filter_condition.LtCond:
		return binaryCondition(value.Cfg, value.Lfield, value.Rfield, value.Value, "<")
	case *filter_condition.LteCond:
		return binaryCondition(value.Cfg, value.Lfield, value.Rfield, value.Value, "<=")
	case *filter_condition.InCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): value.Value}, nil
	case *filter_condition.NotInCond:
		return sq.NotEq{quoteIdentifier(value.Lfield.OriginalName): value.Value}, nil
	case *filter_condition.LikeCond:
		return sq.Expr(quoteIdentifier(value.Lfield.OriginalName)+` LIKE ? ESCAPE '\'`,
			"%"+likeEscaper.Replace(value.Value)+"%"), nil
	case *filter_condition.NotLikeCond:
		return sq.Expr(quoteIdentifier(value.Lfield.OriginalName)+` NOT LIKE ? ESCAPE '\'`,
			"%"+likeEscaper.Replace(value.Value)+"%"), nil
	case *filter_condition.ContainCond:
		return containConditions(value.Lfield, value.Value, false), nil
	case *filter_condition.NotContainCond:
		return containConditions(value.Lfield, value.Value, true), nil
	case *filter_condition.RangeCond:
		return betweenCondition(value.Lfield, value.Value)
	case *filter_condition.OutRangeCond:
		if len(value.Value) != 2 {
			return nil, fmt.Errorf("out_range condition requires exactly 2 values")
		}
		if interfaces.DataType_IsDate(value.Lfield.Type) {
			return sq.Or{
				sqlServerDateComparison(value.Lfield.OriginalName, "<", value.Value[0]),
				sqlServerDateComparison(value.Lfield.OriginalName, ">", value.Value[1]),
			}, nil
		}
		return sq.Or{sq.Lt{quoteIdentifier(value.Lfield.OriginalName): value.Value[0]},
			sq.Gt{quoteIdentifier(value.Lfield.OriginalName): value.Value[1]}}, nil
	case *filter_condition.NullCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): nil}, nil
	case *filter_condition.NotNullCond:
		return sq.NotEq{quoteIdentifier(value.Lfield.OriginalName): nil}, nil
	case *filter_condition.EmptyCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): ""}, nil
	case *filter_condition.NotEmptyCond:
		return sq.NotEq{quoteIdentifier(value.Lfield.OriginalName): ""}, nil
	case *filter_condition.PrefixCond:
		return prefixCondition(value.Lfield, value.Value, false), nil
	case *filter_condition.NotPrefixCond:
		return prefixCondition(value.Lfield, value.Value, true), nil
	case *filter_condition.BetweenCond:
		return betweenCondition(value.Lfield, value.Value)
	case *filter_condition.ExistCond:
		return sq.NotEq{quoteIdentifier(value.Lfield.OriginalName): nil}, nil
	case *filter_condition.NotExistCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): nil}, nil
	case *filter_condition.TrueCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): true}, nil
	case *filter_condition.FalseCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): false}, nil
	case *filter_condition.BeforeCond:
		return beforeCondition(value.Lfield, value.Value)
	case *filter_condition.CurrentCond:
		return currentCondition(value.Lfield, value.Value)
	default:
		return nil, fmt.Errorf("sqlserver filter operation %q is not supported", condition.GetOperation())
	}
}

func (c *SQLServerConnector) convertSubConditions(ctx context.Context, conditions []interfaces.FilterCondition,
	fields map[string]*interfaces.Property, conjunction bool) (sq.Sqlizer, error) {
	converted := make([]sq.Sqlizer, 0, len(conditions))
	for _, condition := range conditions {
		item, err := c.convertFilterCondition(ctx, condition, fields)
		if err != nil {
			return nil, err
		}
		converted = append(converted, item)
	}
	if conjunction {
		return sq.And(converted), nil
	}
	return sq.Or(converted), nil
}

func binaryCondition(cfg *interfaces.FilterCondCfg, left, right *interfaces.Property, value any, operator string) (sq.Sqlizer, error) {
	if cfg.ValueFrom == interfaces.ValueFrom_Field {
		if right == nil {
			return nil, fmt.Errorf("right field is required for field comparison")
		}
		return sq.Expr(quoteIdentifier(left.OriginalName) + " " + operator + " " + quoteIdentifier(right.OriginalName)), nil
	}
	if cfg.ValueFrom != interfaces.ValueFrom_Const {
		return nil, fmt.Errorf("value_from %q is not supported", cfg.ValueFrom)
	}
	if interfaces.DataType_IsDate(left.Type) {
		return sqlServerDateComparison(left.OriginalName, operator, value), nil
	}
	return sq.Expr(quoteIdentifier(left.OriginalName)+" "+operator+" ?", value), nil
}

func containConditions(field *interfaces.Property, values []any, negate bool) sq.Sqlizer {
	conditions := make([]sq.Sqlizer, 0, len(values))
	column := quoteIdentifier(field.OriginalName)
	for _, value := range values {
		operator := "EXISTS"
		if negate {
			operator = "NOT EXISTS"
		}
		conditions = append(conditions, sq.Expr(
			operator+" (SELECT 1 FROM STRING_SPLIT(CAST("+column+" AS nvarchar(max)), ',') AS _split WHERE _split.value = ?)",
			fmt.Sprint(value),
		))
	}
	if negate {
		return sq.Or(conditions)
	}
	return sq.And(conditions)
}

func prefixCondition(field *interfaces.Property, value string, negate bool) sq.Sqlizer {
	operator := "LIKE"
	if negate {
		operator = "NOT LIKE"
	}
	return sq.Expr(quoteIdentifier(field.OriginalName)+" "+operator+` ? ESCAPE '\'`, likeEscaper.Replace(value)+"%")
}

func betweenCondition(field *interfaces.Property, values []any) (sq.Sqlizer, error) {
	if len(values) != 2 {
		return nil, fmt.Errorf("between condition requires exactly 2 values")
	}
	if interfaces.DataType_IsDate(field.Type) {
		return sq.And{
			sqlServerDateComparison(field.OriginalName, ">=", values[0]),
			sqlServerDateComparison(field.OriginalName, "<=", values[1]),
		}, nil
	}
	return sq.And{
		sq.GtOrEq{quoteIdentifier(field.OriginalName): values[0]},
		sq.LtOrEq{quoteIdentifier(field.OriginalName): values[1]},
	}, nil
}

func sqlServerDateComparison(field, operator string, value any) sq.Sqlizer {
	return sq.Expr(
		quoteIdentifier(field)+" "+operator+" DATEADD_BIG(millisecond, ?, CONVERT(datetime2, '1970-01-01T00:00:00', 126))",
		value,
	)
}

func beforeCondition(field *interfaces.Property, values []any) (sq.Sqlizer, error) {
	if len(values) != 2 {
		return nil, fmt.Errorf("before condition requires exactly 2 values")
	}
	interval, ok := numericInterval(values[0])
	if !ok {
		return nil, fmt.Errorf("condition [before] interval value should be a number")
	}
	unit, ok := values[1].(string)
	if !ok {
		return nil, fmt.Errorf("condition [before] unit value should be a string")
	}
	datePart, err := sqlServerDatePart(unit)
	if err != nil {
		return nil, err
	}
	return sq.Expr(
		quoteIdentifier(field.OriginalName)+" < DATEADD("+datePart+", -?, SYSDATETIME())",
		interval,
	), nil
}

func numericInterval(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed >= 0 && typed == float64(int64(typed))
	case int:
		return int64(typed), typed >= 0
	case int64:
		return typed, typed >= 0
	default:
		return 0, false
	}
}

func currentCondition(field *interfaces.Property, unit string) (sq.Sqlizer, error) {
	datePart, err := sqlServerDatePart(unit)
	if err != nil {
		return nil, err
	}
	column := quoteIdentifier(field.OriginalName)
	return sq.Expr("DATETRUNC(" + datePart + ", " + column + ") = DATETRUNC(" + datePart + ", SYSDATETIME())"), nil
}

func sqlServerDatePart(unit string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case interfaces.CALENDAR_UNIT_YEAR:
		return "year", nil
	case interfaces.CALENDAR_UNIT_QUARTER:
		return "quarter", nil
	case interfaces.CALENDAR_UNIT_MONTH:
		return "month", nil
	case interfaces.CALENDAR_UNIT_WEEK:
		return "week", nil
	case interfaces.CALENDAR_UNIT_DAY:
		return "day", nil
	case interfaces.CALENDAR_UNIT_HOUR:
		return "hour", nil
	case interfaces.CALENDAR_UNIT_MINUTE:
		return "minute", nil
	default:
		return "", fmt.Errorf("unsupported calendar unit: %s", unit)
	}
}
