// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
)

// convertValue converts driver byte slices to strings unless binary content must be preserved.
func convertValue(v any, preserveBinary bool) any {
	if b, ok := v.([]byte); ok {
		if preserveBinary {
			return b
		}
		return string(b)
	}
	return v
}

// BuildPagedSQL wraps validated read-only SQL with LIMIT and OFFSET.
// The inner query retains its own ordering and limits; the newline terminates trailing line comments.
func (c *MariaDBConnector) BuildPagedSQL(sqlStr string, offset, limit int) string {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = interfaces.DefaultPageLimit
	}
	return fmt.Sprintf("SELECT * FROM (%s\n) AS _raw_query_page LIMIT %d OFFSET %d", strings.TrimSpace(sqlStr), limit, offset)
}

// BuildCountSQL counts the full result of validated SQL, including any original limit.
// The newline terminates trailing line comments before the wrapper closes.
func (c *MariaDBConnector) BuildCountSQL(sqlStr string) string {
	return fmt.Sprintf("SELECT COUNT(*) AS _raw_query_total_count FROM (%s\n) AS _raw_query_total", strings.TrimSpace(sqlStr))
}

// ExecuteQuery runs a structured query using the resource schema and compiled filter.
// NeedTotal issues a separate count for detail queries; aggregate queries return their entries.
func (c *MariaDBConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {

	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, property := range resource.SchemaDefinition {
		fields[property.Name] = property
	}

	var condition sq.Sqlizer
	var err error
	if params.ActualFilterCond != nil {
		condition, err = c.ConvertFilterCondition(params.ActualFilterCond)
		if err != nil {
			return nil, err
		}
	}

	result := &interfaces.QueryResult{
		Entries: make([]map[string]any, 0),
	}

	builder, err := c.buildSelectBuilder(resource, params, fields, condition)
	if err != nil {
		return nil, err
	}

	// Serialize the statement and retain the converter's bound arguments.
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
		return nil, fmt.Errorf("failed to execute query: %w", mariadbQueryError(err))
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
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
			return nil, err
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = convertValue(values[i], binaryContentColumns[col])
		}
		result.Entries = append(result.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mariadbQueryError(err)
	}

	// Count the complete filtered detail result without pagination, rather than
	// treating the number of rows on this page as the total.
	if params.NeedTotal && !isAggregate {
		countBuilder := sq.Select("COUNT(1)").
			From(resource.SourceIdentifier)
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
			return nil, fmt.Errorf("failed to scan total: %w", mariadbQueryError(err))
		}
		result.Total = total
	}

	return result, nil
}

// ExecuteRawSQL executes SQL that has passed the caller's read-only and resource policy checks.
// Column types come from the driver, byte values become strings, and TotalCount is the returned row count.
func (c *MariaDBConnector) ExecuteRawSQL(ctx context.Context, sqlStr string) (*interfaces.RawQueryResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("execute query failed: %w", mariadbQueryError(err))
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

	// Map driver-reported column types to the response schema.
	for i, col := range columns {
		response.Columns[i] = interfaces.ColumnInfo{
			Name: col,
			Type: c.MapType(columnTypes[i].DatabaseTypeName()),
		}
	}

	// Scan rows into entries using the driver's column order.
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
			row[col] = convertValue(values[i], false)
		}
		response.Entries = append(response.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows failed: %w", mariadbQueryError(err))
	}

	totalCount := int64(len(response.Entries))
	response.TotalCount = &totalCount

	return response, nil
}

