// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	g "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler/sqlparser/generated"
)

// treeBuilder 只保存一次转换的预算和首个错误，不生成另一份 AST 或绑定元数据。
// 表达式均创建为具体节点指针，depths 仅用于检查最终表达式的真实深度。
type treeBuilder struct {
	err    *ParseError
	nodes  int
	depths map[sb.SqlNode]int
}

func (b *treeBuilder) query(ctx g.ISelectQueryContext) *sb.SelectBuilder {
	query := sb.Select().From(b.source(ctx.Relation().RelationPrimary()))
	if ctx.DISTINCT() != nil {
		query.Distinct()
	}
	for _, item := range ctx.AllSelectItem() {
		if item.STAR() != nil {
			star := &sb.Star{}
			if item.Identifier() != nil {
				star.Qualifier = identifier(item.Identifier())
			}
			query.Columns(star)
			continue
		}
		expr := b.expr(item.Expression())
		if item.Identifier() != nil {
			expr = &sb.Alias{Expr: expr, Name: identifier(item.Identifier())}
		}
		query.Columns(expr)
	}
	for _, part := range ctx.Relation().AllJoinPart() {
		join := sb.Join{Kind: sb.InnerJoin, Source: b.source(part.RelationPrimary())}
		switch {
		case part.LEFT() != nil:
			join.Kind = sb.LeftJoin
		case part.RIGHT() != nil:
			join.Kind = sb.RightJoin
		case part.CROSS() != nil:
			join.Kind = sb.CrossJoin
		}
		if part.Expression() != nil {
			join.On = b.expr(part.Expression())
		}
		query.JoinClause(join)
	}
	index := 0
	if ctx.WHERE() != nil {
		query.Where(b.expr(ctx.Expression(index)))
		index++
	}
	if ctx.ExpressionList() != nil {
		query.GroupBy(b.expressions(ctx.ExpressionList())...)
	}
	if ctx.HAVING() != nil {
		query.Having(b.expr(ctx.Expression(index)))
	}
	for _, order := range ctx.AllSortItem() {
		item := sb.Order{Expr: b.expr(order.Expression()), Descending: order.DESC() != nil}
		if param, ok := item.Expr.(*sb.Param); ok {
			if number, ok := param.Value.(json.Number); ok && !strings.ContainsAny(string(number), ".eE") {
				position, err := strconv.ParseUint(string(number), 10, 64)
				if err != nil || position == 0 {
					b.fail(CodeSyntax, order, "排序位置必须为正整数")
				}
				item.Position, item.Expr = position, nil
			}
		}
		query.OrderBy(item)
	}
	if limit := ctx.LimitClause(); limit != nil {
		first := b.unsigned(limit.NUMBER(0).GetSymbol())
		query.Limit(first)
		if len(limit.AllNUMBER()) == 2 {
			second := b.unsigned(limit.NUMBER(1).GetSymbol())
			if limit.COMMA() != nil {
				query.Offset(first).Limit(second)
			} else {
				query.Offset(second)
			}
		}
	}
	return query
}

func (b *treeBuilder) source(ctx g.IRelationPrimaryContext) sb.SqlNode {
	alias := ""
	if ctx.Identifier() != nil {
		alias = identifier(ctx.Identifier())
	}
	if resource := ctx.RESOURCE(); resource != nil {
		text := resource.GetText()
		return &sb.Table{ResourceID: strings.TrimSpace(text[2 : len(text)-2]), Alias: alias}
	}
	return &sb.Subquery{Query: b.query(ctx.SelectQuery()), Alias: alias}
}

func (b *treeBuilder) expr(ctx g.IExpressionContext) sb.SqlNode {
	if b.err != nil {
		return nil
	}
	return b.or(ctx.OrExpression())
}

func (b *treeBuilder) expressions(ctx g.IExpressionListContext) []sb.SqlNode {
	result := make([]sb.SqlNode, 0, len(ctx.AllExpression()))
	for _, expr := range ctx.AllExpression() {
		result = append(result, b.expr(expr))
	}
	return result
}

