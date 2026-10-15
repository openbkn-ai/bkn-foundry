// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
)

// convertRawValue converts driver byte values to strings unless binary output is requested.
func convertRawValue(v any, preserveBinary bool) any {
	if b, ok := v.([]byte); ok {
		if preserveBinary {
			return b
		}
		return string(b)
	}
	return v
}

// convertValue converts zoned time values to the server time zone and preserves binary output when requested.
func convertValue(v any, colName string, origTypeMap map[string]string, preserveBinary bool) any {
	if v == nil {
		return nil
	}

	// Obtain the original type information from origTypeMap
	origType, ok := origTypeMap[colName]
	if !ok {
		return convertRawValue(v, preserveBinary)
	}

	// Only time types with time zones need to be converted
	// PostgreSQL primitive types: timestamptz, timetz, timestamp with time zone, time with time zone
	needsConversion := false
	switch origType {
	case "timestamptz", "timetz", "timestamp with time zone", "time with time zone":
		needsConversion = true
	}

	if !needsConversion {
		return convertRawValue(v, preserveBinary)
	}

	// Processing time type
	switch t := v.(type) {
	case time.Time:
		// Convert to the local time zone
		return t.Local()
	default:
		return convertRawValue(v, preserveBinary)
	}
}

// BuildPagedSQL applies PostgreSQL paging syntax to a validated query.
func (c *PostgresqlConnector) BuildPagedSQL(sql string, offset, limit int) string {
	return fmt.Sprintf("SELECT * FROM (%s) AS _raw_query_page LIMIT %d OFFSET %d", sql, limit, offset)
}

// BuildCountSQL applies PostgreSQL total-count syntax to a validated query.
func (c *PostgresqlConnector) BuildCountSQL(sql string) string {
	return fmt.Sprintf("SELECT COUNT(*) AS _raw_query_total_count FROM (%s) AS _raw_query_total", sql)
}

// ExecuteRawSQL executes validated source SQL and converts driver values for the response.
func (c *PostgresqlConnector) ExecuteRawSQL(ctx context.Context, sql string) (*interfaces.RawQueryResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for raw query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to execute raw query: %w", postgresqlQueryError(err))
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to read result columns: %w", err)
	}

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to read result column types: %w", err)
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
			return nil, fmt.Errorf("failed to scan result row: %w", err)
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = convertRawValue(values[i], false)
		}
		response.Entries = append(response.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate result rows: %w", postgresqlQueryError(err))
	}

	totalCount := int64(len(response.Entries))
	response.TotalCount = &totalCount

	return response, nil
}

