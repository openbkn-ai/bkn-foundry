// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package postgresql

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

// pgSQLParameter 保存绑定值及最终编号；复用片段共享参数引用，不合并裸参数。
type pgSQLParameter struct {
	value  any
	key    string
	number int
}

// pgSQLPart 区分 SQL 文本与参数，编号时不会扫描文本中的 $n 或问号。
type pgSQLPart struct {
	text      string
	parameter *pgSQLParameter
}

// pgSQLBuffer 记录一次生成的片段；表达式复用限定在当前 SELECT 作用域。
type pgSQLBuffer struct {
	tokens        []pgSQLPart
	err           error
	havingAliases map[string]sb.SqlNode
	expressions   map[string][]pgSQLPart
}

// ToSql 将公共 Builder 转换为 PostgreSQL SQL，保留参数精度，不修改查询节点。
func ToSql(query *sb.SelectBuilder) (string, []any, error) {
	if query == nil {
		return "", nil, fmt.Errorf("SELECT requires a query")
	}
	b := &pgSQLBuffer{}
	b.node(query)
	if b.err != nil {
		return "", nil, b.err
	}
	var text strings.Builder
	var args []any
	for _, part := range b.tokens {
		if part.parameter == nil {
			text.WriteString(part.text)
			continue
		}
		parameter := part.parameter
		if parameter.number == 0 {
			args = append(args, parameter.value)
			parameter.number = len(args)
		}
		text.WriteByte('$')
		text.WriteString(strconv.Itoa(parameter.number))
	}
	return text.String(), args, nil
}

// write 写入已生成的 SQL 文本，不处理绑定值。
func (b *pgSQLBuffer) write(text string) {
	if text != "" {
		b.tokens = append(b.tokens, pgSQLPart{text: text})
	}
}

// fail 保留首个生成错误，调用方不会获得部分 SQL。
func (b *pgSQLBuffer) fail(detail string) {
	if b.err == nil {
		b.err = fmt.Errorf("%s", detail)
	}
}

// identifier 引用单个标识符，点号和占位符符号均视为名称内容。
func (b *pgSQLBuffer) identifier(name string) {
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		b.fail("identifier is empty or invalid")
		return
	}
	b.write(pgQuoteIdent(name))
}

// parts 按语法顺序生成节点列表，共享当前参数编号。
func (b *pgSQLBuffer) parts(parts []sb.SqlNode, separator string) {
	for i, part := range parts {
		if i > 0 {
			b.write(separator)
		}
		b.node(part)
	}
}

// binary 用括号保留双目表达式的运算优先级。
func (b *pgSQLBuffer) binary(left sb.SqlNode, operator string, right sb.SqlNode) {
	b.write("(")
	b.node(left)
	b.write(" " + operator + " ")
	b.node(right)
	b.write(")")
}

// parameter 记录独立绑定引用；数字显式定型，编号留到最终输出时分配。
func (b *pgSQLBuffer) parameter(value any) {
	target := ""
	switch v := value.(type) {
	case json.Number:
		text := string(v)
		if text == "" || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
			b.fail("invalid numeric literal")
			return
		}
		switch {
		case strings.ContainsAny(text, "eE"):
			target = "DOUBLE PRECISION"
		case strings.Contains(text, "."):
			target = "NUMERIC"
		default:
			if _, err := strconv.ParseInt(text, 10, 64); err == nil {
				target = "BIGINT"
			} else {
				target = "NUMERIC"
			}
		}
		value = text
	case bool:
		target = "BOOLEAN"
	case int, int8, int16, int32, int64:
		target = "BIGINT"
	case uint, uint8, uint16, uint32, uint64:
		target = "NUMERIC"
		value = fmt.Sprint(v)
	case float32, float64:
		target = "DOUBLE PRECISION"
	}
	parameter := &pgSQLParameter{value: value}
	switch value.(type) {
	case nil, string, bool, json.Number, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		parameter.key = fmt.Sprintf("%T:%q", value, fmt.Sprint(value))
	default:
		// 不遍历业务数据，也不按内容合并未知驱动值。
		parameter.key = fmt.Sprintf("opaque:%p", parameter)
	}
	if target != "" {
		b.write("CAST(")
	}
	b.tokens = append(b.tokens, pgSQLPart{parameter: parameter})
	if target != "" {
		b.write(" AS " + target + ")")
	}
}

