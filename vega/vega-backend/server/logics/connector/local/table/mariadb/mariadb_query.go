// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/go-sql-driver/mysql"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
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
// NeedTotal counts all matching rows or aggregate groups before pagination.
func (c *MariaDBConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {

	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for query: %w", err)
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
			return nil, fmt.Errorf("failed to convert filter condition: %w", err)
		}
	}

	builder, err := c.buildSelectBuilder(resource, params, fields, condition)
	if err != nil {
		return nil, fmt.Errorf("failed to build select query: %w", err)
	}

	// Serialize the statement and retain the converter's bound arguments.
	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", mariadbQueryError(err))
	}
	defer func() { _ = rows.Close() }()

	binaryContentColumns := table.QueryBinaryContentColumns(resource, params)
	result, err := c.scanQueryRows(rows, binaryContentColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to scan query rows: %w", err)
	}
	// Release query rows before COUNT, including with a one-connection pool.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close query rows: %w", mariadbQueryError(err))
	}

	isAggregate := params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil
	// Count the complete filtered result, including GROUP BY and HAVING.
	if params.NeedTotal {
		countBuilder := sq.Select("COUNT(*)").
			From(resource.SourceIdentifier)
		if isAggregate {
			countBuilder = sq.Select("COUNT(*)").FromSelect(builder.RemoveLimit().RemoveOffset(), "_query_groups")
		} else if condition != nil {
			countBuilder = countBuilder.Where(condition)
		}
		countQuery, countArgs, err := countBuilder.ToSql()
		if err != nil {
			return nil, fmt.Errorf("failed to build count query: %w", err)
		}

		var total int64
		row := c.db.QueryRowContext(ctx, countQuery, countArgs...)
		if err := row.Scan(&total); err != nil {
			return nil, fmt.Errorf("failed to scan total: %w", mariadbQueryError(err))
		}
		result.Total = total
	}

	table.NormalizeQueryResult(result, resource, params)
	return result, nil
}