// ExecuteQuery builds and executes a single-table query with filtering, grouping, sorting, and paging.
func (c *PostgresqlConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
	if params.Paging.Offset < 0 || params.Paging.Limit < 0 {
		return nil, fmt.Errorf("paging offset and limit must not be negative")
	}

	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for query: %w", err)
	}

	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}
	originalName := func(property string) string {
		if field, ok := fieldMap[property]; ok && field.OriginalName != "" {
			return field.OriginalName
		}
		return property
	}

	// Results are always aliased to property names, so retain original types by
	// the same logical names instead of re-reading source metadata.
	origTypeMap := make(map[string]string, len(resource.SchemaDefinition))
	for _, prop := range resource.SchemaDefinition {
		if prop.OriginalType != "" {
			origTypeMap[prop.Name] = prop.OriginalType
		}
	}

	var condition sq.Sqlizer
	var err error
	if params.ActualFilterCond != nil {
		condition, err = c.ConvertFilterCondition(params.ActualFilterCond)
		if err != nil {
			return nil, fmt.Errorf("failed to convert filter condition: %w", err)
		}
	}

	result := &interfaces.QueryResult{
		Entries: make([]map[string]any, 0),
	}

	// Construct the SELECT clause
	selectFields := []string{}

	// Add the GROUP BY field (when aggregating queries)
	for _, groupByItem := range params.GroupBy {
		column := pgQuoteColumnName(originalName(groupByItem.Property))
		if groupByItem.CalendarInterval != "" {
			selectFields = append(selectFields,
				c.buildDateFormat(column, groupByItem.CalendarInterval)+" AS "+pgQuoteColumnName(groupByItem.Property))
		} else {
			selectFields = append(selectFields, column+" AS "+pgQuoteColumnName(groupByItem.Property))
		}
	}

	// Add aggregated fields (when performing aggregated queries)
	var aggAlias, aggExpression string
	if params.Aggregation != nil {
		aggField := pgQuoteColumnName(originalName(params.Aggregation.Property))
		if params.Aggregation.Property == "*" && params.Aggregation.Aggr == "count" {
			aggField = "*"
		}

		// Determine the aggregation function
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

		aggExpression = aggFunc
		selectFields = append(selectFields, aggExpression+" AS "+pgQuoteColumnName(aggAlias))
	} else if params.Having != nil && params.Having.Field == "count(*)" {
		aggAlias, aggExpression = "__value", "COUNT(*)"
		selectFields = append(selectFields, aggExpression+" AS "+pgQuoteColumnName(aggAlias))
	}

	// Aggregate output aliases must not overwrite a grouping dimension.
	if aggExpression != "" {
		for _, group := range params.GroupBy {
			if group.Property == aggAlias {
				return nil, interfaces.NewConditionBuildError("aggregate alias conflicts with a group field")
			}
		}
	}

	// If it is not an aggregated query and GROUP BY is not specified, add all fields
	selectField := func(name string) string {
		column := pgQuoteColumnName(originalName(name))
		if prop := fieldMap[name]; prop != nil && prop.Type == interfaces.DataType_Binary {
			if params.BinaryMode == nil {
				return "NULL AS " + pgQuoteColumnName(name)
			}
			switch *params.BinaryMode {
			case interfaces.BinaryModeMetadata:
				return "octet_length(" + column + ") AS " + pgQuoteColumnName(name)
			case interfaces.BinaryModeContent:
				return column + " AS " + pgQuoteColumnName(name)
			default:
				return "NULL AS " + pgQuoteColumnName(name)
			}
		}
		return column + " AS " + pgQuoteColumnName(name)
	}
	if len(params.GroupBy) == 0 && params.Aggregation == nil && params.Having == nil {
		if len(params.OutputFields) > 0 {
			for _, field := range params.OutputFields {
				selectFields = append(selectFields, selectField(field))
			}
		} else if len(selectFields) == 0 {
			// If no output field is specified, all fields will be queried
			for _, prop := range resource.SchemaDefinition {
				selectFields = append(selectFields, selectField(prop.Name))
			}
		}
	}

	// Build query
	builder := stmtBuilder.Select(selectFields...).
		From(resource.SourceIdentifier)

	// Add the WHERE condition
	if condition != nil {
		builder = builder.Where(condition)
	}

	// Add GROUP BY (when aggregating queries)
	if len(params.GroupBy) > 0 {
		groupByFields := []string{}
		for _, groupByItem := range params.GroupBy {
			column := pgQuoteColumnName(originalName(groupByItem.Property))
			if groupByItem.CalendarInterval != "" {
				groupByFields = append(groupByFields, c.buildDateFormat(column, groupByItem.CalendarInterval))
			} else {
				groupByFields = append(groupByFields, column)
			}
		}
		builder = builder.GroupBy(groupByFields...)
	}

	// HAVING references aggregate expressions; PostgreSQL does not resolve result aliases here.
	if params.Having != nil {
		expression := aggExpression
		if params.Having.Field == "count(*)" {
			expression = "COUNT(*)"
		}
		having, err := c.buildHavingCondition(params.Having, expression)
		if err != nil {
			return nil, fmt.Errorf("failed to build HAVING condition: %w", err)
		}
		builder = builder.Having(having)
	}

	// Add ORDER BY
	if len(params.Sort) > 0 {
		for _, sortItem := range params.Sort {
			dir := "ASC"
			if sortItem.Direction == interfaces.DESC_DIRECTION {
				dir = "DESC"
			}
			sortField := pgQuoteColumnName(originalName(sortItem.Field))
			if aggAlias != "" && sortItem.Field == aggAlias {
				sortField = pgQuoteColumnName(aggAlias)
			}
			for _, groupByItem := range params.GroupBy {
				if groupByItem.Property == sortItem.Field && groupByItem.CalendarInterval != "" {
					sortField = c.buildDateFormat(pgQuoteColumnName(originalName(groupByItem.Property)), groupByItem.CalendarInterval)
					break
				}
			}
			builder = builder.OrderBy(sortField + " " + dir)
		}
	}

	// Add LIMIT and OFFSET
	if params.CursorEncoded == "" {
		builder = builder.Offset(uint64(params.Paging.Offset))
	}
	builder = builder.Limit(uint64(params.Paging.Limit))

	// Build SQL and execute it
	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	isAggregate := params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil
	if isAggregate {
		logger.Debugf("postgresql aggregate query: %s", table.SQLSummary(query, args))
	} else {
		logger.Debugf("postgresql query: %s", table.SQLSummary(query, args))
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", postgresqlQueryError(err))
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to read result columns: %w", err)
	}
	result.Columns = columns

	binaryContentColumns := map[string]bool{}
	if params.BinaryMode != nil && *params.BinaryMode == interfaces.BinaryModeContent {
		for _, prop := range resource.SchemaDefinition {
			if prop != nil && prop.Type == interfaces.DataType_Binary {
				binaryContentColumns[prop.Name] = true
			}
		}
	}
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan result row: %w", err)
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = convertValue(values[i], col, origTypeMap, binaryContentColumns[col])
		}
		result.Entries = append(result.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate result rows: %w", postgresqlQueryError(err))
	}

	// Release query rows before COUNT, including with a one-connection pool.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close query rows: %w", postgresqlQueryError(err))
	}
	// Count result groups after HAVING and before pagination, or filtered detail rows.
	if params.NeedTotal {
		countBuilder := stmtBuilder.Select("COUNT(1)").
			From(resource.SourceIdentifier)
		if isAggregate {
			countBuilder = stmtBuilder.Select("COUNT(*)").FromSelect(builder.RemoveLimit().RemoveOffset(), "_query_groups")
		} else if condition != nil {
			countBuilder = countBuilder.Where(condition)
		}
		countQuery, countArgs, countErr := countBuilder.ToSql()
		if countErr != nil {
			return nil, fmt.Errorf("failed to build count query: %w", countErr)
		}
		logger.Debugf("postgresql count query: %s", table.SQLSummary(countQuery, countArgs))
		var total int64
		row := c.db.QueryRowContext(ctx, countQuery, countArgs...)
		if err := row.Scan(&total); err != nil {
			return nil, fmt.Errorf("failed to scan total: %w", postgresqlQueryError(err))
		}
		result.Total = total
	}

	return result, nil
}

