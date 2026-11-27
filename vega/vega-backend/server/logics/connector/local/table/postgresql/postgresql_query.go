// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
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
	if t, ok := v.(time.Time); ok && isZonedTimeType(origTypeMap[colName]) {
		return t.Local()
	}
	return convertRawValue(v, preserveBinary)
}

func isZonedTimeType(nativeType string) bool {
	switch nativeType {
	case "timestamptz", "timetz", "timestamp with time zone", "time with time zone":
		return true
	default:
		return false
	}
}

// ExecuteQuery 为 PostgreSQL 使用公共 Builder，复用既有结果扫描和归一化。
func (c *PostgresqlConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	query, err := c.buildSelectBuilder(resource, params)
	if err != nil {
		return nil, err
	}
	rows, _, err := c.OpenSQLBuilder(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result, err := c.scanQueryRows(rows, resource, params)
	if err != nil {
		return nil, fmt.Errorf("failed to scan query rows: %w", err)
	}
	// COUNT 独立执行，先释放数据 Rows，兼容单连接池。
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close query rows: %w", c.ClassifyQueryError(err))
	}
	if params.NeedTotal {
		text, args, err := ToSql(table.StructuredCountQuery(query))
		if err != nil {
			return nil, err
		}
		var total *int64
		if err := c.db.QueryRowContext(ctx, text, args...).Scan(&total); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			otellog.LogWarn(ctx, "Resource row count is unavailable")
		} else {
			result.TotalCount = total
		}
	}
	table.NormalizeQueryResult(result, resource, params)
	return result, nil
}