// node 仅复用已经生成的完整计算片段；字段、裸参数和关系节点分别处理。
func (b *pgSQLBuffer) node(node sb.SqlNode) {
	if b.err != nil {
		return
	}
	start := len(b.tokens)
	computed := true
	switch node.(type) {
	case *sb.Column, *sb.Param, *sb.Star, *sb.Alias, *sb.Table, *sb.Subquery,
		*sb.SelectBuilder, *sb.Join, *sb.JoinedTable, *sb.Order:
		computed = false
	}
	b.writeNode(node)
	if b.err != nil || !computed || b.expressions == nil {
		return
	}
	fragment := b.tokens[start:]
	key := pgFragmentKey(fragment)
	if previous, ok := b.expressions[key]; ok {
		b.tokens = append(b.tokens[:start], previous...)
	} else {
		// 后续父片段复用可能覆盖当前缓冲区，缓存独立保存 token 切片。
		b.expressions[key] = append([]pgSQLPart(nil), fragment...)
	}
}

// pgFragmentKey 只比较生成文本及参数类型/值，不再遍历 SQL 节点。
// 文本段和参数段使用长度边界，SQL 内的特殊字符不会成为参数标记。
func pgFragmentKey(parts []pgSQLPart) string {
	var key, text strings.Builder
	for _, part := range parts {
		if part.parameter == nil {
			text.WriteString(part.text)
			continue
		}
		key.WriteString(strconv.Itoa(text.Len()))
		key.WriteByte(':')
		key.WriteString(text.String())
		text.Reset()
		value := part.parameter.key
		key.WriteString(strconv.Itoa(len(value)))
		key.WriteByte(':')
		key.WriteString(value)
	}
	key.WriteString(strconv.Itoa(text.Len()))
	key.WriteByte(':')
	key.WriteString(text.String())
	return key.String()
}