// buildSelectBuilder assembles projection, filtering, grouping, HAVING, sorting, and paging.
// Resource.SourceIdentifier is already quoted and is used directly as the table reference.
// Column and result-alias references use connector quoting, including reserved words such as key.
func (c *MariaDBConnector) buildSelectBuilder(resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams, fieldMap map[string]*interfaces.Property,
	condition sq.Sqlizer) (sq.SelectBuilder, error) {
	if params.Paging.Offset < 0 || params.Paging.Limit < 0 {
		return sq.SelectBuilder{}, fmt.Errorf("paging offset and limit must not be negative")
	}

	// Generated fields may lack OriginalName; use the property name rather than
	// producing an empty SQL identifier.
	originalName := func(property string) string {
		if field, ok := fieldMap[property]; ok && field.OriginalName != "" {
			return field.OriginalName
		}
		return property
	}

	selectFields := []string{}

	// Project grouping dimensions using their resource names as aliases.
	for _, groupByItem := range params.GroupBy {
		column := originalName(groupByItem.Property)
		// Calendar intervals use the same expression in projection and grouping.
		if groupByItem.CalendarInterval != "" {
			dateFmt := c.buildDateFormat(mariadbQuoteColumnName(column), groupByItem.CalendarInterval)
			selectFields = append(selectFields, dateFmt+" AS "+mariadbQuoteColumnName(groupByItem.Property))
		} else {
			selectFields = append(selectFields,
				mariadbQuoteColumnName(column)+" AS "+mariadbQuoteColumnName(groupByItem.Property))
		}
	}

	// Project the aggregate expression and its result alias.
	var aggAlias string
	if params.Aggregation != nil {
		aggField := mariadbQuoteColumnName(originalName(params.Aggregation.Property))

		aggFunc := params.Aggregation.Aggr
		switch aggFunc {
		case "count_distinct":
			aggFunc = "COUNT(DISTINCT " + aggField + ")"
		default:
			aggFunc = strings.ToUpper(aggFunc) + "(" + aggField + ")"
		}

		// Use __value when no aggregate alias is supplied.
		if params.Aggregation.Alias != "" {
			aggAlias = params.Aggregation.Alias
		} else {
			aggAlias = "__value"
		}

		selectFields = append(selectFields, aggFunc+" AS "+mariadbQuoteColumnName(aggAlias))
	} else if params.Having != nil && params.Having.Field == "count(*)" {
		// A count(*) HAVING clause requires an implicit COUNT(*) projection.
		aggAlias = "__value"
		selectFields = append(selectFields, "COUNT(*) AS "+mariadbQuoteColumnName(aggAlias))
	}

	// Detail projection follows the requested binary representation.
	selectField := func(name string) string {
		column := mariadbQuoteColumnName(originalName(name))
		if prop := fieldMap[name]; prop != nil && prop.Type == interfaces.DataType_Binary {
			if params.BinaryMode == nil {
				return "NULL AS " + mariadbQuoteColumnName(name)
			}
			switch *params.BinaryMode {
			case interfaces.BinaryModeMetadata:
				return "OCTET_LENGTH(" + column + ") AS " + mariadbQuoteColumnName(name)
			case interfaces.BinaryModeContent:
				return column + " AS " + mariadbQuoteColumnName(name)
			default:
				return "NULL AS " + mariadbQuoteColumnName(name)
			}
		}
		return column + " AS " + mariadbQuoteColumnName(name)
	}
	// OutputFields controls detail projection only. Aggregate projection consists
	// of grouping dimensions and the aggregate result; extra plain columns would
	// violate grouping semantics.
	if len(params.GroupBy) == 0 && params.Aggregation == nil && params.Having == nil {
		if len(params.OutputFields) > 0 {
			for _, outName := range params.OutputFields {
				selectFields = append(selectFields, selectField(outName))
			}
		} else if len(selectFields) == 0 {
			// Without an explicit projection, include all resource fields using
			// the same original-name fallback as requested fields.
			for _, prop := range resource.SchemaDefinition {
				selectFields = append(selectFields, selectField(prop.Name))
			}
		}
	}

	builder := sq.Select(selectFields...).
		From(resource.SourceIdentifier)

	// Apply the compiled filter before grouping.
	if condition != nil {
		builder = builder.Where(condition)
	}

	// Group by source expressions, including calendar intervals.
	if len(params.GroupBy) > 0 {
		groupByFields := []string{}
		for _, groupByItem := range params.GroupBy {
			column := mariadbQuoteColumnName(originalName(groupByItem.Property))
			// Calendar intervals use the same expression in projection and grouping.
			if groupByItem.CalendarInterval != "" {
				groupByFields = append(groupByFields,
					c.buildDateFormat(column, groupByItem.CalendarInterval))
			} else {
				groupByFields = append(groupByFields, column)
			}
		}
		builder = builder.GroupBy(groupByFields...)
	}

	// Apply HAVING only to queries with an aggregate result.
	if params.Having != nil && (params.Aggregation != nil || (params.Having.Field == "count(*)")) {
		havingCond, err := c.buildHavingCondition(params.Having, aggAlias)
		if err != nil {
			return builder, fmt.Errorf("failed to build HAVING condition: %w", err)
		}
		if havingCond != "" {
			builder = builder.Having(havingCond)
		}
	}

	for _, sortItem := range params.Sort {
		dir := "ASC"
		if sortItem.Direction == interfaces.DESC_DIRECTION {
			dir = "DESC"
		}

		// An aggregate alias takes precedence over a same-named resource property.
		// Resolving it as a source column can silently sort arbitrary rows when
		// ONLY_FULL_GROUP_BY is disabled.
		sortField := mariadbQuoteColumnName(originalName(sortItem.Field))
		if aggAlias != "" && sortItem.Field == aggAlias {
			sortField = mariadbQuoteColumnName(aggAlias)
		}

		// Calendar-group sorting uses the same expression as GROUP BY.
		for _, groupByItem := range params.GroupBy {
			if groupByItem.Property == sortItem.Field && groupByItem.CalendarInterval != "" {
				sortField = c.buildDateFormat(
					mariadbQuoteColumnName(originalName(groupByItem.Property)), groupByItem.CalendarInterval)
				break
			}
		}

		builder = builder.OrderBy(sortField + " " + dir)
	}

	// Core supplies the keyset filter; cursor queries omit offset pagination.
	if params.CursorEncoded == "" {
		builder = builder.Offset(uint64(params.Paging.Offset))
	}
	return builder.Limit(uint64(params.Paging.Limit)), nil
}