func (b *treeBuilder) or(ctx g.IOrExpressionContext) sb.SqlNode {
	parts := ctx.AllAndExpression()
	if len(parts) == 1 {
		return b.and(parts[0])
	}
	result := make(sb.Or, 0, len(parts))
	for _, part := range parts {
		result = append(result, b.and(part))
	}
	return b.node(ctx, &result, result...)
}

func (b *treeBuilder) and(ctx g.IAndExpressionContext) sb.SqlNode {
	parts := ctx.AllNotExpression()
	if len(parts) == 1 {
		return b.not(parts[0])
	}
	result := make(sb.And, 0, len(parts))
	for _, part := range parts {
		result = append(result, b.not(part))
	}
	return b.node(ctx, &result, result...)
}

func (b *treeBuilder) not(ctx g.INotExpressionContext) sb.SqlNode {
	if ctx.NOT() != nil {
		expr := b.not(ctx.NotExpression())
		return b.node(ctx, &sb.Not{Expr: expr}, expr)
	}
	return b.predicate(ctx.Predicate())
}

func (b *treeBuilder) predicate(ctx g.IPredicateContext) sb.SqlNode {
	left := b.add(ctx.AdditiveExpression(0))
	negated := ctx.NOT() != nil
	switch {
	case ctx.IS() != nil:
		return b.node(ctx, &sb.IsNull{Expr: left, Negated: negated}, left)
	case ctx.IN() != nil:
		if len(ctx.ExpressionList().AllExpression()) > maxINValues {
			b.fail(CodeLimit, ctx, "IN 列表长度超限")
			return nil
		}
		values := b.expressions(ctx.ExpressionList())
		return b.node(ctx, &sb.In{Expr: left, Values: values, Negated: negated}, append([]sb.SqlNode{left}, values...)...)
	case ctx.BETWEEN() != nil:
		lower, upper := b.add(ctx.AdditiveExpression(1)), b.add(ctx.AdditiveExpression(2))
		return b.node(ctx, &sb.Between{Expr: left, Lower: lower, Upper: upper, Negated: negated}, left, lower, upper)
	case ctx.LIKE() != nil:
		pattern := b.add(ctx.AdditiveExpression(1))
		escape := "\\"
		if ctx.STRING() != nil {
			escape = stringValue(ctx.STRING().GetText())
		}
		if utf8.RuneCountInString(escape) != 1 {
			b.fail(CodeSyntax, ctx, "LIKE 转义字符必须是单个字符")
		}
		return b.node(ctx, &sb.Like{Expr: left, Pattern: pattern, Escape: &escape, Negated: negated}, left, pattern)
	}
	if len(ctx.AllAdditiveExpression()) == 1 {
		return left
	}
	right := b.add(ctx.AdditiveExpression(1))
	var result sb.SqlNode
	switch {
	case ctx.EQ() != nil:
		result = &sb.Eq{Left: left, Right: right}
	case ctx.NEQ() != nil:
		result = &sb.NotEq{Left: left, Right: right}
	case ctx.LT() != nil:
		result = &sb.Lt{Left: left, Right: right}
	case ctx.LTE() != nil:
		result = &sb.LtOrEq{Left: left, Right: right}
	case ctx.GT() != nil:
		result = &sb.Gt{Left: left, Right: right}
	case ctx.GTE() != nil:
		result = &sb.GtOrEq{Left: left, Right: right}
	}
	return b.node(ctx, result, left, right)
}

func (b *treeBuilder) add(ctx g.IAdditiveExpressionContext) sb.SqlNode {
	parts := ctx.AllMultiplicativeExpression()
	result := b.multiply(parts[0])
	for i := 1; i < len(parts); i++ {
		right := b.multiply(parts[i])
		op := ctx.GetChild(2*i - 1).(antlr.TerminalNode).GetText()
		result = b.node(ctx, &sb.Arithmetic{Left: result, Right: right, Op: sb.ArithmeticOperator(op)}, result, right)
	}
	return result
}

func (b *treeBuilder) multiply(ctx g.IMultiplicativeExpressionContext) sb.SqlNode {
	parts := ctx.AllUnaryExpression()
	result := b.unary(parts[0])
	for i := 1; i < len(parts); i++ {
		right := b.unary(parts[i])
		op := ctx.GetChild(2*i - 1).(antlr.TerminalNode).GetText()
		result = b.node(ctx, &sb.Arithmetic{Left: result, Right: right, Op: sb.ArithmeticOperator(op)}, result, right)
	}
	return result
}