// writeNode 是唯一的节点分派，生成 SQL 文本及参数引用。
// 内部构造保证具体节点指针非 nil；此处仅检查普通 nil，SQL NULL 使用 Param 表达。
func (b *pgSQLBuffer) writeNode(node sb.SqlNode) {
	if node == nil {
		b.fail("query node is nil")
		return
	}
	switch n := node.(type) {
	case *sb.SelectBuilder:
		b.selectQuery(n)
	case *sb.Column:
		if n.Qualifier == "" && b.havingAliases[n.Name] != nil {
			aliases := b.havingAliases
			b.havingAliases = nil
			b.node(aliases[n.Name])
			b.havingAliases = aliases
		} else {
			if n.Qualifier != "" {
				b.identifier(n.Qualifier)
				b.write(".")
			}
			b.identifier(n.Name)
		}
	case *sb.Param:
		b.parameter(n.Value)
	case *sb.Star:
		if n.Qualifier != "" {
			b.identifier(n.Qualifier)
			b.write(".")
		}
		b.write("*")
	case *sb.Alias:
		b.node(n.Expr)
		b.write(" AS ")
		b.identifier(n.Name)
	case *sb.Table:
		b.write(n.SourceIdentifier)
		if n.Alias != "" {
			b.write(" AS ")
			b.identifier(n.Alias)
		}
	case *sb.Subquery:
		b.write("(")
		b.node(n.Query)
		b.write(") AS ")
		b.identifier(n.Alias)
	case *sb.Join:
		b.join(*n)
	case *sb.JoinedTable:
		b.write("(")
		b.node(n.From)
		for _, j := range n.Joins {
			b.write(" ")
			b.join(j)
		}
		b.write(")")
	case *sb.Order:
		b.order(*n)
	case *sb.Eq:
		b.binary(n.Left, "=", n.Right)
	case *sb.NotEq:
		b.binary(n.Left, "<>", n.Right)
	case *sb.Gt:
		b.binary(n.Left, ">", n.Right)
	case *sb.GtOrEq:
		b.binary(n.Left, ">=", n.Right)
	case *sb.Lt:
		b.binary(n.Left, "<", n.Right)
	case *sb.LtOrEq:
		b.binary(n.Left, "<=", n.Right)
	case *sb.And:
		if len(*n) == 0 {
			b.write("TRUE")
		} else {
			b.write("(")
			b.parts(*n, " AND ")
			b.write(")")
		}
	case *sb.Or:
		if len(*n) == 0 {
			b.write("FALSE")
		} else {
			b.write("(")
			b.parts(*n, " OR ")
			b.write(")")
		}
	case *sb.Not:
		b.write("(NOT ")
		b.node(n.Expr)
		b.write(")")
	case *sb.IsNull:
		b.write("(")
		b.node(n.Expr)
		if n.Negated {
			b.write(" IS NOT NULL)")
		} else {
			b.write(" IS NULL)")
		}
	case *sb.In:
		if len(n.Values) == 0 {
			b.fail("IN list must not be empty")
			return
		}
		b.write("(")
		b.node(n.Expr)
		if n.Negated {
			b.write(" NOT")
		}
		b.write(" IN (")
		b.parts(n.Values, ", ")
		b.write("))")
	case *sb.Between:
		b.write("(")
		b.node(n.Expr)
		if n.Negated {
			b.write(" NOT")
		}
		b.write(" BETWEEN ")
		b.node(n.Lower)
		b.write(" AND ")
		b.node(n.Upper)
		b.write(")")
	case *sb.Like:
		if n.Escape != nil && (!utf8.ValidString(*n.Escape) || utf8.RuneCountInString(*n.Escape) != 1) {
			b.fail("LIKE escape must be one valid character")
			return
		}
		b.write("(")
		b.node(n.Expr)
		if n.Negated {
			b.write(" NOT")
		}
		b.write(" LIKE ")
		b.node(n.Pattern)
		if n.Escape != nil {
			b.write(" ESCAPE ")
			b.parameter(*n.Escape)
		}
		b.write(")")
	case *sb.Contains:
		b.write("(")
		b.node(n.Expr)
		b.write(" LIKE ('%' || REPLACE(REPLACE(REPLACE(CAST(")
		b.node(n.Value)
		b.write(" AS TEXT), '!', '!!'), '%', '!%'), '_', '!_') || '%') ESCAPE '!')")
	case *sb.Regex:
		b.binary(n.Expr, "~", n.Pattern)
	case *sb.Arithmetic:
		switch n.Op {
		case sb.Divide:
			// MySQL / 不使用 PG 的整数除法；先把两侧提升为精确数值。
			b.write("(CAST(")
			b.node(n.Left)
			b.write(" AS NUMERIC) / CAST(")
			b.node(n.Right)
			b.write(" AS NUMERIC))")
		case sb.Add, sb.Subtract, sb.Multiply, sb.Modulo:
			b.binary(n.Left, string(n.Op), n.Right)
		default:
			b.fail("invalid arithmetic operator")
		}
	case *sb.Unary:
		if n.Op != sb.Positive && n.Op != sb.Negative {
			b.fail("invalid unary operator")
			return
		}
		b.write("(" + string(n.Op))
		b.node(n.Expr)
		b.write(")")
	case *sb.Interval:
		switch n.Unit {
		case sb.IntervalMicrosecond, sb.IntervalSecond, sb.IntervalMinute, sb.IntervalHour, sb.IntervalDay, sb.IntervalWeek, sb.IntervalMonth, sb.IntervalYear:
			b.write("(CAST(")
			b.node(n.Value)
			b.write(" AS DOUBLE PRECISION) * INTERVAL '1 " + string(n.Unit) + "')")
		case sb.IntervalQuarter:
			b.write("(CAST(")
			b.node(n.Value)
			b.write(" AS DOUBLE PRECISION) * INTERVAL '3 months')")
		default:
			b.fail("unsupported interval unit")
		}
	case *sb.Function:
		b.function(*n)
	case *sb.Case:
		if len(n.WhenParts) == 0 {
			b.fail("CASE requires a WHEN branch")
			return
		}
		b.write("CASE")
		if n.Operand != nil {
			b.write(" ")
			b.node(n.Operand)
		}
		for _, w := range n.WhenParts {
			b.write(" WHEN ")
			b.node(w.Condition)
			b.write(" THEN ")
			b.node(w.Result)
		}
		if n.Else != nil {
			b.write(" ELSE ")
			b.node(n.Else)
		}
		b.write(" END")
	case *sb.Cast:
		b.cast(*n)
	default:
		b.fail("unsupported PostgreSQL query node")
	}
}

