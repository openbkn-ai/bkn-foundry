// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package mariadb provides MariaDB database connector implementation.
package mariadb

import (
	"context"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/interfaces"
	"vega-backend/logics/connector/local/table"
)

// convertValue converts []byte to string for MariaDB driver compatibility
func convertValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// BuildPagedSQL applies MariaDB paging syntax to a validated query.
func (c *MariaDBConnector) BuildPagedSQL(sql string, offset, limit int) string {
	return fmt.Sprintf("SELECT * FROM (%s) AS _raw_query_page LIMIT %d OFFSET %d", sql, limit, offset)
}

// BuildCountSQL applies MariaDB total-count syntax to a validated query.
func (c *MariaDBConnector) BuildCountSQL(sql string) string {
	return fmt.Sprintf("SELECT COUNT(*) AS _raw_query_total_count FROM (%s) AS _raw_query_total", sql)
}

// ExecuteRawSQL executes the original SQL query
func (c *MariaDBConnector) ExecuteRawSQL(ctx context.Context, sql string) (*interfaces.RawQueryResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("execute query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("get columns failed: %w", err)
	}

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("get column types failed: %w", err)
	}

	response := &interfaces.RawQueryResponse{
		Columns: make([]interfaces.ColumnInfo, len(columns)),
		Entries: make([]map[string]any, 0),
	}

	// Fill column information
	for i, col := range columns {
		response.Columns[i] = interfaces.ColumnInfo{
			Name: col,
			Type: c.MapType(columnTypes[i].DatabaseTypeName()),
		}
	}

	// Read the result row
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("scan row failed: %w", err)
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = convertValue(values[i])
		}
		response.Entries = append(response.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows failed: %w", err)
	}

	totalCount := int64(len(response.Entries))
	response.TotalCount = &totalCount

	return response, nil
}