func (b *treeBuilder) unary(ctx g.IUnaryExpressionContext) sb.SqlNode {
	if ctx.PrimaryExpression() != nil {
		return b.primary(ctx.PrimaryExpression())
	}
	expr := b.unary(ctx.UnaryExpression())
	op := sb.Positive
	if ctx.MINUS() != nil {
		op = sb.Negative
	}
	return b.node(ctx, &sb.Unary{Expr: expr, Op: op}, expr)
}

func (b *treeBuilder) primary(ctx g.IPrimaryExpressionContext) sb.SqlNode {
	switch {
	case ctx.Literal() != nil:
		literal := ctx.Literal()
		var value any
		switch {
		case literal.NUMBER() != nil:
			raw := literal.NUMBER().GetText()
			end := strings.IndexAny(raw, ".eE")
			if end < 0 {
				end = len(raw)
			}
			integer := strings.TrimLeft(raw[:end], "0")
			if integer == "" {
				integer = "0"
			}
			value = json.Number(integer + raw[end:])
		case literal.STRING() != nil:
			value = stringValue(literal.STRING().GetText())
		case literal.TRUE() != nil:
			value = true
		case literal.FALSE() != nil:
			value = false
		}
		return b.node(ctx, &sb.Param{Value: value})
	case ctx.CURRENT_TIMESTAMP() != nil:
		return b.node(ctx, &sb.Function{Name: "CURRENT_TIMESTAMP"})
	case ctx.FunctionCall() != nil:
		call := ctx.FunctionCall()
		var args []sb.SqlNode
		if call.STAR() != nil {
			args = []sb.SqlNode{b.node(ctx, &sb.Star{})}
		}
		if call.ExpressionList() != nil {
			args = b.expressions(call.ExpressionList())
		}
		name := strings.ToUpper(identifier(call.Identifier()))
		if !allowedSQLFunction(name) {
			b.fail(CodeUnsupported, ctx, "函数未开放")
			return nil
		}
		return b.node(ctx, &sb.Function{Name: name, Args: args, Distinct: call.DISTINCT() != nil}, args...)
	case ctx.CAST() != nil:
		expr := b.expr(ctx.Expression(0))
		typeName := ctx.TypeName()
		var parameters []uint64
		for _, number := range typeName.AllNUMBER() {
			parameters = append(parameters, b.unsigned(number.GetSymbol()))
		}
		return b.node(ctx, &sb.Cast{Expr: expr, Type: b.castType(typeName), Parameters: parameters}, expr)
	case ctx.CASE() != nil:
		return b.caseExpression(ctx)
	case len(ctx.AllIdentifier()) != 0:
		column := &sb.Column{Name: identifier(ctx.Identifier(0))}
		if len(ctx.AllIdentifier()) == 2 {
			column.Qualifier = column.Name
			column.Name = identifier(ctx.Identifier(1))
		}
		return b.node(ctx, column)
	default:
		return b.expr(ctx.Expression(0))
	}
}

func (b *treeBuilder) caseExpression(ctx g.IPrimaryExpressionContext) sb.SqlNode {
	result := &sb.Case{}
	parts := ctx.AllExpression()
	count := len(ctx.AllWHEN())
	expected := count * 2
	if ctx.ELSE() != nil {
		expected++
	}
	index := 0
	var children []sb.SqlNode
	if len(parts) > expected {
		result.Operand = b.expr(parts[0])
		children = append(children, result.Operand)
		index++
	}
	for i := 0; i < count; i++ {
		condition, value := b.expr(parts[index]), b.expr(parts[index+1])
		result.WhenParts = append(result.WhenParts, sb.When{Condition: condition, Result: value})
		children = append(children, condition, value)
		index += 2
	}
	if ctx.ELSE() != nil {
		result.Else = b.expr(parts[index])
		children = append(children, result.Else)
	}
	return b.node(ctx, result, children...)
}

