// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"vega-backend/interfaces"
)

// BuildPagedSQL applies SQL Server paging syntax to a validated query.
func (c *SQLServerConnector) BuildPagedSQL(sql string, offset, limit int) string {
	sql, queryOption := splitTopLevelQueryOption(sql)
	orderStart, offsetStart := topLevelOrderBy(sql)
	if orderStart < 0 {
		if topLevelTopClause(sql) < 0 {
			return appendQueryOption(fmt.Sprintf(
				"%s\nORDER BY (SELECT 1) OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", sql, offset, limit,
			), queryOption)
		}
		return appendQueryOption(fmt.Sprintf(
			"SELECT * FROM (%s\n) AS _raw_query_page ORDER BY (SELECT 1) OFFSET %d ROWS FETCH NEXT %d ROWS ONLY",
			sql, offset, limit,
		), queryOption)
	}
	topStart := topLevelTopClause(sql[:orderStart])
	if topStart < 0 && offsetStart < 0 {
		return appendQueryOption(fmt.Sprintf(
			"%s\nOFFSET %d ROWS FETCH NEXT %d ROWS ONLY", sql, offset, limit,
		), queryOption)
	}
	if offsetStart < 0 {
		if pagedSQL, ok := rewriteLiteralTopPaging(sql, topStart, offset, limit); ok {
			return appendQueryOption(pagedSQL, queryOption)
		}
	}

	outerOrderEnd := len(sql)
	if offsetStart >= 0 {
		outerOrderEnd = offsetStart
	}
	outerOrder := strings.TrimSpace(sql[orderStart:outerOrderEnd])
	return appendQueryOption(fmt.Sprintf(
		"SELECT * FROM (%s\n) AS _raw_query_page %s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY",
		sql, outerOrder, offset, limit,
	), queryOption)
}

// rewriteLiteralTopPaging replaces a numeric TOP cap with an equivalent
// OFFSET/FETCH cap. Keeping the ORDER BY in the original query scope allows it
// to reference source aliases and columns that are not part of the projection.
func rewriteLiteralTopPaging(statement string, topStart, offset, limit int) (string, bool) {
	if topStart < 0 {
		return "", false
	}

	topLimit, topEnd, ok := parseLiteralTop(statement, topStart)
	if !ok {
		return "", false
	}
	if offset >= topLimit {
		return statement[:topStart] + "TOP (0) " + statement[topEnd:], true
	}

	pageLimit := min(limit, topLimit-offset)
	withoutTop := statement[:topStart] + statement[topEnd:]
	return fmt.Sprintf("%s\nOFFSET %d ROWS FETCH NEXT %d ROWS ONLY", withoutTop, offset, pageLimit), true
}

func parseLiteralTop(statement string, topStart int) (int, int, bool) {
	index := topStart + len("top")
	index = skipSQLSpaces(statement, index)
	parenthesized := index < len(statement) && statement[index] == '('
	if parenthesized {
		index++
		index = skipSQLSpaces(statement, index)
	}

	digitsStart := index
	for index < len(statement) && statement[index] >= '0' && statement[index] <= '9' {
		index++
	}
	if digitsStart == index {
		return 0, 0, false
	}
	topLimit, err := strconv.Atoi(statement[digitsStart:index])
	if err != nil {
		return 0, 0, false
	}

	index = skipSQLSpaces(statement, index)
	if parenthesized {
		if index >= len(statement) || statement[index] != ')' {
			return 0, 0, false
		}
		index++
		index = skipSQLSpaces(statement, index)
	}
	if hasSQLWordAt(statement, index, "percent") || hasSQLWordAt(statement, index, "with") {
		return 0, 0, false
	}
	return topLimit, index, true
}

func skipSQLSpaces(statement string, index int) int {
	for index < len(statement) {
		switch statement[index] {
		case ' ', '\t', '\r', '\n':
			index++
		default:
			return index
		}
	}
	return index
}

func hasSQLWordAt(statement string, index int, word string) bool {
	end := index + len(word)
	if end > len(statement) || !strings.EqualFold(statement[index:end], word) {
		return false
	}
	return end == len(statement) || !isSQLWordByte(statement[end])
}

// BuildCountSQL removes a top-level ORDER BY when it only affects presentation.
// SQL Server rejects such an ORDER BY inside a derived table. TOP and OFFSET
// queries retain it because ordering changes the selected row set in those cases.
func (c *SQLServerConnector) BuildCountSQL(sql string) string {
	sql, queryOption := splitTopLevelQueryOption(sql)
	orderStart, offsetStart := topLevelOrderBy(sql)
	if orderStart >= 0 && offsetStart < 0 && topLevelTopClause(sql[:orderStart]) < 0 {
		sql = strings.TrimSpace(sql[:orderStart])
	}
	return appendQueryOption(fmt.Sprintf(
		"SELECT COUNT(*) AS _raw_query_total_count FROM (%s\n) AS _raw_query_total", sql,
	), queryOption)
}