// buildSelectBuilder assembles the SELECT statement for detail and aggregate queries.
//
// Every identifier — table, column, alias — must go through quoteColumnName / qualTable.
// A source column named after a SQL reserved word (say `key`) survives DDL, catalog
// discover and bkn push untouched, because none of them build a query; concatenating it
// raw only fails at execution time with a 1064 that names nothing useful.
func (c *MariaDBConnector) buildSelectBuilder(resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams, fieldMap map[string]*interfaces.Property,
	condition sq.Sqlizer) (sq.SelectBuilder, error) {

	// Source column name. Fall back to the property name when the schema has no mapping,
	// and also when the property carries no original_name: build tasks add vector fields
	// with a Name and no OriginalName (appendTaskEmbeddingVectorFields), and without the
	// fallback those render as an empty quoted identifier.
	originalName := func(property string) string {
		if field, ok := fieldMap[property]; ok && field.OriginalName != "" {
			return field.OriginalName
		}
		return property
	}

	// Construct the SELECT clause
	selectFields := []string{}
	// Output names already selected (column or alias), used to de-duplicate output_fields
	selected := map[string]struct{}{}

	// Add the GROUP BY field (when aggregating queries)
	for _, groupByItem := range params.GroupBy {
		column := originalName(groupByItem.Property)
		// Check whether calendar_interval is needed
		if groupByItem.CalendarInterval != "" {
			dateFmt := c.buildDateFormat(groupByItem.Property, quoteColumnName(column), groupByItem.CalendarInterval)
			selectFields = append(selectFields, dateFmt+" AS "+quoteColumnName(groupByItem.Property))
			selected[groupByItem.Property] = struct{}{}
		} else {
			selectFields = append(selectFields, quoteColumnName(column))
			selected[groupByItem.Property] = struct{}{}
		}
	}

	// Add the aggregate field (when aggregating queries)
	var aggAlias string
	if params.Aggregation != nil {
		aggField := quoteColumnName(originalName(params.Aggregation.Property))

		// Determine the aggregate function
		aggFunc := params.Aggregation.Aggr
		switch aggFunc {
		case "count_distinct":
			aggFunc = "COUNT(DISTINCT " + aggField + ")"
		default:
			aggFunc = strings.ToUpper(aggFunc) + "(" + aggField + ")"
		}

		// Determine the alias
		if params.Aggregation.Alias != "" {
			aggAlias = params.Aggregation.Alias
		} else {
			aggAlias = "__value"
		}

		selectFields = append(selectFields, aggFunc+" AS "+quoteColumnName(aggAlias))
		selected[aggAlias] = struct{}{}
	} else if params.Having != nil && params.Having.Field == "count(*)" {
		// When HAVING uses count(*), add the COUNT(*) aggregate automatically
		aggAlias = "__value"
		selectFields = append(selectFields, "COUNT(*) AS "+quoteColumnName(aggAlias))
		selected[aggAlias] = struct{}{}
	}

	// Select every field when the query is neither aggregated nor grouped
	if len(params.GroupBy) == 0 && params.Aggregation == nil {
		if len(params.OutputFields) > 0 {
			for _, outName := range params.OutputFields {
				selectFields = append(selectFields, quoteColumnName(originalName(outName)))
			}
		} else if len(selectFields) == 0 {
			// No output fields specified, so query them all
			for _, prop := range resource.SchemaDefinition {
				selectFields = append(selectFields, quoteColumnName(prop.OriginalName))
			}
		}
	} else if len(params.OutputFields) > 0 {
		// For aggregate or GROUP BY queries, make sure output_fields end up in selectFields
		for _, outName := range params.OutputFields {
			if _, found := selected[outName]; found {
				continue
			}
			selectFields = append(selectFields, quoteColumnName(originalName(outName)))
			selected[outName] = struct{}{}
		}
	}

	// Build the query
	builder := sq.Select(selectFields...).From(qualTable(resource.SourceIdentifier))

	// Add the WHERE condition
	if condition != nil {
		builder = builder.Where(condition)
	}

	// Add GROUP BY (when aggregating queries)
	if len(params.GroupBy) > 0 {
		groupByFields := []string{}
		for _, groupByItem := range params.GroupBy {
			column := quoteColumnName(originalName(groupByItem.Property))
			// Check whether calendar_interval is needed
			if groupByItem.CalendarInterval != "" {
				groupByFields = append(groupByFields,
					c.buildDateFormat(groupByItem.Property, column, groupByItem.CalendarInterval))
			} else {
				groupByFields = append(groupByFields, column)
			}
		}
		builder = builder.GroupBy(groupByFields...)
	}

	// Add the HAVING condition (when aggregating queries)
	if params.Having != nil && (params.Aggregation != nil || (params.Having.Field == "count(*)")) {
		havingCond, err := c.buildHavingCondition(params.Having, aggAlias)
		if err != nil {
			return builder, fmt.Errorf("failed to build HAVING condition: %w", err)
		}
		if havingCond != "" {
			builder = builder.Having(havingCond)
		}
	}

	// Add ORDER BY
	for _, sortItem := range params.Sort {
		dir := "ASC"
		if sortItem.Direction == interfaces.DESC_DIRECTION {
			dir = "DESC"
		}

		// Check whether this is a GROUP BY field using calendar_interval
		sortField := quoteColumnName(originalName(sortItem.Field))
		for _, groupByItem := range params.GroupBy {
			if groupByItem.Property == sortItem.Field && groupByItem.CalendarInterval != "" {
				// Use the full date_format expression
				sortField = c.buildDateFormat(groupByItem.Property,
					quoteColumnName(originalName(groupByItem.Property)), groupByItem.CalendarInterval)
				break
			}
		}

		builder = builder.OrderBy(sortField + " " + dir)
	}

	// Add LIMIT and OFFSET
	if params.CursorEncoded == "" {
		builder = builder.Offset(uint64(params.Offset))
	}
	return builder.Limit(uint64(params.Limit)), nil
}