// postgresqlQueryError classifies unknown-column errors and preserves the source error chain.
func postgresqlQueryError(err error) error {
	var driverErr *pq.Error
	if errors.As(err, &driverErr) && string(driverErr.Code) == "42703" {
		return interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, err)
	}
	return err
}

// buildDateFormat formats a date field for the requested calendar grouping.
func (c *PostgresqlConnector) buildDateFormat(dateField, calendarInterval string) string {
	switch calendarInterval {
	case interfaces.CALENDAR_UNIT_MINUTE:
		return fmt.Sprintf(`to_char(date_trunc('minute',%s),'YYYY-MM-DD HH24:MI')`, dateField)
	case interfaces.CALENDAR_UNIT_HOUR:
		return fmt.Sprintf(`to_char(date_trunc('hour',%s),'YYYY-MM-DD HH24')`, dateField)
	case interfaces.CALENDAR_UNIT_DAY:
		return fmt.Sprintf(`to_char(date_trunc('day',%s),'YYYY-MM-DD')`, dateField)
	case interfaces.CALENDAR_UNIT_WEEK:
		return fmt.Sprintf(`to_char(date_trunc('week',%s),'IYYY-IW')`, dateField)
	case interfaces.CALENDAR_UNIT_MONTH:
		return fmt.Sprintf(`to_char(date_trunc('month',%s),'YYYY-MM')`, dateField)
	case interfaces.CALENDAR_UNIT_QUARTER:
		return fmt.Sprintf(`to_char(date_trunc('quarter',%s),'YYYY-"Q"Q')`, dateField)
	case interfaces.CALENDAR_UNIT_YEAR:
		return fmt.Sprintf(`to_char(date_trunc('year',%s),'YYYY')`, dateField)
	default:
		return ""
	}
}

// buildHavingCondition binds values to a validated aggregate expression.
func (c *PostgresqlConnector) buildHavingCondition(having *interfaces.HavingClause, expression string) (sq.Sqlizer, error) {
	if having.Field != "__value" && having.Field != "count(*)" {
		return nil, fmt.Errorf("HAVING field must be '__value' or 'count(*)'")
	}
	switch having.Operation {
	case "==", "=", "!=", ">", ">=", "<", "<=":
		op := having.Operation
		if op == "==" {
			op = "="
		}
		if op == "!=" {
			op = "<>"
		}
		if having.Value == nil {
			return nil, fmt.Errorf("HAVING comparison requires a value")
		}
		return sq.Expr(expression+" "+op+" ?", having.Value), nil
	case "in", "not_in", "range", "out_range":
		var values []any
		switch value := having.Value.(type) {
		case []any:
			values = value
		case []string:
			values = make([]any, len(value))
			for i, item := range value {
				values[i] = item
			}
		default:
			return nil, fmt.Errorf("HAVING operation requires an array")
		}
		if having.Operation == "range" || having.Operation == "out_range" {
			if len(values) != 2 {
				return nil, fmt.Errorf("HAVING range requires two values")
			}
			lowerOp, upperOp := ">=", "<="
			if having.Operation == "out_range" {
				lowerOp, upperOp = "<", ">"
			}
			lower := sq.Expr(expression+" "+lowerOp+" ?", values[0])
			upper := sq.Expr(expression+" "+upperOp+" ?", values[1])
			if having.Operation == "range" {
				return sq.And{lower, upper}, nil
			}
			return sq.Or{lower, upper}, nil
		}
		op := "IN"
		if having.Operation == "not_in" {
			op = "NOT IN"
		}
		if len(values) == 0 {
			if op == "IN" {
				return sq.Expr("1=0"), nil
			}
			return sq.Expr("1=1"), nil
		}
		return sq.Expr(expression+" "+op+" ("+sq.Placeholders(len(values))+")", values...), nil
	default:
		return nil, fmt.Errorf("unsupported HAVING operation: %s", having.Operation)
	}
}