// mariadbQueryError identifies unknown-column errors while preserving the driver cause.
func mariadbQueryError(err error) error {
	var driverErr *mysql.MySQLError
	if errors.As(err, &driverErr) && driverErr.Number == 1054 {
		return interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, err)
	}
	return err
}

// buildHavingCondition renders HAVING for __value or count(*) using the selected aggregate alias.
func (c *MariaDBConnector) buildHavingCondition(having *interfaces.HavingClause, aggAlias string) (string, error) {
	if having.Field != "__value" && having.Field != "count(*)" {
		return "", fmt.Errorf("HAVING field must be '__value' or 'count(*)'")
	}

	// Quote the result alias consistently with SELECT, including reserved words.
	// count(*) is a fixed aggregate expression rather than an identifier.
	var fieldExpr string
	if having.Field == "count(*)" {
		fieldExpr = "COUNT(*)"
	} else {
		fieldExpr = mariadbQuoteColumnName(aggAlias)
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

	// Render scalar values as SQL literals; this path does not return bound arguments.
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

// formatInValues renders a comma-separated SQL value list for HAVING.
// String slices receive SQL quotes; other values are formatted directly.
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

// buildDateFormat groups a quoted date expression by a validated calendar interval.
// Minute through year use DATE_FORMAT; quarter uses a year-and-quarter label.
// The caller validates the interval before invoking this helper.
func (c *MariaDBConnector) buildDateFormat(dateField, calendarInterval string) string {
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
		dateFmt = fmt.Sprintf(`concat(year(%s),'-Q',quarter(%s))`, dateField, dateField)
	case interfaces.CALENDAR_UNIT_YEAR:
		dateFmt = fmt.Sprintf(`date_format(%s,'%s')`, dateField, `%Y`)
	}
	return dateFmt
}
