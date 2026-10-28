// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
	fc "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// sqlBuffer 只按写入顺序拼接节点及参数，并保留第一个错误。
type sqlBuffer struct {
	text strings.Builder
	args []any
	err  error
}

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

// ExecuteRawSQL 不再执行原始 SQL；SQL 查询必须通过 Parser 和 Builder。
// 保留方法以满足共享 TableConnector 端口，误调用时在建立连接前明确拒绝。
func (c *MariaDBConnector) ExecuteRawSQL(_ context.Context, _ string) (*interfaces.RawQueryResponse, error) {
	return nil, interfaces.NewUnsupportedOperationError("ExecuteRawSQL", errors.New("use Parser and ExecuteSQLBuilder for SQL queries"))
}

// scanQueryRows 保留所选 Binary 内容，其他字节值转为字符串；总数由调用方按接口契约设置。
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

// ExecuteQuery 为 MariaDB/MySQL 使用公共 Builder，复用既有结果扫描和归一化。
func (c *MariaDBConnector) ExecuteQuery(ctx context.Context, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
	query, err := c.buildSelectBuilder(resource, params)
	if err != nil {
		return nil, err
	}
	rows, _, err := c.OpenSQLBuilder(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result, err := c.scanQueryRows(rows, table.QueryBinaryContentColumns(resource, params))
	if err != nil {
		return nil, fmt.Errorf("failed to scan query rows: %w", err)
	}
	// COUNT 独立执行，先释放数据 Rows，兼容单连接池。
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close query rows: %w", mariadbQueryError(err))
	}
	if params.NeedTotal {
		unpaged := *query
		unpaged.LimitPart = nil
		unpaged.OffsetPart = 0
		unpaged.OrderByPart = nil
		count := sb.Select(&sb.Function{Name: "COUNT", Args: []sb.SqlNode{&sb.Star{}}}).FromSelect(&unpaged, "_resource_total")
		text, args, err := ToSql(count)
		if err != nil {
			return nil, err
		}
		args, err = sqlBuilderArguments(args)
		if err != nil {
			return nil, err
		}
		if err := c.db.QueryRowContext(ctx, text, args...).Scan(&result.Total); err != nil {
			return nil, fmt.Errorf("failed to scan total: %w", mariadbQueryError(err))
		}
	}
	table.NormalizeQueryResult(result, resource, params)
	return result, nil
}

