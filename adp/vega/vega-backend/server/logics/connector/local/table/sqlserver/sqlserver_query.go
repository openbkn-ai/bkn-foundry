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
)

// ExecuteQuery executes a parameterized table query.
func (c *SQLServerConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {

	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, property := range resource.SchemaDefinition {
		fields[property.Name] = property
	}
	selectFields, groupFields, aggregateAlias, aggregateExpression, isAggregate, err :=
		buildSQLServerProjection(resource, params, fields)
	if err != nil {
		return nil, err
	}
	if len(selectFields) == 0 {
		return nil, fmt.Errorf("resource schema has no queryable fields")
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.AtP).
		Select(selectFields...).
		From(qualifiedTable(resource.SourceIdentifier))
	var condition sq.Sqlizer
	if params.ActualFilterCond != nil {
		var err error
		condition, err = c.convertFilterCondition(ctx, params.ActualFilterCond, fields)
		if err != nil {
			return nil, err
		}
		builder = builder.Where(condition)
	}
	if len(groupFields) > 0 {
		builder = builder.GroupBy(groupFields...)
	}
	having, err := buildSQLServerHaving(params.Having, aggregateExpression)
	if err != nil {
		return nil, err
	}
	if having != nil {
		builder = builder.Having(having)
	}
	if len(params.Sort) > 0 {
		for _, sort := range params.Sort {
			property, ok := fields[sort.Field]
			sortExpression := ""
			if ok {
				sortExpression = quoteIdentifier(property.OriginalName)
			} else if isAggregate && sort.Field == aggregateAlias {
				sortExpression = quoteIdentifier(aggregateAlias)
			} else {
				return nil, fmt.Errorf("sort field is not defined by resource schema: %s", sort.Field)
			}
			direction := "ASC"
			if sort.Direction == interfaces.DESC_DIRECTION {
				direction = "DESC"
			}
			builder = builder.OrderBy(sortExpression + " " + direction)
		}
	} else {
		// OFFSET/FETCH requires ORDER BY. This neutral expression avoids using an
		// untrusted column name; callers requiring stable cursor order provide sort.
		builder = builder.OrderBy("(SELECT 1)")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = interfaces.DEFAULT_DATA_LIMIT
	}
	builder = builder.Suffix("OFFSET ? ROWS FETCH NEXT ? ROWS ONLY", params.Offset, limit)
	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build sqlserver query: %w", err)
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result, err := scanQueryRows(rows)
	if err != nil {
		return nil, err
	}
	if params.NeedTotal && !isAggregate {
		countBuilder := sq.StatementBuilder.
			PlaceholderFormat(sq.AtP).
			Select("COUNT(1)").
			From(qualifiedTable(resource.SourceIdentifier))
		if condition != nil {
			countBuilder = countBuilder.Where(condition)
		}
		countQuery, countArgs, err := countBuilder.ToSql()
		if err != nil {
			return nil, fmt.Errorf("failed to build count query: %w", err)
		}
		if err := c.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&result.Total); err != nil {
			return nil, fmt.Errorf("failed to query total: %w", err)
		}
	}
	return result, nil
}

func buildSQLServerProjection(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams,
	fields map[string]*interfaces.Property) ([]string, []string, string, string, bool, error) {
	if len(params.GroupBy) == 0 && params.Aggregation == nil {
		selected := params.OutputFields
		if len(selected) == 0 {
			selected = make([]string, 0, len(resource.SchemaDefinition))
			for _, property := range resource.SchemaDefinition {
				selected = append(selected, property.Name)
			}
		}
		projection := make([]string, 0, len(selected))
		for _, name := range selected {
			property, ok := fields[name]
			if !ok {
				return nil, nil, "", "", false, fmt.Errorf("output field is not defined by resource schema: %s", name)
			}
			projection = append(projection, quoteIdentifier(property.OriginalName))
		}
		return projection, nil, "", "", false, nil
	}

	projection := make([]string, 0, len(params.GroupBy)+1)
	groupFields := make([]string, 0, len(params.GroupBy))
	for _, group := range params.GroupBy {
		if group.CalendarInterval != "" {
			return nil, nil, "", "", false, fmt.Errorf("sqlserver calendar_interval %q is not supported", group.CalendarInterval)
		}
		property, ok := fields[group.Property]
		if !ok {
			return nil, nil, "", "", false, fmt.Errorf("group field is not defined by resource schema: %s", group.Property)
		}
		field := quoteIdentifier(property.OriginalName)
		projection = append(projection, field)
		groupFields = append(groupFields, field)
	}

	alias, expression := "", ""
	if params.Aggregation != nil {
		property, ok := fields[params.Aggregation.Property]
		if !ok {
			return nil, nil, "", "", false, fmt.Errorf("aggregation field is not defined by resource schema: %s", params.Aggregation.Property)
		}
		function := strings.ToUpper(params.Aggregation.Aggr)
		switch params.Aggregation.Aggr {
		case "count_distinct":
			expression = "COUNT(DISTINCT " + quoteIdentifier(property.OriginalName) + ")"
		case "count", "sum", "avg", "min", "max":
			expression = function + "(" + quoteIdentifier(property.OriginalName) + ")"
		default:
			return nil, nil, "", "", false, fmt.Errorf("unsupported aggregation: %s", params.Aggregation.Aggr)
		}
		alias = params.Aggregation.Alias
		if alias == "" {
			alias = "__value"
		}
		projection = append(projection, expression+" AS "+quoteIdentifier(alias))
	}
	if len(projection) == 0 {
		return nil, nil, "", "", false, fmt.Errorf("aggregate query has no projection")
	}
	return projection, groupFields, alias, expression, true, nil
}

func buildSQLServerHaving(having *interfaces.HavingClause, expression string) (sq.Sqlizer, error) {
	if having == nil {
		return nil, nil
	}
	if having.Field != "__value" || expression == "" {
		return nil, fmt.Errorf("HAVING requires aggregation field __value")
	}
	operators := map[string]string{"==": "=", "!=": "<>", ">": ">", ">=": ">=", "<": "<", "<=": "<="}
	if operator, ok := operators[having.Operation]; ok {
		return sq.Expr(expression+" "+operator+" ?", having.Value), nil
	}
	values, ok := having.Value.([]any)
	if !ok || len(values) != 2 {
		return nil, fmt.Errorf("HAVING operation %s requires two values", having.Operation)
	}
	switch having.Operation {
	case "range":
		return sq.Expr(expression+" BETWEEN ? AND ?", values[0], values[1]), nil
	case "out_range":
		return sq.Expr(expression+" NOT BETWEEN ? AND ?", values[0], values[1]), nil
	default:
		return nil, fmt.Errorf("unsupported HAVING operation: %s", having.Operation)
	}
}