// buildSelectBuilder 将 DTO 的查询子句组装为公共 Builder，不创建连接或拼接 SQL 片段。
func (c *PostgresqlConnector) buildSelectBuilder(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*sb.SelectBuilder, error) {
	if params.Paging.Offset < 0 || params.Paging.Limit < 0 {
		return nil, interfaces.NewConditionBuildError("paging offset and limit must not be negative")
	}
	fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, field := range resource.SchemaDefinition {
		fields[field.Name] = field
	}
	sourceColumn := func(name string) sb.SqlNode {
		if field := fields[name]; field != nil {
			return sqlResourceColumn(field)
		}
		return sqlColumnName(name)
	}
	query := sb.Select().From(&sb.Table{ResourceID: resource.ID, SourceIdentifier: resource.SourceIdentifier})
	groupExpressions := make(map[string]sb.SqlNode, len(params.GroupBy))
	for _, group := range params.GroupBy {
		expr := sourceColumn(group.Property)
		if group.CalendarInterval != "" {
			var err error
			expr, err = sqlCalendarGroup(expr, group.CalendarInterval)
			if err != nil {
				return nil, err
			}
		}
		query.Columns(&sb.Alias{Expr: expr, Name: group.Property}).GroupBy(expr)
		groupExpressions[group.Property] = expr
	}
	var metric sb.SqlNode
	metricAlias := ""
	metricField := &interfaces.Property{Type: interfaces.DataType_Integer}
	if aggregation := params.Aggregation; aggregation != nil {
		name := strings.ToUpper(aggregation.Aggr)
		distinct := name == "COUNT_DISTINCT"
		if distinct {
			name = "COUNT"
		}
		switch name {
		case "COUNT", "SUM", "AVG", "MIN", "MAX":
		default:
			return nil, interfaces.NewConditionBuildError("unsupported aggregate function")
		}
		var arg sb.SqlNode = &sb.Star{}
		if aggregation.Property != "*" {
			arg = sourceColumn(aggregation.Property)
		} else if name != "COUNT" || distinct {
			return nil, interfaces.NewConditionBuildError("aggregate wildcard requires count")
		}
		metric = &sb.Function{Name: name, Args: []sb.SqlNode{arg}, Distinct: distinct}
		metricAlias = aggregation.Alias
		if metricAlias == "" {
			metricAlias = "__value"
		}
		if name == "MIN" || name == "MAX" {
			metricField = fields[aggregation.Property]
		}
	} else if params.Having != nil && params.Having.Field == "count(*)" {
		metric = &sb.Function{Name: "COUNT", Args: []sb.SqlNode{&sb.Star{}}}
		metricAlias = "__value"
	}
	if err := table.ValidateAggregateAlias(metricAlias, params.GroupBy); err != nil {
		return nil, err
	}
	if metric != nil {
		query.Columns(&sb.Alias{Expr: metric, Name: metricAlias})
	}
	if len(params.GroupBy) == 0 && params.Aggregation == nil && params.Having == nil {
		names := params.OutputFields
		if len(names) == 0 {
			for _, field := range resource.SchemaDefinition {
				names = append(names, field.Name)
			}
		}
		for _, name := range names {
			field := fields[name]
			if field == nil {
				return nil, interfaces.NewOutputFieldError(interfaces.OutputFieldUnknown, name)
			}
			expr := sourceColumn(name)
			if field.Type == interfaces.DataType_Binary {
				switch {
				case params.BinaryMode != nil && *params.BinaryMode == interfaces.BinaryModeContent:
				case params.BinaryMode != nil && *params.BinaryMode == interfaces.BinaryModeMetadata:
					expr = &sb.Function{Name: "OCTET_LENGTH", Args: []sb.SqlNode{expr}}
				default:
					expr = &sb.Literal{Value: nil}
				}
			}
			query.Columns(&sb.Alias{Expr: expr, Name: name})
		}
	}
	if len(query.ColumnsPart) == 0 {
		return nil, interfaces.NewOutputFieldError(interfaces.OutputFieldNoneQueryable, "")
	}
	if params.ActualFilterCond != nil {
		columns, err := table.ColumnMetadata(resource)
		if err != nil {
			return nil, err
		}
		predicate, err := c.ConvertFilterCondition(params.ActualFilterCond, resource.SchemaDefinition, columns)
		if err != nil {
			return nil, err
		}
		query.Where(predicate)
	}
	if having := params.Having; having != nil && metric != nil {
		expr := metric
		field := metricField
		if having.Field == "count(*)" {
			expr = &sb.Function{Name: "COUNT", Args: []sb.SqlNode{&sb.Star{}}}
			field = &interfaces.Property{Type: interfaces.DataType_Integer}
		}
		predicate, err := sqlHavingCondition(having, expr, field)
		if err != nil {
			return nil, err
		}
		query.Having(predicate)
	}
	for _, sort := range table.QuerySortFields(params, metricAlias) {
		expr := sourceColumn(sort.Field)
		if sort.Field == metricAlias && metricAlias != "" {
			expr = sqlColumnName(metricAlias)
		}
		if grouped := groupExpressions[sort.Field]; grouped != nil && sort.Field != metricAlias {
			expr = sqlColumnName(sort.Field)
		}
		nulls := sb.NullsLast
		if len(params.GroupBy) > 0 {
			// 分组查询与索引及 MySQL 保持一致；明细查询保留既有 NULL 排序。
			if sort.Direction != interfaces.DESC_DIRECTION {
				nulls = sb.NullsFirst
			}
		} else if sort.Direction == interfaces.DESC_DIRECTION {
			nulls = sb.NullsFirst
		}
		query.OrderBy(sb.Order{Expr: expr, Descending: sort.Direction == interfaces.DESC_DIRECTION, Nulls: nulls})
	}
	if params.CursorEncoded == "" {
		query.Offset(uint64(params.Paging.Offset))
	}
	query.Limit(uint64(params.Paging.Limit))
	return query, nil
}

func sqlHavingCondition(having *interfaces.HavingClause, expr sb.SqlNode, field *interfaces.Property) (sb.SqlNode, error) {
	if having.Field != "__value" && having.Field != "count(*)" {
		return nil, interfaces.NewConditionBuildError("HAVING field must be '__value' or 'count(*)'")
	}
	if field != nil {
		if err := filter_condition.ValidateTemporalPrecision(field.Type, having.Value); err != nil {
			return nil, err
		}
	}
	switch having.Operation {
	case "=", "==", "!=", "<>", ">", ">=", "<", "<=":
		if having.Value == nil {
			return nil, interfaces.NewConditionBuildError("HAVING comparison requires a value")
		}
		value, err := sqlConditionValue(field, having.Value)
		if err != nil {
			return nil, err
		}
		return sqlComparisonNode(expr, value, having.Operation)
	case "in", "not_in", "range", "out_range":
		var values []any
		switch value := having.Value.(type) {
		case []any:
			values = value
		case []string:
			for _, v := range value {
				values = append(values, v)
			}
		default:
			return nil, interfaces.NewConditionBuildError("HAVING operation requires an array")
		}
		if having.Operation == "in" || having.Operation == "not_in" {
			return sqlSetCondition(expr, field, values, having.Operation == "not_in")
		}
		return sqlRangeCondition(expr, field, values, having.Operation == "out_range")
	default:
		return nil, interfaces.NewConditionBuildError("unsupported HAVING operation")
	}
}