// buildSelectBuilder 将 DTO 的查询子句组装为公共 Builder，不创建连接或拼接 SQL 片段。
func (c *MariaDBConnector) buildSelectBuilder(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*sb.SelectBuilder, error) {
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
					expr = &sb.Param{Value: nil}
				}
			}
			query.Columns(&sb.Alias{Expr: expr, Name: name})
		}
	}
	if len(query.ColumnsPart) == 0 {
		return nil, interfaces.NewOutputFieldError(interfaces.OutputFieldNoneQueryable, "")
	}
	if params.ActualFilterCond != nil {
		predicate, err := c.ConvertFilterCondition(params.ActualFilterCond)
		if err != nil {
			return nil, err
		}
		query.Where(predicate)
	}
	if having := params.Having; having != nil && metric != nil {
		expr := sqlColumnName(metricAlias)
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
	for _, sort := range params.Sort {
		expr := sourceColumn(sort.Field)
		if sort.Field == metricAlias && metricAlias != "" {
			expr = sqlColumnName(metricAlias)
		}
		if grouped := groupExpressions[sort.Field]; grouped != nil && sort.Field != metricAlias {
			expr = grouped
		}
		query.OrderBy(sb.Order{Expr: expr, Descending: sort.Direction == interfaces.DESC_DIRECTION})
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
		if err := fc.ValidateTemporalPrecision(field.Type, having.Value); err != nil {
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

func sqlCalendarGroup(expr sb.SqlNode, unit string) (sb.SqlNode, error) {
	format := ""
	switch unit {
	case interfaces.CALENDAR_UNIT_MINUTE:
		format = "%Y-%m-%d %H:%i"
	case interfaces.CALENDAR_UNIT_HOUR:
		format = "%Y-%m-%d %H"
	case interfaces.CALENDAR_UNIT_DAY:
		format = "%Y-%m-%d"
	case interfaces.CALENDAR_UNIT_WEEK:
		format = "%x-%v"
	case interfaces.CALENDAR_UNIT_MONTH:
		format = "%Y-%m"
	case interfaces.CALENDAR_UNIT_YEAR:
		format = "%Y"
	case interfaces.CALENDAR_UNIT_QUARTER:
		return &sb.Function{Name: "CONCAT", Args: []sb.SqlNode{&sb.Function{Name: "YEAR", Args: []sb.SqlNode{expr}}, &sb.Param{Value: "-Q"}, &sb.Function{Name: "QUARTER", Args: []sb.SqlNode{expr}}}}, nil
	default:
		return nil, interfaces.NewConditionBuildError("unsupported calendar interval")
	}
	return sqlDateFormat(expr, format), nil
}

// ExecuteSQLBuilder 执行已完成资源授权和来源解析的查询，在执行时生成 SQL 和参数。
// 结果沿用 RawQueryResponse；TotalCount 是本次返回行数，API 分页和会话由调用层负责。
// MariaDB/MySQL 与 PG SQL 服务消费 Builder 入口，其他连接器继续沿用 ExecuteRawSQL。
func (c *MariaDBConnector) ExecuteSQLBuilder(ctx context.Context, query *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
	rows, columns, err := c.OpenSQLBuilder(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	binaryColumns := make(map[string]bool)
	for _, column := range columns {
		if column.Type == interfaces.DataType_Binary {
			binaryColumns[column.Name] = true
		}
	}
	result, err := c.scanQueryRows(rows, binaryColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to scan SQL builder query rows: %w", mariadbQueryError(err))
	}
	total := int64(len(result.Entries))
	return &interfaces.RawQueryResponse{Columns: columns, Entries: result.Entries, TotalCount: &total}, nil
}

// OpenSQLBuilder 只执行一次 SQL，不提前扫描结果；成功后 Rows 的所有权交给调用方。
func (c *MariaDBConnector) OpenSQLBuilder(ctx context.Context, query *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	text, args, err := ToSql(query)
	if err != nil {
		failure := interfaces.NewConditionBuildError("SQL expression is not supported by MySQL")
		failure.Cause = err
		return nil, nil, failure
	}
	args, err = sqlBuilderArguments(args)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Connect(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to connect for SQL builder query: %w", mariadbQueryError(err))
	}
	rows, err := c.db.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute SQL builder query: %w", mariadbQueryError(err))
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = rows.Close()
		}
	}()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read result column types: %w", err)
	}
	columns := make([]interfaces.ColumnInfo, len(types))
	names := make(map[string]bool, len(types))
	for i, column := range types {
		if names[column.Name()] {
			return nil, nil, interfaces.NewConditionBuildError("query result contains duplicate column names; use explicit aliases")
		}
		names[column.Name()] = true
		columns[i] = interfaces.ColumnInfo{Name: column.Name(), Type: c.MapType(column.DatabaseTypeName())}
	}
	transferred = true
	return rows, columns, nil
}

// render 直接分派具体节点；空指针与未知节点均返回错误。
func render(node sb.SqlNode) (string, []any, error) {
	switch n := node.(type) {
	case *sb.SelectBuilder:
		if n != nil {
			return ToSql(n)
		}
	case *sb.Table:
		if n != nil {
			return tableSql(*n)
		}
	case *sb.Subquery:
		if n != nil {
			return subquerySql(*n)
		}
	case *sb.Join:
		if n != nil {
			return joinSql(*n)
		}
	case *sb.JoinedTable:
		if n != nil {
			return joinedTableSql(*n)
		}
	case *sb.Order:
		if n != nil {
			return orderSql(*n)
		}
	case *sb.Column:
		if n != nil {
			return columnSql(*n)
		}
	case *sb.Param:
		if n != nil {
			if number, ok := n.Value.(json.Number); ok {
				return numericLiteralSQL(number)
			}
			return "?", []any{n.Value}, nil
		}
	case *sb.Star:
		if n != nil {
			return starSql(*n)
		}
	case *sb.Alias:
		if n != nil {
			return aliasSql(*n)
		}
	case *sb.Eq:
		if n != nil {
			return binarySql(n.Left, "=", n.Right)
		}
	case *sb.NotEq:
		if n != nil {
			return binarySql(n.Left, "<>", n.Right)
		}
	case *sb.Gt:
		if n != nil {
			return binarySql(n.Left, ">", n.Right)
		}
	case *sb.GtOrEq:
		if n != nil {
			return binarySql(n.Left, ">=", n.Right)
		}
	case *sb.Lt:
		if n != nil {
			return binarySql(n.Left, "<", n.Right)
		}
	case *sb.LtOrEq:
		if n != nil {
			return binarySql(n.Left, "<=", n.Right)
		}
	case *sb.And:
		if n != nil {
			return conditionsSql(*n, " AND ", "(1=1)")
		}
	case *sb.Or:
		if n != nil {
			return conditionsSql(*n, " OR ", "(1=0)")
		}
	case *sb.Not:
		if n != nil {
			return notSql(*n)
		}
	case *sb.IsNull:
		if n != nil {
			return isNullSql(*n)
		}
	case *sb.In:
		if n != nil {
			return inSql(*n)
		}
	case *sb.Between:
		if n != nil {
			return betweenSql(*n)
		}
	case *sb.Like:
		if n != nil {
			return likeSql(*n)
		}
	case *sb.Regex:
		if n != nil {
			return binarySql(n.Expr, "REGEXP", n.Pattern)
		}
	case *sb.Interval:
		if n != nil {
			if !calendarUnit(string(n.Unit)) {
				return "", nil, fmt.Errorf("invalid calendar interval unit")
			}
			b := sqlBuffer{}
			b.writeString("INTERVAL ")
			b.writePart(n.Value)
			b.writeString(" " + strings.ToUpper(string(n.Unit)))
			return b.result()
		}
	case *sb.Contains:
		if n != nil {
			return containsSql(*n)
		}
	case *sb.Arithmetic:
		if n != nil {
			return arithmeticSql(*n)
		}
	case *sb.Unary:
		if n != nil {
			return unarySql(*n)
		}
	case *sb.Function:
		if n != nil {
			return functionSql(*n)
		}
	case *sb.Case:
		if n != nil {
			return caseSql(*n)
		}
	case *sb.Cast:
		if n != nil {
			return castSql(*n)
		}
	}
	return "", nil, fmt.Errorf("query node is nil or unsupported")
}

// ToSql 根据结构化查询生成 MySQL SQL 和按出现顺序排列的参数，不执行请求。
func ToSql(q *sb.SelectBuilder) (string, []any, error) {
	if q == nil || len(q.ColumnsPart) == 0 {
		return "", nil, fmt.Errorf("SELECT requires a projection")
	}
	if q.FromPart == nil && len(q.JoinsPart) != 0 {
		return "", nil, fmt.Errorf("JOIN requires a FROM source")
	}
	b := sqlBuffer{}
	b.writeString("SELECT ")
	if q.DistinctPart {
		b.writeString("DISTINCT ")
	}
	b.writeParts(q.ColumnsPart, ", ")
	if q.FromPart != nil {
		b.writeString(" FROM ")
		b.writePart(q.FromPart)
	}
	for _, join := range q.JoinsPart {
		b.writeByte(' ')
		b.writePart(&join)
	}
	if len(q.WherePart) != 0 {
		b.writeString(" WHERE ")
		b.writeParts(q.WherePart, " AND ")
	}
	if len(q.GroupByPart) != 0 {
		b.writeString(" GROUP BY ")
		for i, group := range q.GroupByPart {
			if i > 0 {
				b.writeString(", ")
			}
			// 整数字面量保留位置语义，范围及聚合合法性由源端判断。
			if param, ok := group.(*sb.Param); ok && param != nil {
				if number, ok := param.Value.(json.Number); ok && len(number) > 0 && strings.IndexFunc(string(number), func(r rune) bool { return r < '0' || r > '9' }) == -1 {
					b.writeString(string(number))
					continue
				}
			}
			b.writePart(group)
		}
	}
	if len(q.HavingPart) != 0 {
		b.writeString(" HAVING ")
		b.writeParts(q.HavingPart, " AND ")
	}
	if len(q.OrderByPart) != 0 {
		b.writeString(" ORDER BY ")
		for i, order := range q.OrderByPart {
			if i != 0 {
				b.writeString(", ")
			}
			b.writePart(&order)
		}
	}
	if q.LimitPart != nil {
		b.writeString(" LIMIT " + strconv.FormatUint(*q.LimitPart, 10))
	} else if q.OffsetPart != 0 {
		// MySQL 的 OFFSET 必须与 LIMIT 同时出现。
		b.writeString(" LIMIT 18446744073709551615")
	}
	if q.OffsetPart != 0 {
		b.writeString(" OFFSET " + strconv.FormatUint(q.OffsetPart, 10))
	}
	return b.result()
}

func tableSql(t sb.Table) (string, []any, error) {
	text := t.SourceIdentifier
	if t.Alias != "" {
		alias, err := quoteIdentifier(t.Alias)
		if err != nil {
			return "", nil, err
		}
		text += " AS " + alias
	}
	return text, nil, nil
}

func subquerySql(s sb.Subquery) (string, []any, error) {
	if s.Query == nil {
		return "", nil, fmt.Errorf("subquery is nil")
	}
	alias, err := quoteIdentifier(s.Alias)
	if err != nil {
		return "", nil, err
	}
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(s.Query)
	b.writeString(") AS " + alias)
	return b.result()
}

func joinSql(j sb.Join) (string, []any, error) {
	var keyword string
	switch j.Kind {
	case sb.InnerJoin:
		keyword = "INNER JOIN"
	case sb.LeftJoin:
		keyword = "LEFT JOIN"
	case sb.RightJoin:
		keyword = "RIGHT JOIN"
	case sb.CrossJoin:
		keyword = "CROSS JOIN"
	default:
		return "", nil, fmt.Errorf("invalid JOIN kind")
	}
	if j.Kind == sb.CrossJoin && j.On != nil {
		return "", nil, fmt.Errorf("CROSS JOIN does not accept ON")
	}
	b := sqlBuffer{}
	b.writeString(keyword + " ")
	b.writePart(j.Source)
	if j.Kind != sb.CrossJoin {
		b.writeString(" ON ")
		b.writePart(j.On)
	}
	return b.result()
}

func joinedTableSql(j sb.JoinedTable) (string, []any, error) {
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(j.From)
	for _, join := range j.Joins {
		b.writeByte(' ')
		b.writePart(&join)
	}
	b.writeByte(')')
	return b.result()
}

func orderSql(o sb.Order) (string, []any, error) {
	if o.Position != 0 {
		if o.Expr != nil || o.Nulls != sb.NullsDefault {
			return "", nil, fmt.Errorf("ORDER BY position conflicts with an expression or NULL ordering")
		}
		direction := " ASC"
		if o.Descending {
			direction = " DESC"
		}
		return strconv.FormatUint(o.Position, 10) + direction, nil, nil
	}
	if o.Nulls != sb.NullsDefault && o.Nulls != sb.NullsFirst && o.Nulls != sb.NullsLast {
		return "", nil, fmt.Errorf("invalid NULL ordering")
	}
	direction := " ASC"
	if o.Descending {
		direction = " DESC"
	}
	b := sqlBuffer{}
	// MySQL 自带 ASC NULLS FIRST、DESC NULLS LAST；仅相反规则需要额外排序键。
	if (o.Descending && o.Nulls == sb.NullsFirst) || (!o.Descending && o.Nulls == sb.NullsLast) {
		b.writeByte('(')
		b.writePart(o.Expr)
		b.writeString(" IS NULL)")
		if o.Nulls == sb.NullsFirst {
			b.writeString(" DESC, ")
		} else {
			b.writeString(" ASC, ")
		}
	}
	b.writePart(o.Expr)
	b.writeString(direction)
	return b.result()
}

func quoteIdentifier(name string) (string, error) {
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("identifier is empty or contains invalid characters")
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

func (b *sqlBuffer) writePart(part sb.SqlNode) {
	if b.err != nil {
		return
	}
	text, args, err := render(part)
	if err != nil {
		b.err = err
		return
	}
	if text == "" {
		b.err = fmt.Errorf("query node generated empty SQL")
		return
	}
	b.writeString(text)
	b.args = append(b.args, args...)
}

func (b *sqlBuffer) writeParts(parts []sb.SqlNode, separator string) {
	for i, part := range parts {
		if i != 0 {
			b.writeString(separator)
		}
		b.writePart(part)
		if b.err != nil {
			return
		}
	}
}

func (b *sqlBuffer) result() (string, []any, error) {
	if b.err != nil {
		return "", nil, b.err
	}
	return b.text.String(), b.args, nil
}

// strings.Builder 的写入始终返回 nil 错误。
func (b *sqlBuffer) writeString(text string) {
	_, _ = b.text.WriteString(text)
}

func (b *sqlBuffer) writeByte(value byte) {
	_ = b.text.WriteByte(value)
}

// sqlBuilderArguments 保持 SQL 数字字面量的整数/浮点参数类型，小数由生成的 DECIMAL CAST 定型。
func sqlBuilderArguments(args []any) ([]any, error) {
	values := append([]any(nil), args...)
	for i, value := range values {
		number, ok := value.(json.Number)
		if !ok {
			continue
		}
		text := string(number)
		switch {
		case strings.ContainsAny(text, "eE"):
			n, err := strconv.ParseFloat(text, 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return nil, interfaces.NewConditionBuildError("numeric literal is outside the supported source range")
			}
			values[i] = n
		case strings.Contains(text, "."):
			values[i] = text
		default:
			if n, err := strconv.ParseInt(text, 10, 64); err == nil {
				values[i] = n
			} else if n, err := strconv.ParseUint(text, 10, 64); err == nil {
				values[i] = n
			} else {
				values[i] = text
			}
		}
	}
	return values, nil
}