// selectQuery 保留当前 SELECT 的子句边界，HAVING 别名不会泄漏到嵌套查询。
func (b *pgSQLBuffer) selectQuery(q *sb.SelectBuilder) {
	if len(q.ColumnsPart) == 0 {
		b.fail("SELECT requires a projection")
		return
	}
	if q.FromPart == nil && len(q.JoinsPart) > 0 {
		b.fail("JOIN requires a FROM source")
		return
	}
	previous := b.havingAliases
	previousExpressions := b.expressions
	b.havingAliases = nil
	b.expressions = nil
	if len(q.GroupByPart) > 0 {
		b.expressions = map[string][]pgSQLPart{}
	}
	b.write("SELECT ")
	if q.DistinctPart {
		b.write("DISTINCT ")
	}
	b.parts(q.ColumnsPart, ", ")
	if q.FromPart != nil {
		b.write(" FROM ")
		b.node(q.FromPart)
	}
	for _, j := range q.JoinsPart {
		b.write(" ")
		b.join(j)
	}
	if len(q.WherePart) > 0 {
		b.write(" WHERE ")
		b.parts(q.WherePart, " AND ")
	}
	if len(q.GroupByPart) > 0 {
		b.write(" GROUP BY ")
		for i, group := range q.GroupByPart {
			if i > 0 {
				b.write(", ")
			}
			// 整数字面量保留位置语义，范围及聚合合法性由源端判断。
			if param, ok := group.(*sb.Param); ok && param != nil {
				if number, ok := param.Value.(json.Number); ok && len(number) > 0 && strings.IndexFunc(string(number), func(r rune) bool { return r < '0' || r > '9' }) == -1 {
					b.write(string(number))
					continue
				}
			}
			b.node(group)
		}
	}
	if len(q.HavingPart) > 0 {
		// PG HAVING 不识别投影别名，仅在该子句展开当前 SELECT 的别名。
		b.havingAliases = map[string]sb.SqlNode{}
		for _, column := range q.ColumnsPart {
			if alias, ok := column.(*sb.Alias); ok && alias != nil {
				b.havingAliases[alias.Name] = alias.Expr
			}
		}
		b.write(" HAVING ")
		b.parts(q.HavingPart, " AND ")
		b.havingAliases = nil
	}
	if len(q.OrderByPart) > 0 {
		b.write(" ORDER BY ")
		for i, o := range q.OrderByPart {
			if i > 0 {
				b.write(", ")
			}
			b.order(o)
		}
	}
	if q.LimitPart != nil {
		b.write(" LIMIT " + strconv.FormatUint(*q.LimitPart, 10))
	}
	if q.OffsetPart != 0 {
		b.write(" OFFSET " + strconv.FormatUint(q.OffsetPart, 10))
	}
	b.havingAliases = previous
	b.expressions = previousExpressions
}

// join 输出明确的连接类型，普通连接必须具有 ON 条件。
func (b *pgSQLBuffer) join(j sb.Join) {
	keyword := ""
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
		b.fail("invalid JOIN kind")
		return
	}
	if j.Kind == sb.CrossJoin && j.On != nil {
		b.fail("CROSS JOIN does not accept ON")
		return
	}
	b.write(keyword + " ")
	b.node(j.Source)
	if j.Kind != sb.CrossJoin {
		b.write(" ON ")
		b.node(j.On)
	}
}