func (c *MariaDBConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {

	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	var condition sq.Sqlizer
	var err error
	if params.ActualFilterCond != nil {
		condition, err = c.ConvertFilterCondition(ctx, params.ActualFilterCond, fieldMap)
		if err != nil {
			return nil, err
		}
	}

	result := &interfaces.QueryResult{
		Entries: make([]map[string]any, 0),
	}

	builder, err := c.buildSelectBuilder(resource, params, fieldMap, condition)
	if err != nil {
		return nil, err
	}

	// Build SQL and execute it
	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	isAggregate := params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil
	if isAggregate {
		logger.Debugf("aggregate query: %s", table.SQLSummary(query, args))
	} else {
		logger.Debugf("query: %s", table.SQLSummary(query, args))
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result.Columns = columns

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = convertValue(values[i])
		}
		result.Entries = append(result.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Total processing (detail query only) : Independent COUNT query, aligned with the postgresql connector.
	// Previously, directly take len(result.Entries) - that is, the number of rows on this page. For tables with more than one page, total is always equal to
	// LIMIT (The progress bar of the build task shows "20802/1000", which indicates this bug)
	if params.NeedTotal && !isAggregate {
		countBuilder := sq.Select("COUNT(1)").From(qualTable(resource.SourceIdentifier))
		if condition != nil {
			countBuilder = countBuilder.Where(condition)
		}
		countQuery, countArgs, countErr := countBuilder.ToSql()
		if countErr != nil {
			return nil, fmt.Errorf("failed to build count query: %w", countErr)
		}
		logger.Debugf("count query: %s", table.SQLSummary(countQuery, countArgs))
		var total int64
		row := c.db.QueryRowContext(ctx, countQuery, countArgs...)
		if err := row.Scan(&total); err != nil {
			return nil, fmt.Errorf("failed to scan total: %w", err)
		}
		result.Total = total
	}

	return result, nil
}

// buildHavingCondition builds the HAVING condition
func (c *MariaDBConnector) buildHavingCondition(having *interfaces.HavingClause, aggAlias string) (string, error) {
	// Support the __value and count(*) fields
	if having.Field != "__value" && having.Field != "count(*)" {
		return "", fmt.Errorf("HAVING field must be '__value' or 'count(*)'")
	}

	// Determine the field expression used in the HAVING clause. The alias comes straight
	// from the request body and is not validated, so a reserved word passes SELECT — which
	// quotes it — and then fails HAVING with a 1064 unless it is quoted here too.
	var fieldExpr string
	if having.Field == "count(*)" {
		fieldExpr = "COUNT(*)"
	} else {
		fieldExpr = quoteColumnName(aggAlias)
	}

	var op string
	switch having.Operation {
	case "==":
		op = "="
	case "!=":
		op = "<>"
	case ">":
		op = ">"
	case ">=":
		op = ">="
	case "<":
		op = "<"
	case "<=":
		op = "<="
	case "in":
		return fmt.Sprintf("%s IN (%s)", fieldExpr, formatInValues(having.Value)), nil
	case "not_in":
		return fmt.Sprintf("%s NOT IN (%s)", fieldExpr, formatInValues(having.Value)), nil
	case "range":
		if values, ok := having.Value.([]any); ok && len(values) == 2 {
			return fmt.Sprintf("%s BETWEEN ? AND ?", fieldExpr), nil
		}
		return "", fmt.Errorf("range operation requires an array with 2 values")
	case "out_range":
		if values, ok := having.Value.([]any); ok && len(values) == 2 {
			return fmt.Sprintf("%s NOT BETWEEN ? AND ?", fieldExpr), nil
		}
		return "", fmt.Errorf("out_range operation requires an array with 2 values")
	default:
		return "", fmt.Errorf("unsupported HAVING operation: %s", having.Operation)
	}

	// Format the value of the HAVING condition
	var valueStr string
	switch v := having.Value.(type) {
	case string:
		valueStr = fmt.Sprintf("'%s'", v)
	case int, int64, float64:
		valueStr = fmt.Sprintf("%v", v)
	default:
		valueStr = fmt.Sprintf("%v", v)
	}
	return fmt.Sprintf("%s %s %s", fieldExpr, op, valueStr), nil
}

// Format IN Values: Format the list of values for the in operation
func formatInValues(value any) string {
	switch v := value.(type) {
	case []any:
		var values []string
		for _, item := range v {
			values = append(values, fmt.Sprintf("%v", item))
		}
		return strings.Join(values, ", ")
	case []string:
		var values []string
		for _, item := range v {
			values = append(values, fmt.Sprintf("'%s'", item))
		}
		return strings.Join(values, ", ")
	default:
		return fmt.Sprintf("%v", value)
	}
}

// buildDateFormat builds the date_format expression based on calendar_interval
// Support the calendar_interval enumeration values of OpenSearch: minute, hour, day, week, month, quarter, year
// Note: The validity of calendar_interval has been verified in the validateCalendarInterval method in validate_resource_data.go
func (c *MariaDBConnector) buildDateFormat(alias, dateField, calendarInterval string) string {
	var dateFmt string
	switch calendarInterval {
	case interfaces.CALENDAR_UNIT_MINUTE:
		dateFmt = fmt.Sprintf(`date_format(%s,'%s')`, dateField, `%Y-%m-%d %H:%i`)
	case interfaces.CALENDAR_UNIT_HOUR:
		dateFmt = fmt.Sprintf(`date_format(%s,'%s')`, dateField, `%Y-%m-%d %H`)
	case interfaces.CALENDAR_UNIT_DAY:
		dateFmt = fmt.Sprintf(`date_format(%s,'%s')`, dateField, `%Y-%m-%d`)
	case interfaces.CALENDAR_UNIT_WEEK:
		dateFmt = fmt.Sprintf(`date_format(%s,'%s')`, dateField, `%x-%v`)
	case interfaces.CALENDAR_UNIT_MONTH:
		dateFmt = fmt.Sprintf(`date_format(%s,'%s')`, dateField, `%Y-%m`)
	case interfaces.CALENDAR_UNIT_QUARTER:
		dateFmt = fmt.Sprintf(`format('%%d-Q%%d',year(%s),quarter(%s))`, dateField, dateField)
	case interfaces.CALENDAR_UNIT_YEAR:
		dateFmt = fmt.Sprintf(`date_format(%s,'%s')`, dateField, `%Y`)
	}
	return dateFmt
}