// ExecuteRawSQL executes SQL that has passed the caller's read-only and resource policy checks.
// 列类型来自驱动；binary 列保留原始字节，其他值沿用原生查询转换，TotalCount 为返回行数。
func (c *MariaDBConnector) ExecuteRawSQL(ctx context.Context, sqlStr string) (*interfaces.RawQueryResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for raw query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to execute raw query: %w", mariadbQueryError(err))
	}
	defer func() { _ = rows.Close() }()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to read result column types: %w", err)
	}

	// binary 列保留原始字节，避免文本转换损坏内容；JSON 编码输出 base64。
	binaryColumns := make(map[string]bool)
	for _, column := range columnTypes {
		if c.MapType(column.DatabaseTypeName()) == interfaces.DataType_Binary {
			binaryColumns[column.Name()] = true
		}
	}
	result, err := c.scanQueryRows(rows, binaryColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to scan raw query rows: %w", err)
	}

	columns := make([]interfaces.ColumnInfo, len(columnTypes))
	for i, column := range columnTypes {
		columns[i] = interfaces.ColumnInfo{
			Name: column.Name(),
			Type: c.MapType(column.DatabaseTypeName()),
		}
	}
	totalCount := int64(len(result.Entries))

	response := &interfaces.RawQueryResponse{
		Columns:    columns,
		Entries:    result.Entries,
		TotalCount: &totalCount,
	}

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

	selectFields, groupByFields := []string{}, []string{}

	// Project grouping dimensions using their resource names as aliases.
	for _, groupByItem := range params.GroupBy {
		column := originalName(groupByItem.Property)
		// Calendar intervals use the same expression in projection and grouping.
		if groupByItem.CalendarInterval != "" {
			dateFmt := c.buildDateFormat(mariadbQuoteColumnName(column), groupByItem.CalendarInterval)
			selectFields = append(selectFields, dateFmt+" AS "+mariadbQuoteColumnName(groupByItem.Property))
			groupByFields = append(groupByFields, dateFmt)
		} else {
			selectFields = append(selectFields,
				mariadbQuoteColumnName(column)+" AS "+mariadbQuoteColumnName(groupByItem.Property))
			groupByFields = append(groupByFields, mariadbQuoteColumnName(column))
		}
	}

	// Project the aggregate expression and its result alias.
	var aggAlias string
	aggResultField := &interfaces.Property{Type: interfaces.DataType_Integer}
	if params.Aggregation != nil {
		aggField := "*"
		if params.Aggregation.Property != "*" {
			aggField = mariadbQuoteColumnName(originalName(params.Aggregation.Property))
		}
		if params.Aggregation.Aggr == "min" || params.Aggregation.Aggr == "max" {
			aggResultField = fieldMap[params.Aggregation.Property]
		}

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

	if err := table.ValidateAggregateAlias(aggAlias, params.GroupBy); err != nil {
		return sq.SelectBuilder{}, err
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
				if fieldMap[outName] == nil {
					return sq.SelectBuilder{}, interfaces.NewOutputFieldError(interfaces.OutputFieldUnknown, outName)
				}
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

	if len(selectFields) == 0 {
		return sq.SelectBuilder{}, interfaces.NewOutputFieldError(interfaces.OutputFieldNoneQueryable, "")
	}

	builder := sq.Select(selectFields...).
		From(resource.SourceIdentifier)

	// Apply the compiled filter before grouping.
	if condition != nil {
		builder = builder.Where(condition)
	}

	// Reuse projection expressions for grouping.
	builder = builder.GroupBy(groupByFields...)

	// Apply HAVING only to queries with an aggregate result.
	if params.Having != nil && (params.Aggregation != nil || (params.Having.Field == "count(*)")) {
		expression, field := mariadbQuoteColumnName(aggAlias), aggResultField
		if params.Having.Field == "count(*)" {
			expression = "COUNT(*)"
			field = &interfaces.Property{Type: interfaces.DataType_Integer}
		}
		havingCond, err := c.buildHavingCondition(params.Having, expression, field)
		if err != nil {
			return builder, fmt.Errorf("failed to build HAVING condition: %w", err)
		}
		if havingCond != nil {
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

// scanQueryRows reads result rows and preserves binary content only for selected columns.
// It leaves Total unchanged; callers determine totals according to the query contract.
func (c *MariaDBConnector) scanQueryRows(rows *sql.Rows, binaryContentColumns map[string]bool) (*interfaces.QueryResult, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to read result columns: %w", err)
	}
	result := &interfaces.QueryResult{
		Columns: columns,
		Entries: make([]map[string]any, 0),
	}
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}

		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("failed to scan result row: %w", err)
		}

		entry := make(map[string]any)
		for i, column := range columns {
			entry[column] = convertValue(values[i], binaryContentColumns[column])
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate result rows: %w", mariadbQueryError(err))
	}

	return result, nil
}

// mariadbQueryError 按源端错误码区分查询输入错误，保留驱动原因供诊断，不透传原文到 API。
func mariadbQueryError(err error) error {
	var driverErr *mysql.MySQLError
	if errors.As(err, &driverErr) {
		switch driverErr.Number {
		case 1054: // ER_BAD_FIELD_ERROR
			return interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, err)
		case 1052, 1055, 1056, 1060, 1064, 1111, 1140, 1210, 1221, 1241, 1582, 1583, 1584, 3065:
			// 歧义/重复列、语法、分组、函数参数和 DISTINCT 排序错误；不将连接或资源失效归为输入错误。
			return interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterInvalidQuery, err)
		}
	}
	return err
}

// buildHavingCondition binds values to a validated aggregate expression or alias.
func (c *MariaDBConnector) buildHavingCondition(having *interfaces.HavingClause, expression string, field *interfaces.Property) (sq.Sqlizer, error) {
	if field != nil {
		if err := filter_condition.ValidateTemporalPrecision(field.Type, having.Value); err != nil {
			return nil, err
		}
	}

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
		if field != nil && interfaces.DataType_IsDate(field.Type) {
			return dateCompareExpr(expression, field, op, having.Value)
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
			var lower, upper sq.Sqlizer
			if field != nil && interfaces.DataType_IsDate(field.Type) {
				var err error
				lower, err = dateCompareExpr(expression, field, lowerOp, values[0])
				if err != nil {
					return nil, fmt.Errorf("failed to convert HAVING lower bound: %w", err)
				}
				upper, err = dateCompareExpr(expression, field, upperOp, values[1])
				if err != nil {
					return nil, fmt.Errorf("failed to convert HAVING upper bound: %w", err)
				}
			} else {
				lower = sq.Expr(expression+" "+lowerOp+" ?", values[0])
				upper = sq.Expr(expression+" "+upperOp+" ?", values[1])
			}
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
		if field != nil && interfaces.DataType_IsDate(field.Type) {
			return dateSetExpr(expression, field, op, values)
		}
		return sq.Expr(expression+" "+op+" ("+sq.Placeholders(len(values))+")", values...), nil
	default:
		return nil, fmt.Errorf("unsupported HAVING operation: %s", having.Operation)
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