// order 使用 PG 原生 NULLS 子句兑现公共排序规则。
func (b *pgSQLBuffer) order(o sb.Order) {
	if o.Nulls != sb.NullsDefault && o.Nulls != sb.NullsFirst && o.Nulls != sb.NullsLast {
		b.fail("invalid NULL ordering")
		return
	}
	if o.Position != 0 {
		if o.Expr != nil {
			b.fail("ORDER BY position conflicts with expression")
			return
		}
		b.write(strconv.FormatUint(o.Position, 10))
	} else {
		b.node(o.Expr)
	}
	if o.Descending {
		b.write(" DESC")
	} else {
		b.write(" ASC")
	}
	// 公共 Builder 的默认规则是 ASC NULLS FIRST / DESC NULLS LAST。
	first := o.Nulls == sb.NullsFirst || (o.Nulls == sb.NullsDefault && !o.Descending)
	if first {
		b.write(" NULLS FIRST")
	} else {
		b.write(" NULLS LAST")
	}
}

// cast 将逻辑类型映射为 PG 类型；没有对应能力时明确拒绝。
func (b *pgSQLBuffer) cast(c sb.Cast) {
	target := ""
	maxParameters := 0
	switch c.Type {
	case sb.CastInteger:
		target = "BIGINT"
	case sb.CastUnsignedInteger:
		b.fail("PostgreSQL does not support unsigned integer casts")
		return
	case sb.CastDecimal:
		target = "NUMERIC"
		maxParameters = 2
		if len(c.Parameters) == 0 {
			target = "NUMERIC(10,0)"
		}
		if len(c.Parameters) > 0 && (c.Parameters[0] == 0 || c.Parameters[0] > 1000) {
			b.fail("NUMERIC precision is out of range")
			return
		}
		if len(c.Parameters) > 1 && c.Parameters[1] > c.Parameters[0] {
			b.fail("NUMERIC scale exceeds precision")
			return
		}
	case sb.CastString:
		target = "TEXT"
		if len(c.Parameters) > 0 {
			target = "VARCHAR"
		}
		maxParameters = 1
	case sb.CastBinary:
		target = "BYTEA"
	case sb.CastBoolean:
		target = "BOOLEAN"
	case sb.CastDate:
		target = "DATE"
	case sb.CastDateTime, sb.CastTimestamp:
		target = "TIMESTAMP"
		maxParameters = 1
	case sb.CastTime:
		target = "TIME"
		maxParameters = 1
	case sb.CastFloat:
		target = "REAL"
	case sb.CastDouble:
		target = "DOUBLE PRECISION"
	case sb.CastJSON:
		target = "JSON"
	default:
		b.fail("unsupported PostgreSQL cast type")
		return
	}
	if len(c.Parameters) > maxParameters {
		b.fail("invalid CAST type parameter count")
		return
	}
	if (target == "TIMESTAMP" || target == "TIME") && len(c.Parameters) > 0 && c.Parameters[0] > 6 {
		b.fail("temporal precision is out of range")
		return
	}
	if len(c.Parameters) > 0 {
		values := make([]string, len(c.Parameters))
		for i, p := range c.Parameters {
			values[i] = strconv.FormatUint(p, 10)
		}
		target += "(" + strings.Join(values, ",") + ")"
	}
	b.write("CAST(")
	b.node(c.Expr)
	b.write(" AS " + target + ")")
}