func (b *treeBuilder) node(ctx antlr.ParserRuleContext, node sb.SqlNode, children ...sb.SqlNode) sb.SqlNode {
	if b.err != nil {
		return nil
	}
	if node == nil {
		b.fail(CodeUnsupported, ctx, "表达式节点未开放")
		return nil
	}
	depth := 1
	for _, child := range children {
		depth = max(depth, b.depths[child]+1)
	}
	b.nodes++
	if depth > maxDepth || b.nodes > maxNodes {
		b.fail(CodeLimit, ctx, "表达式深度或节点数量超限")
		return nil
	}
	b.depths[node] = depth
	return node
}

func (b *treeBuilder) unsigned(token antlr.Token) uint64 {
	value, err := strconv.ParseUint(token.GetText(), 10, 64)
	if err != nil && b.err == nil {
		b.err = errorAt(CodeSyntax, token, "LIMIT 或类型参数必须是 uint64 范围内的整数")
	}
	return value
}

func (b *treeBuilder) fail(code ErrorCode, ctx antlr.ParserRuleContext, message string) {
	if b.err == nil {
		b.err = errorAt(code, ctx.GetStart(), message)
	}
}

func identifier(ctx g.IIdentifierContext) string {
	text := ctx.GetText()
	if text[0] == '`' {
		return strings.ReplaceAll(text[1:len(text)-1], "``", "`")
	}
	return text
}

// stringValue 解码 Vega 输入语言的 MySQL 风格字符串，不读取源端 SQL mode。
func stringValue(text string) string {
	var result strings.Builder
	quote := text[0]
	for i := 1; i < len(text)-1; i++ {
		value := text[i]
		if value == quote && i+1 < len(text)-1 && text[i+1] == quote {
			i++
		} else if value == '\\' {
			i++
			value = text[i]
			switch value {
			case '0':
				value = 0
			case 'b':
				value = '\b'
			case 'n':
				value = '\n'
			case 'r':
				value = '\r'
			case 't':
				value = '\t'
			case 'Z':
				value = 26
			case '%', '_':
				_ = result.WriteByte('\\')
			}
		}
		_ = result.WriteByte(value)
	}
	return result.String()
}

// castType 将输入语言的类型别名归一为逻辑类型，不选择物理方言。
func (b *treeBuilder) castType(ctx g.ITypeNameContext) sb.CastType {
	switch strings.ToUpper(identifier(ctx.Identifier())) {
	case "SIGNED", "INT", "INTEGER", "BIGINT":
		return sb.CastInteger
	case "UNSIGNED":
		return sb.CastUnsignedInteger
	case "DECIMAL":
		return sb.CastDecimal
	case "CHAR", "VARCHAR", "STRING":
		return sb.CastString
	case "BINARY":
		return sb.CastBinary
	case "BOOL", "BOOLEAN":
		return sb.CastBoolean
	case "DATE":
		return sb.CastDate
	case "DATETIME":
		return sb.CastDateTime
	case "TIMESTAMP":
		return sb.CastTimestamp
	case "TIME":
		return sb.CastTime
	case "FLOAT":
		return sb.CastFloat
	case "DOUBLE":
		return sb.CastDouble
	case "JSON":
		return sb.CastJSON
	default:
		b.fail(CodeUnsupported, ctx, "转换目标类型未开放")
		return ""
	}
}

// allowedSQLFunction 只开放已明确为只读的内置函数，避免透传 UDF 或有副作用的调用。
func allowedSQLFunction(name string) bool {
	switch name {
	case "COUNT", "SUM", "AVG", "MIN", "MAX", "ABS", "ROUND", "FLOOR", "CEIL", "CEILING",
		"COALESCE", "IFNULL", "NULLIF", "CONCAT", "CONCAT_WS", "UPPER", "LOWER", "LENGTH", "CHAR_LENGTH",
		"SUBSTRING", "SUBSTR", "TRIM", "LTRIM", "RTRIM", "REPLACE", "DATE", "YEAR", "MONTH", "DAY", "DAYOFMONTH",
		"HOUR", "MINUTE", "SECOND", "DATE_FORMAT", "STR_TO_DATE", "NOW", "CURRENT_TIMESTAMP", "CURDATE":
		return true
	default:
		return false
	}
}
