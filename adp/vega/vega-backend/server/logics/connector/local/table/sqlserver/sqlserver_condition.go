// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
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
	case *filter_condition.RangeCond:
		return sq.And{sq.GtOrEq{quoteIdentifier(value.Lfield.OriginalName): value.Value[0]},
			sq.LtOrEq{quoteIdentifier(value.Lfield.OriginalName): value.Value[1]}}, nil
	case *filter_condition.OutRangeCond:
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
	case *filter_condition.TrueCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): true}, nil
	case *filter_condition.FalseCond:
		return sq.Eq{quoteIdentifier(value.Lfield.OriginalName): false}, nil
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
	return sq.Expr(quoteIdentifier(left.OriginalName)+" "+operator+" ?", value), nil
}