// function 显式映射输入函数；没有可靠映射的调用在执行前拒绝，不回退拼接 MySQL SQL。
func (b *pgSQLBuffer) function(f sb.Function) {
	name := strings.ToUpper(f.Name)
	if name == "CEILING" {
		name = "CEIL"
	}
	switch name {
	case "COUNT", "SUM", "AVG", "MIN", "MAX", "ABS", "FLOOR", "CEIL", "CEILING", "COALESCE", "NULLIF", "UPPER", "LOWER", "CHAR_LENGTH", "TRIM", "LTRIM", "RTRIM", "REPLACE", "OCTET_LENGTH", "TO_CHAR", "DATE_TRUNC", "TRANSACTION_TIMESTAMP":
		if name == "COUNT" && f.Distinct && len(f.Args) > 1 {
			b.write("COUNT(DISTINCT ROW(")
			b.parts(f.Args, ", ")
			b.write(")) FILTER (WHERE ")
			for i, arg := range f.Args {
				if i > 0 {
					b.write(" AND ")
				}
				b.node(arg)
				b.write(" IS NOT NULL")
			}
			b.write(")")
			return
		}
	case "FROM_UNIXTIME":
		if len(f.Args) != 1 || f.Distinct {
			b.fail("FROM_UNIXTIME requires one argument")
			return
		}
		b.write("TO_TIMESTAMP(CAST(")
		b.node(f.Args[0])
		b.write(" AS DOUBLE PRECISION))")
		return
	case "FIND_IN_SET":
		if len(f.Args) != 2 || f.Distinct {
			b.fail("FIND_IN_SET requires two arguments")
			return
		}
		b.write("CASE WHEN ")
		b.node(f.Args[1])
		b.write(" IS NULL THEN NULL ELSE COALESCE(ARRAY_POSITION(STRING_TO_ARRAY(CAST(")
		b.node(f.Args[1])
		b.write(" AS TEXT), ','), CAST(")
		b.node(f.Args[0])
		b.write(" AS TEXT)), 0) END")
		return
	case "IFNULL":
		name = "COALESCE"
	case "LENGTH":
		name = "OCTET_LENGTH"
	case "CURDATE":
		if len(f.Args) != 0 {
			b.fail("invalid CURDATE arguments")
			return
		}
		b.write("CURRENT_DATE")
		return
	case "NOW", "CURRENT_TIMESTAMP":
		if f.Distinct || len(f.Args) > 1 {
			b.fail("invalid temporal function arguments")
			return
		}
		b.write("LOCALTIMESTAMP")
		if len(f.Args) == 1 {
			p, ok := f.Args[0].(*sb.Param)
			if !ok || p == nil {
				b.fail("temporal precision must be a literal")
				return
			}
			n, ok := p.Value.(json.Number)
			v, err := strconv.ParseUint(string(n), 10, 8)
			if !ok || err != nil || v > 6 {
				b.fail("temporal precision is out of range")
				return
			}
			b.write("(" + strconv.FormatUint(v, 10) + ")")
		}
		return
	case "DATE":
		if len(f.Args) != 1 {
			b.fail("DATE requires one argument")
			return
		}
		b.cast(sb.Cast{Expr: f.Args[0], Type: sb.CastDate})
		return
	case "YEAR", "MONTH", "DAY", "DAYOFMONTH", "HOUR", "MINUTE", "SECOND":
		if len(f.Args) != 1 {
			b.fail("date part requires one argument")
			return
		}
		if name == "DAYOFMONTH" {
			name = "DAY"
		}
		target := "TIMESTAMP"
		if name == "HOUR" || name == "MINUTE" || name == "SECOND" {
			target = "TIME"
		}
		b.write("CAST(TRUNC(EXTRACT(" + name + " FROM CAST(")
		b.node(f.Args[0])
		b.write(" AS " + target + "))) AS BIGINT)")
		return
	case "CONCAT":
		if len(f.Args) == 0 {
			b.fail("CONCAT requires arguments")
			return
		}
		b.write("(")
		for i, arg := range f.Args {
			if i > 0 {
				b.write(" || ")
			}
			b.write("CAST(")
			b.node(arg)
			b.write(" AS TEXT)")
		}
		b.write(")")
		return
	case "CONCAT_WS":
		b.write("CONCAT_WS(")
		for i, arg := range f.Args {
			if i > 0 {
				b.write(", ")
			}
			b.write("CAST(")
			b.node(arg)
			b.write(" AS TEXT)")
		}
		b.write(")")
		return
	case "SUBSTR", "SUBSTRING":
		if len(f.Args) < 2 || len(f.Args) > 3 {
			b.fail("invalid SUBSTRING arguments")
			return
		}
		b.substring(f.Args)
		return

	case "ROUND":
		if len(f.Args) < 1 || len(f.Args) > 2 {
			b.fail("invalid ROUND arguments")
			return
		}
		b.write("ROUND(CAST(")
		b.node(f.Args[0])
		b.write(" AS NUMERIC)")
		if len(f.Args) == 2 {
			b.write(", CAST(")
			b.node(f.Args[1])
			b.write(" AS INTEGER)")
		}
		b.write(")")
		return
	case "DATE_FORMAT", "STR_TO_DATE":
		if len(f.Args) != 2 {
			b.fail("date formatting requires two arguments")
			return
		}
		p, ok := f.Args[1].(*sb.Param)
		if !ok || p == nil {
			b.fail("date format must be a string literal")
			return
		}
		format, ok := p.Value.(string)
		if !ok {
			b.fail("date format must be a string literal")
			return
		}
		mapped, err := pgDateFormat(format)
		if err != nil {
			b.err = err
			return
		}
		if name == "DATE_FORMAT" {
			b.write("TO_CHAR(CAST(")
			b.node(f.Args[0])
			b.write(" AS TIMESTAMP), ")
			b.parameter(mapped)
			b.write(")")
		} else {
			b.write("CAST(TO_TIMESTAMP(CAST(")
			b.node(f.Args[0])
			b.write(" AS TEXT), ")
			b.parameter(mapped)
			b.write(") AS TIMESTAMP)")
		}
		return
	default:
		b.fail("function has no PostgreSQL mapping")
		return
	}
	b.write(name + "(")
	if f.Distinct {
		b.write("DISTINCT ")
	}
	b.parts(f.Args, ", ")
	b.write(")")
}