// sqlCalendarGroup 构造 PG 日历分桶，按源时间类型保留会话时区语义。
func sqlCalendarGroup(expr sb.SqlNode, unit string) (sb.SqlNode, error) {
	format := ""
	switch unit {
	case interfaces.CALENDAR_UNIT_MINUTE:
		format = "YYYY-MM-DD HH24:MI"
	case interfaces.CALENDAR_UNIT_HOUR:
		format = "YYYY-MM-DD HH24"
	case interfaces.CALENDAR_UNIT_DAY:
		format = "YYYY-MM-DD"
	case interfaces.CALENDAR_UNIT_WEEK:
		format = "IYYY-IW"
	case interfaces.CALENDAR_UNIT_MONTH:
		format = "YYYY-MM"
	case interfaces.CALENDAR_UNIT_QUARTER:
		format = `YYYY-"Q"Q`
	case interfaces.CALENDAR_UNIT_YEAR:
		format = "YYYY"
	default:
		return nil, interfaces.NewConditionBuildError("unsupported calendar interval")
	}
	return &sb.Function{Name: "TO_CHAR", Args: []sb.SqlNode{&sb.Function{Name: "DATE_TRUNC", Args: []sb.SqlNode{&sb.Literal{Value: unit}, expr}}, &sb.Literal{Value: format}}}, nil
}

func queryColumnTypes(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) map[string]string {
	fieldMap := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, field := range resource.SchemaDefinition {
		fieldMap[field.Name] = field
	}
	// Results are always aliased to property names, so retain original types by
	// the same logical names instead of re-reading source metadata.
	origTypeMap := make(map[string]string, len(resource.SchemaDefinition))
	for _, prop := range resource.SchemaDefinition {
		if prop.OriginalType != "" {
			origTypeMap[prop.Name] = prop.OriginalType
		}
	}

	metricAlias := ""
	if params.Aggregation != nil {
		metricAlias = params.Aggregation.Alias
		if metricAlias == "" {
			metricAlias = "__value"
		}
	} else if params.Having != nil && params.Having.Field == "count(*)" {
		metricAlias = "__value"
	}
	// 聚合列仅保留 MIN/MAX 实际源字段的时间类型，不继承同名字段的类型。
	delete(origTypeMap, metricAlias)

	if agg := params.Aggregation; agg != nil && (agg.Aggr == "min" || agg.Aggr == "max") {
		if field := fieldMap[agg.Property]; field != nil {
			origTypeMap[metricAlias] = field.OriginalType
		}
	}
	return origTypeMap
}

// scanQueryRows 复用原始字段类型和聚合别名信息，保留带时区值与二进制内容。
func (c *PostgresqlConnector) scanQueryRows(rows *sql.Rows, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	origTypeMap := queryColumnTypes(resource, params)
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to read result columns: %w", err)
	}
	result := &interfaces.ResourceDataQueryResult{
		Columns: columns,
		Entries: make([]map[string]any, 0),
	}
	binaryColumns := table.QueryBinaryContentColumns(resource, params)
	for rows.Next() {
		values, destinations := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("failed to scan result row: %w", err)
		}
		entry := make(map[string]any, len(columns))
		for i, column := range columns {
			entry[column] = convertValue(values[i], column, origTypeMap, binaryColumns[column])
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate result rows: %w", c.ClassifyQueryError(err))
	}
	return result, nil
}

// ClassifyQueryError 按 SQLSTATE 归类查询错误，并保留原始驱动错误供诊断。
func (*PostgresqlConnector) ClassifyQueryError(err error) error {
	var driverErr *pq.Error
	if errors.As(err, &driverErr) {
		switch string(driverErr.Code) {
		case "42703":
			return interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, err)
		case "42601", "42701", "42702", "42803", "42804", "42846", "42883", "42P10", "42P18",
			"22023", "2201B", "22P02", "22003", "22007", "22008", "22012", "22018", "22021":
			return interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterInvalidQuery, err)
		}
	}
	return err
}