type sqlToken struct {
	text  string
	start int
}

func topLevelOrderBy(statement string) (int, int) {
	tokens := topLevelSQLTokens(statement)
	orderStart := -1
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].text == "order" && tokens[i+1].text == "by" {
			orderStart = tokens[i].start
		}
	}
	if orderStart < 0 {
		return -1, -1
	}
	for _, token := range tokens {
		if token.start > orderStart && token.text == "offset" {
			return orderStart, token.start
		}
	}
	return orderStart, -1
}

func topLevelTopClause(statement string) int {
	tokens := topLevelSQLTokens(statement)
	for index, token := range tokens {
		if token.text != "select" {
			continue
		}
		index++
		if index < len(tokens) && (tokens[index].text == "all" || tokens[index].text == "distinct") {
			index++
		}
		if index < len(tokens) && tokens[index].text == "top" {
			return tokens[index].start
		}
		return -1
	}
	return -1
}

func splitTopLevelQueryOption(statement string) (string, string) {
	for _, token := range topLevelSQLTokens(statement) {
		if token.text == "option" {
			return strings.TrimSpace(statement[:token.start]), strings.TrimSpace(statement[token.start:])
		}
	}
	return statement, ""
}

func appendQueryOption(statement, queryOption string) string {
	if queryOption == "" {
		return statement
	}
	return statement + "\n" + queryOption
}

func topLevelSQLTokens(statement string) []sqlToken {
	tokens := make([]sqlToken, 0)
	depth := 0
	for i := 0; i < len(statement); {
		switch {
		case strings.HasPrefix(statement[i:], "--"):
			if end := strings.IndexByte(statement[i+2:], '\n'); end >= 0 {
				i += end + 3
			} else {
				return tokens
			}
		case strings.HasPrefix(statement[i:], "/*"):
			if end := strings.Index(statement[i+2:], "*/"); end >= 0 {
				i += end + 4
			} else {
				return tokens
			}
		case statement[i] == '\'' || statement[i] == '"':
			quote := statement[i]
			i++
			for i < len(statement) {
				if statement[i] != quote {
					i++
					continue
				}
				if i+1 < len(statement) && statement[i+1] == quote {
					i += 2
					continue
				}
				i++
				break
			}
		case statement[i] == '[':
			i++
			for i < len(statement) {
				if statement[i] != ']' {
					i++
					continue
				}
				if i+1 < len(statement) && statement[i+1] == ']' {
					i += 2
					continue
				}
				i++
				break
			}
		case statement[i] == '(':
			depth++
			i++
		case statement[i] == ')':
			if depth > 0 {
				depth--
			}
			i++
		case isSQLWordByte(statement[i]):
			start := i
			for i < len(statement) && isSQLWordByte(statement[i]) {
				i++
			}
			if depth == 0 {
				tokens = append(tokens, sqlToken{text: strings.ToLower(statement[start:i]), start: start})
			}
		default:
			i++
		}
	}
	return tokens
}

func isSQLWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