// pgDateFormat 只转换已定义的常用格式标记，未知标记拒绝而不是当作普通文字。
func pgDateFormat(format string) (string, error) {
	if !utf8.ValidString(format) {
		return "", fmt.Errorf("invalid UTF-8 date format")
	}
	tokens := map[byte]string{'Y': "YYYY", 'y': "YY", 'm': "MM", 'd': "DD", 'H': "HH24", 'h': "HH12", 'i': "MI", 's': "SS", 'S': "SS", 'f': "US", 'p': "AM", 'x': "IYYY", 'v': "IW"}
	var result strings.Builder
	for i := 0; i < len(format); {
		if format[i] == '%' {
			i++
			if i == len(format) {
				return "", fmt.Errorf("incomplete date format")
			}
			if format[i] == '%' {
				result.WriteString(`"%"`)
			} else {
				token, ok := tokens[format[i]]
				if !ok {
					return "", fmt.Errorf("date format token has no PostgreSQL mapping")
				}
				result.WriteString(token)
			}
			i++
		} else {
			start := i
			for i < len(format) && format[i] != '%' {
				i++
			}
			result.WriteByte('"')
			result.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(format[start:i]))
			result.WriteByte('"')
		}
	}
	return result.String(), nil
}

// substring 将零、负起点及负长度按输入语言的语义展开，避免 PG 原生 SUBSTRING 改变结果。
func (b *pgSQLBuffer) substring(args []sb.SqlNode) {
	text := &sb.Cast{Expr: args[0], Type: sb.CastString}
	b.write("CASE WHEN ")
	b.node(args[1])
	b.write(" = 0 OR ")
	b.node(args[1])
	b.write(" < -CHAR_LENGTH(")
	b.node(text)
	b.write(")")
	if len(args) == 3 {
		b.write(" OR ")
		b.node(args[2])
		b.write(" < 0")
	}
	b.write(" THEN '' ELSE SUBSTRING(")
	b.node(text)
	b.write(", CAST(CASE WHEN ")
	b.node(args[1])
	b.write(" < 0 THEN CHAR_LENGTH(")
	b.node(text)
	b.write(") + ")
	b.node(args[1])
	b.write(" + 1 ELSE ")
	b.node(args[1])
	b.write(" END AS INTEGER)")
	if len(args) == 3 {
		b.write(", CAST(")
		b.node(args[2])
		b.write(" AS INTEGER)")
	}
	b.write(") END")
}
