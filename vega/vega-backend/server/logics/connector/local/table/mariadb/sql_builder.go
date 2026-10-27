// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

// sqlBuffer 只按写入顺序拼接节点及参数，并保留第一个错误。
type sqlBuffer struct {
	text strings.Builder
	args []any
	err  error
}

// ExecuteSQLBuilder 执行已完成资源授权和来源解析的查询，在执行时生成 SQL 和参数。
// 结果沿用 RawQueryResponse；TotalCount 是本次返回行数，API 分页和会话由调用层负责。
// MySQL SQL 服务消费此入口，其他连接器继续沿用 ExecuteRawSQL。
func (c *MariaDBConnector) ExecuteSQLBuilder(ctx context.Context, query *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
	rows, columns, err := c.OpenSQLBuilder(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result, err := c.scanQueryRows(rows, nil)
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
	return "", nil, fmt.Errorf("查询节点为空或类型不受支持")
}

// ToSql 根据结构化查询生成 MySQL SQL 和按出现顺序排列的参数，不执行请求。
func ToSql(q *sb.SelectBuilder) (string, []any, error) {
	if q == nil || len(q.ColumnsPart) == 0 {
		return "", nil, fmt.Errorf("SELECT 缺少投影")
	}
	if q.FromPart == nil && len(q.JoinsPart) != 0 {
		return "", nil, fmt.Errorf("JOIN 缺少 FROM 来源")
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
		b.writeParts(q.GroupByPart, ", ")
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
		return "", nil, fmt.Errorf("子查询为空")
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
		return "", nil, fmt.Errorf("JOIN 类型无效")
	}
	if j.Kind == sb.CrossJoin && j.On != nil {
		return "", nil, fmt.Errorf("CROSS JOIN 不接受 ON")
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
			return "", nil, fmt.Errorf("排序位置与表达式或 NULL 规则冲突")
		}
		direction := " ASC"
		if o.Descending {
			direction = " DESC"
		}
		return strconv.FormatUint(o.Position, 10) + direction, nil, nil
	}
	if o.Nulls != sb.NullsDefault && o.Nulls != sb.NullsFirst && o.Nulls != sb.NullsLast {
		return "", nil, fmt.Errorf("NULL 排序规则无效")
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
		return "", fmt.Errorf("标识符为空或包含非法字符")
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
		b.err = fmt.Errorf("查询节点生成空 SQL")
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