// ExecuteRawSQL executes a validated read-only SQL statement.
func (c *SQLServerConnector) ExecuteRawSQL(ctx context.Context, statement string) (*interfaces.RawQueryResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}
	rows, err := c.db.QueryContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("execute query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	result := &interfaces.RawQueryResponse{
		Columns: make([]interfaces.ColumnInfo, len(columns)),
		Entries: make([]map[string]any, 0),
	}
	for i, name := range columns {
		result.Columns[i] = interfaces.ColumnInfo{Name: name, Type: c.MapType(types[i].DatabaseTypeName())}
	}
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		entry := make(map[string]any, len(columns))
		for i, name := range columns {
			entry[name] = normalizeQueryValue(values[i], types[i].DatabaseTypeName())
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	total := int64(len(result.Entries))
	result.TotalCount = &total
	return result, nil
}

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
		From(qualifiedResourceTable(resource))
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
			sortExpression := ""
			if isAggregate {
				for _, group := range params.GroupBy {
					if group.Property != sort.Field {
						continue
					}
					property := fields[group.Property]
					sortExpression = quoteIdentifier(property.OriginalName)
					if group.CalendarInterval != "" {
						sortExpression = quoteIdentifier(group.Property)
					}
					break
				}
				if sortExpression == "" && aggregateAlias != "" && sort.Field == aggregateAlias {
					sortExpression = quoteIdentifier(aggregateAlias)
				}
				if sortExpression == "" {
					return nil, fmt.Errorf("aggregate sort field must be a group field or aggregation alias: %s", sort.Field)
				}
			} else {
				property, ok := fields[sort.Field]
				if !ok {
					return nil, fmt.Errorf("sort field is not defined by resource schema: %s", sort.Field)
				}
				sortExpression = quoteIdentifier(property.OriginalName)
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
			From(qualifiedResourceTable(resource))
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
	if len(params.GroupBy) == 0 && params.Aggregation == nil && params.Having == nil {
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
		property, ok := fields[group.Property]
		if !ok {
			return nil, nil, "", "", false, fmt.Errorf("group field is not defined by resource schema: %s", group.Property)
		}
		field := quoteIdentifier(property.OriginalName)
		if group.CalendarInterval == "" {
			projection = append(projection, field)
			groupFields = append(groupFields, field)
			continue
		}
		datePart, err := sqlServerDateTruncPart(group.CalendarInterval)
		if err != nil {
			return nil, nil, "", "", false, err
		}
		expression := "DATETRUNC(" + datePart + ", " + field + ")"
		projection = append(projection, expression+" AS "+quoteIdentifier(group.Property))
		groupFields = append(groupFields, expression)
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
	} else if params.Having != nil && params.Having.Field == "count(*)" {
		alias = "__value"
		expression = "COUNT(*)"
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
	fieldExpression := expression
	if having.Field == "count(*)" {
		fieldExpression = "COUNT(*)"
	} else if having.Field != "__value" || expression == "" {
		return nil, fmt.Errorf("HAVING field must be '__value' or 'count(*)'")
	}
	operators := map[string]string{"==": "=", "!=": "<>", ">": ">", ">=": ">=", "<": "<", "<=": "<="}
	if operator, ok := operators[having.Operation]; ok {
		return sq.Expr(fieldExpression+" "+operator+" ?", having.Value), nil
	}
	values, ok := having.Value.([]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("HAVING operation %s requires an array value", having.Operation)
	}
	if having.Operation == "in" || having.Operation == "not_in" {
		placeholders := make([]string, len(values))
		for index := range placeholders {
			placeholders[index] = "?"
		}
		operator := " IN ("
		if having.Operation == "not_in" {
			operator = " NOT IN ("
		}
		return sq.Expr(fieldExpression+operator+strings.Join(placeholders, ",")+")", values...), nil
	}
	if len(values) != 2 {
		return nil, fmt.Errorf("HAVING operation %s requires two values", having.Operation)
	}
	switch having.Operation {
	case "range":
		return sq.Expr(fieldExpression+" BETWEEN ? AND ?", values[0], values[1]), nil
	case "out_range":
		return sq.Expr(fieldExpression+" NOT BETWEEN ? AND ?", values[0], values[1]), nil
	default:
		return nil, fmt.Errorf("unsupported HAVING operation: %s", having.Operation)
	}
}

func qualifiedResourceTable(resource *interfaces.Resource) string {
	sourceIdentifier := strings.TrimSpace(resource.SourceIdentifier)
	schema := strings.TrimSpace(resource.Schema)
	table := sourceIdentifier
	if schema != "" {
		prefixLength := len(schema) + 1
		if len(sourceIdentifier) >= prefixLength &&
			strings.EqualFold(sourceIdentifier[:prefixLength], schema+".") {
			table = sourceIdentifier[prefixLength:]
		} else if separator := strings.IndexByte(sourceIdentifier, '.'); separator >= 0 &&
			strings.EqualFold(strings.TrimSpace(sourceIdentifier[:separator]), schema) {
			table = sourceIdentifier[separator+1:]
		}
	} else if separator := strings.IndexByte(sourceIdentifier, '.'); separator >= 0 {
		schema, table = sourceIdentifier[:separator], sourceIdentifier[separator+1:]
	}
	if schema == "" {
		return quoteIdentifier(strings.TrimSpace(table))
	}
	return quoteIdentifier(schema) + "." + quoteIdentifier(strings.TrimSpace(table))
}

func quoteIdentifier(identifier string) string {
	return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
}

func scanQueryRows(rows *sql.Rows) (*interfaces.QueryResult, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	result := &interfaces.QueryResult{Columns: columns, Entries: make([]map[string]any, 0)}
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		entry := make(map[string]any, len(columns))
		for i, column := range columns {
			entry[column] = normalizeQueryValue(values[i], types[i].DatabaseTypeName())
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result.Total = int64(len(result.Entries))
	return result, nil
}

func normalizeQueryValue(value any, nativeType string) any {
	switch TypeMapping[strings.ToLower(strings.TrimSpace(nativeType))] {
	case interfaces.DataType_Decimal:
		if raw, ok := value.([]byte); ok {
			return string(raw)
		}
	case interfaces.DataType_Time:
		if timestamp, ok := value.(time.Time); ok {
			return timestamp.Format("15:04:05.9999999")
		}
	}
	return value
}