// OpenSQLBuilder 在执行时生成 PG SQL 和参数；Rows 成功后交由查询会话或调用方关闭。
func (c *PostgresqlConnector) OpenSQLBuilder(ctx context.Context, query *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	statement, err := readStatement(query)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Connect(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to connect for SQL builder query: %w", c.ClassifyQueryError(err))
	}
	rows, err := c.db.QueryContext(ctx, statement.Text, statement.Args...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute SQL builder query: %w", c.ClassifyQueryError(err))
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = rows.Close()
		}
	}()
	columns, err := table.DescribeSQLRows(rows, c)
	if err != nil {
		return nil, nil, err
	}
	transferred = true
	return rows, columns, nil
}

// OpenTableRead 以结构化查询的字段转换规则打开流式读取会话。
func (c *PostgresqlConnector) OpenTableRead(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (interfaces.QueryPageSession, error) {

	query, err := c.buildSelectBuilder(resource, params)
	if err != nil {
		return nil, err
	}
	query.LimitPart = nil
	var count *sb.SelectBuilder
	if params.NeedTotal {
		count = table.StructuredCountQuery(query)
	}
	return c.openReadSession(ctx, query, count, resource, params, 0)
}

// OpenSQLRead 为已通过只读策略校验的 Builder 查询打开流式读取会话。
func (c *PostgresqlConnector) OpenSQLRead(ctx context.Context, query, count *sb.SelectBuilder, timeout time.Duration) (interfaces.QueryPageSession, error) {
	return c.openReadSession(ctx, query, count, nil, nil, timeout)
}

func (c *PostgresqlConnector) openReadSession(ctx context.Context, query, count *sb.SelectBuilder,
	resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams, timeout time.Duration) (interfaces.QueryPageSession, error) {
	statement, err := readStatement(query)
	if err != nil {
		return nil, err
	}
	var countStatement *table.SQLStatement
	if count != nil {
		rendered, err := readStatement(count)
		if err != nil {
			return nil, err
		}
		countStatement = &rendered
	}
	var localTimeColumns map[string]bool
	if params != nil {
		localTimeColumns = make(map[string]bool)
		for name, nativeType := range queryColumnTypes(resource, params) {
			if isZonedTimeType(nativeType) {
				localTimeColumns[name] = true
			}
		}
	}

	session, err := table.OpenReadSession(ctx, c, statement, countStatement, resource, params, nil, localTimeColumns, timeout)
	return session, c.ClassifyQueryError(err)
}

func readStatement(query *sb.SelectBuilder) (table.SQLStatement, error) {
	text, args, err := ToSql(query)
	if err != nil {
		failure := interfaces.NewConditionBuildError("SQL expression is not supported by PostgreSQL")
		failure.Cause = err
		return table.SQLStatement{}, failure
	}
	return table.SQLStatement{Text: text, Args: args}, nil
}

// ExecuteSQLBuilder 消费一次 Builder 结果，保留 bytea 原始字节和 NUMERIC 文本精度。
// TotalCount 为本次返回行数；独立 COUNT 和 API 分页由查询服务负责。
func (c *PostgresqlConnector) ExecuteSQLBuilder(ctx context.Context, query *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
	rows, columns, err := c.OpenSQLBuilder(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := &interfaces.RawQueryResponse{Columns: columns, Entries: make([]map[string]any, 0)}
	for rows.Next() {
		values, destinations := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("failed to scan SQL builder row: %w", err)
		}
		entry := make(map[string]any, len(columns))
		for i, column := range columns {
			entry[column.Name] = convertRawValue(values[i], column.Type == interfaces.DataType_Binary)
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate SQL builder rows: %w", c.ClassifyQueryError(err))
	}
	total := int64(len(result.Entries))
	result.TotalCount = &total
	return result, nil
}

// ScanTableReadRow 读取结构化查询的一行，并保留投影指定的二进制列。
func (*PostgresqlConnector) ScanTableReadRow(rows *sql.Rows, columns []interfaces.ColumnInfo,
	_ map[string]*interfaces.Property, _ *string, binaryContentColumns map[string]bool) (map[string]any, error) {

	return table.ScanTableReadRow(rows, columns, binaryContentColumns)
}

// ScanSQLBuilderRow reads one retained SQL row and classifies source errors.
func (c *PostgresqlConnector) ScanSQLBuilderRow(rows *sql.Rows, columns []interfaces.ColumnInfo) (map[string]any, error) {
	row, err := table.ScanSQLRow(rows, columns)
	return row, c.ClassifyQueryError(err)
}
