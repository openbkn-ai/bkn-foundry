// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/interfaces"
	"vega-backend/logics/connector/local/table"
)

func convertRawValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// convertValue converts time values with time zones to the current time zone and handles other types
func convertValue(v any, colName string, origTypeMap map[string]string) any {
	if v == nil {
		return nil
	}

	// Obtain the original type information from origTypeMap
	origType, ok := origTypeMap[colName]
	if !ok {
		return convertRawValue(v)
	}

	// Only time types with time zones need to be converted
	// PostgreSQL primitive types: timestamptz, timetz, timestamp with time zone, time with time zone
	needsConversion := false
	switch origType {
	case "timestamptz", "timetz", "timestamp with time zone", "time with time zone":
		needsConversion = true
	}

	if !needsConversion {
		return convertRawValue(v)
	}

	// Processing time type
	switch t := v.(type) {
	case time.Time:
		// Convert to the local time zone
		return t.Local()
	default:
		return convertRawValue(v)
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

// ExecuteRawSQL executes the original SQL query
func (c *PostgresqlConnector) ExecuteRawSQL(ctx context.Context, sql string) (*interfaces.RawQueryResponse, error) {
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
			row[col] = convertValue(values[i], col, nil)
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

// ExecuteQuery performs single-table queries.
func (c *PostgresqlConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {

	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	// Build the origTypeMap in advance to only store the correspondence between column names and primitive types
	origTypeMap := map[string]string{}
	if resource.SourceMetadata != nil {
		if columnsAny, ok := resource.SourceMetadata["columns"].([]any); ok {
			for _, colAny := range columnsAny {
				if col, ok := colAny.(map[string]any); ok {
					if name, ok := col["name"].(string); ok {
						if origType, ok := col["original_type"].(string); ok {
							origTypeMap[name] = origType
						}
					}
				}
			}
		}
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

	tableRef := qualTable(resource)

	// Construct the SELECT clause
	selectFields := []string{}

	// Add the GROUP BY field (when aggregating queries)
	for _, groupByItem := range params.GroupBy {
		if field, ok := fieldMap[groupByItem.Property]; ok {
			if groupByItem.CalendarInterval != "" {
				selectFields = append(selectFields,
					c.buildDateFormat(field.OriginalName, groupByItem.CalendarInterval)+" AS "+groupByItem.Property)
			} else {
				selectFields = append(selectFields, field.OriginalName)
			}
		} else {
			selectFields = append(selectFields, groupByItem.Property)
		}
	}

	// Add aggregated fields (when performing aggregated queries)
	var aggAlias string
	if params.Aggregation != nil {
		aggField := params.Aggregation.Property
		if field, ok := fieldMap[aggField]; ok {
			aggField = field.OriginalName
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

		selectFields = append(selectFields, aggFunc+" AS "+aggAlias)
	}

	// If it is not an aggregated query and GROUP BY is not specified, add all fields
	if len(params.GroupBy) == 0 && params.Aggregation == nil {
		if len(params.OutputFields) > 0 {
			for _, field := range params.OutputFields {
				if prop, ok := fieldMap[field]; ok {
					selectFields = append(selectFields, prop.OriginalName)
				} else {
					selectFields = append(selectFields, field)
				}
			}
		} else if len(selectFields) == 0 {
			// If no output field is specified, all fields will be queried
			for _, prop := range resource.SchemaDefinition {
				selectFields = append(selectFields, prop.OriginalName)
			}
		}
	}

	// Build query
	builder := pgSq.Select(selectFields...).From(tableRef)

	// Add the WHERE condition
	if condition != nil {
		builder = builder.Where(condition)
	}

	// Add GROUP BY (when aggregating queries)
	if len(params.GroupBy) > 0 {
		groupByFields := []string{}
		for _, groupByItem := range params.GroupBy {
			if field, ok := fieldMap[groupByItem.Property]; ok {
				if groupByItem.CalendarInterval != "" {
					groupByFields = append(groupByFields, c.buildDateFormat(field.OriginalName, groupByItem.CalendarInterval))
				} else {
					groupByFields = append(groupByFields, field.OriginalName)
				}
			} else {
				groupByFields = append(groupByFields, groupByItem.Property)
			}
		}
		builder = builder.GroupBy(groupByFields...)
	}

	// Add a HAVING condition (when aggregating queries)
	if params.Having != nil && params.Aggregation != nil {
		havingCond, havingErr := c.buildHavingCondition(params.Having, aggAlias)
		if havingErr != nil {
			return nil, fmt.Errorf("failed to build HAVING condition: %w", havingErr)
		}
		if havingCond != "" {
			builder = builder.Where(havingCond)
		}
	}

	// Add ORDER BY
	if len(params.Sort) > 0 {
		for _, sortItem := range params.Sort {
			dir := "ASC"
			if sortItem.Direction == interfaces.DESC_DIRECTION {
				dir = "DESC"
			}
			builder = builder.OrderBy(sortItem.Field + " " + dir)
		}
	}

	// Add LIMIT and OFFSET
	if params.CursorEncoded == "" {
		builder = builder.Offset(uint64(params.Offset))
	}
	builder = builder.Limit(uint64(params.Limit))

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
			row[col] = convertValue(values[i], col, origTypeMap)
		}
		result.Entries = append(result.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Total number of processed items (for detailed inquiries only)
	if params.NeedTotal && !isAggregate {
		countBuilder := pgSq.Select("COUNT(1)").From(tableRef)
		if condition != nil {
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
			return nil, fmt.Errorf("failed to scan total: %w", err)
		}
		result.Total = total
	}

	return result, nil
}

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

// buildHavingCondition builds the HAVING condition
func (c *PostgresqlConnector) buildHavingCondition(having *interfaces.HavingClause, aggAlias string) (string, error) {
	if having.Field != "__value" {
		return "", fmt.Errorf("HAVING field must be '__value'")
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
		return fmt.Sprintf("%s IN (%s)", aggAlias, formatInValues(having.Value)), nil
	case "not_in":
		return fmt.Sprintf("%s NOT IN (%s)", aggAlias, formatInValues(having.Value)), nil
	case "range":
		if values, ok := having.Value.([]any); ok && len(values) == 2 {
			return fmt.Sprintf("%s BETWEEN ? AND ?", aggAlias), nil
		}
		return "", fmt.Errorf("range operation requires an array with 2 values")
	case "out_range":
		if values, ok := having.Value.([]any); ok && len(values) == 2 {
			return fmt.Sprintf("%s NOT BETWEEN ? AND ?", aggAlias), nil
		}
		return "", fmt.Errorf("out_range operation requires an array with 2 values")
	default:
		return "", fmt.Errorf("unsupported HAVING operation: %s", having.Operation)
	}

	return fmt.Sprintf("%s %s ?", aggAlias, op), nil
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
