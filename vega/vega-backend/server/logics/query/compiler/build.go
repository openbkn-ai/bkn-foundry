// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/sqlparser/generated"
)

type builder struct {
	positions []int
	nodes     int
	depths    map[*querytree.Expr]int
	err       error
}

func (b *builder) span(ctx antlr.ParserRuleContext) querymodel.Span {
	start := tokenSpan(ctx.GetStart(), b.positions)
	end := tokenSpan(ctx.GetStop(), b.positions)
	return querymodel.Span{Start: start.Start, End: end.End}
}

func (b *builder) count(span querymodel.Span) {
	b.nodes++
	if b.nodes > querymodel.MaxNodes && b.err == nil {
		b.err = compileError(querymodel.ErrorLimit, span, "Query Tree 节点数超限")
	}
}

func (b *builder) selectQuery(ctx generated.ISelectQueryContext) *querytree.Select {
	q := &querytree.Select{Span: b.span(ctx), Distinct: ctx.DISTINCT() != nil}
	b.count(q.Span)
	q.From = b.relation(ctx.Relation())
	for _, item := range ctx.AllSelectItem() {
		projection := querytree.Projection{Span: b.span(item)}
		if item.STAR() != nil {
			projection.Expr = &querytree.Expr{Kind: querytree.Wildcard, Span: b.span(item)}
			if item.Identifier() != nil {
				projection.Expr.Name = []string{identifier(item.Identifier())}
			}
			b.add(projection.Expr)
		} else {
			projection.Expr = b.expr(item.Expression())
			if item.Identifier() != nil {
				projection.Alias = identifier(item.Identifier())
			}
		}
		q.Projections = append(q.Projections, projection)
	}
	index := 0
	if ctx.WHERE() != nil {
		q.Where = b.expr(ctx.Expression(index))
		index++
	}
	if ctx.HAVING() != nil {
		q.Having = b.expr(ctx.Expression(index))
	}
	q.GroupBy = b.exprList(ctx.ExpressionList())
	for _, item := range ctx.AllSortItem() {
		q.OrderBy = append(q.OrderBy, querytree.SortItem{Expr: b.expr(item.Expression()), Descending: item.DESC() != nil, Span: b.span(item)})
	}
	if limit := ctx.LimitClause(); limit != nil {
		q.Limit = &querytree.Limit{Span: b.span(limit), Count: b.nonnegative(limit.NUMBER(0))}
		if limit.COMMA() != nil {
			q.Limit.Offset, q.Limit.Count = q.Limit.Count, b.nonnegative(limit.NUMBER(1))
		} else if limit.OFFSET() != nil {
			q.Limit.Offset = b.nonnegative(limit.NUMBER(1))
		}
	}
	return q
}

func (b *builder) nonnegative(token antlr.TerminalNode) int64 {
	value, err := strconv.ParseInt(token.GetText(), 10, 64)
	if err != nil && b.err == nil {
		b.err = compileError(querymodel.ErrorSyntax, tokenSpan(token.GetSymbol(), b.positions), "要求非负 int64 整数")
	}
	return value
}

func (b *builder) relation(ctx generated.IRelationContext) *querytree.Relation {
	left := b.primaryRelation(ctx.RelationPrimary())
	for _, part := range ctx.AllJoinPart() {
		kind := querymodel.JoinInner
		switch {
		case part.LEFT() != nil:
			kind = querymodel.JoinLeft
		case part.RIGHT() != nil:
			kind = querymodel.JoinRight
		case part.CROSS() != nil:
			kind = querymodel.JoinCross
		}
		joined := &querytree.Relation{Kind: querytree.Join, Span: querymodel.Span{Start: left.Span.Start, End: b.span(part).End}, Left: left, Right: b.primaryRelation(part.RelationPrimary()), JoinKind: kind}
		if part.Expression() != nil {
			joined.On = b.expr(part.Expression())
		}
		b.count(joined.Span)
		left = joined
	}
	return left
}

func (b *builder) primaryRelation(ctx generated.IRelationPrimaryContext) *querytree.Relation {
	r := &querytree.Relation{Span: b.span(ctx)}
	b.count(r.Span)
	if ctx.Identifier() != nil {
		r.Alias = identifier(ctx.Identifier())
	}
	if resource := ctx.RESOURCE(); resource != nil {
		r.Kind = querytree.Resource
		r.ResourceID = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(resource.GetText(), "{{"), "}}"))
	} else {
		r.Kind = querytree.Derived
		r.Query = b.selectQuery(ctx.SelectQuery())
	}
	return r
}

func (b *builder) add(e *querytree.Expr) *querytree.Expr {
	b.count(e.Span)
	depth := 1
	for _, arg := range e.Args {
		if d := b.depths[arg] + 1; d > depth {
			depth = d
		}
	}
	b.depths[e] = depth
	if depth > querymodel.MaxDepth && b.err == nil {
		b.err = compileError(querymodel.ErrorLimit, e.Span, "表达式深度超限")
	}
	return e
}

func (b *builder) expr(ctx antlr.Tree) *querytree.Expr {
	if b.err != nil {
		return &querytree.Expr{}
	}
	switch c := ctx.(type) {
	case generated.IExpressionContext:
		return b.expr(c.OrExpression())
	case generated.IOrExpressionContext, generated.IAndExpressionContext,
		generated.IAdditiveExpressionContext, generated.IMultiplicativeExpressionContext:
		return b.chain(ctx)
	case generated.INotExpressionContext:
		if c.NOT() == nil {
			return b.expr(c.Predicate())
		}
		return b.add(&querytree.Expr{Kind: querytree.Unary, Span: b.span(c), Op: "NOT", Args: []*querytree.Expr{b.expr(c.NotExpression())}})
	case generated.IUnaryExpressionContext:
		if c.PrimaryExpression() != nil {
			return b.expr(c.PrimaryExpression())
		}
		return b.add(&querytree.Expr{Kind: querytree.Unary, Span: b.span(c), Op: c.GetChild(0).(antlr.TerminalNode).GetText(), Args: []*querytree.Expr{b.expr(c.UnaryExpression())}})
	case generated.IPredicateContext:
		return b.predicate(c)
	case generated.IPrimaryExpressionContext:
		return b.primary(c)
	case generated.IFunctionCallContext:
		return b.function(c)
	case generated.ILiteralContext:
		return b.literal(c)
	default:
		b.err = compileError(querymodel.ErrorInvalidTree, querymodel.Span{}, "未知解析节点")
		return &querytree.Expr{}
	}
}

func (b *builder) chain(ctx antlr.Tree) *querytree.Expr {
	left := b.expr(ctx.GetChild(0))
	for i := 1; i < ctx.GetChildCount(); i += 2 {
		op := strings.ToUpper(ctx.GetChild(i).(antlr.TerminalNode).GetText())
		right := b.expr(ctx.GetChild(i + 1))
		left = b.add(&querytree.Expr{Kind: querytree.Binary, Op: op, Args: []*querytree.Expr{left, right}, Span: querymodel.Span{Start: left.Span.Start, End: right.Span.End}})
	}
	return left
}

func (b *builder) exprList(ctx generated.IExpressionListContext) []*querytree.Expr {
	if ctx == nil {
		return nil
	}
	result := make([]*querytree.Expr, 0, len(ctx.AllExpression()))
	for _, e := range ctx.AllExpression() {
		result = append(result, b.expr(e))
	}
	return result
}

func (b *builder) predicate(c generated.IPredicateContext) *querytree.Expr {
	left := b.expr(c.AdditiveExpression(0))
	if c.GetChildCount() == 1 {
		return left
	}
	e := &querytree.Expr{Span: b.span(c), Args: []*querytree.Expr{left}, Negated: c.NOT() != nil}
	switch {
	case c.IS() != nil:
		e.Kind = querytree.IsNull
	case c.BETWEEN() != nil:
		e.Kind = querytree.Between
		e.Args = append(e.Args, b.expr(c.AdditiveExpression(1)), b.expr(c.AdditiveExpression(2)))
	case c.IN() != nil:
		e.Kind = querytree.In
		list := c.ExpressionList()
		if len(list.AllExpression()) > querymodel.MaxINValues && b.err == nil {
			b.err = compileError(querymodel.ErrorLimit, b.span(c), "IN 元素数超限")
		}
		e.Args = append(e.Args, b.exprList(list)...)
	case c.LIKE() != nil:
		e.Kind = querytree.Like
		e.Args = append(e.Args, b.expr(c.AdditiveExpression(1)))
		escape := "\\"
		if c.ESCAPE() != nil {
			escape = decodeString(c.STRING().GetText())
		}
		if utf8.RuneCountInString(escape) != 1 && b.err == nil {
			b.err = compileError(querymodel.ErrorSyntax, e.Span, "LIKE ESCAPE 必须为单字符")
		}
		e.Escape = &escape
	default:
		e.Kind = querytree.Binary
		e.Op = c.GetChild(1).(antlr.TerminalNode).GetText()
		e.Args = append(e.Args, b.expr(c.AdditiveExpression(1)))
	}
	return b.add(e)
}

func (b *builder) primary(c generated.IPrimaryExpressionContext) *querytree.Expr {
	switch {
	case c.Literal() != nil:
		return b.literal(c.Literal())
	case c.FunctionCall() != nil:
		return b.function(c.FunctionCall())
	case c.CURRENT_TIMESTAMP() != nil:
		return b.add(&querytree.Expr{Kind: querytree.Call, Name: []string{"CURRENT_TIMESTAMP"}, Span: b.span(c)})
	case c.CAST() != nil:
		e := &querytree.Expr{Kind: querytree.Cast, Span: b.span(c), CastType: strings.ToUpper(identifier(c.TypeName().Identifier())), Args: []*querytree.Expr{b.expr(c.Expression(0))}}
		for _, param := range c.TypeName().AllNUMBER() {
			value := b.nonnegative(param)
			// CAST 类型参数不可能需要超过 int32 的精度或长度。
			if value > 2147483647 && b.err == nil {
				b.err = compileError(querymodel.ErrorSyntax, e.Span, "CAST 类型参数超限")
			}
			e.CastParameters = append(e.CastParameters, int(value))
		}
		return b.add(e)
	case c.CASE() != nil:
		e := &querytree.Expr{Kind: querytree.Case, Span: b.span(c), CaseElse: c.ELSE() != nil}
		e.CaseBase = c.Expression(0).GetStart().GetTokenIndex() < c.WHEN(0).GetSymbol().GetTokenIndex()
		for _, part := range c.AllExpression() {
			e.Args = append(e.Args, b.expr(part))
		}
		return b.add(e)
	case len(c.AllIdentifier()) > 0:
		e := &querytree.Expr{Kind: querytree.Name, Span: b.span(c)}
		for _, name := range c.AllIdentifier() {
			e.Name = append(e.Name, identifier(name))
		}
		return b.add(e)
	default:
		return b.expr(c.Expression(0))
	}
}

func (b *builder) function(c generated.IFunctionCallContext) *querytree.Expr {
	e := &querytree.Expr{Kind: querytree.Call, Span: b.span(c), Name: []string{strings.ToUpper(identifier(c.Identifier()))}, Distinct: c.DISTINCT() != nil}
	e.Args = b.exprList(c.ExpressionList())
	if c.STAR() != nil {
		e.Args = []*querytree.Expr{b.add(&querytree.Expr{Kind: querytree.Wildcard, Span: tokenSpan(c.STAR().GetSymbol(), b.positions)})}
	}
	return b.add(e)
}

func (b *builder) literal(c generated.ILiteralContext) *querytree.Expr {
	e := &querytree.Expr{Kind: querytree.Literal, Span: b.span(c), Text: c.GetText()}
	switch {
	case c.NULL() != nil:
		e.LiteralKind = querymodel.LiteralNull
		e.Text = ""
	case c.TRUE() != nil || c.FALSE() != nil:
		e.LiteralKind = querymodel.LiteralBoolean
		e.Text = strings.ToLower(e.Text)
	case c.STRING() != nil:
		e.LiteralKind = querymodel.LiteralString
		e.Text = decodeString(e.Text)
	default:
		e.LiteralKind = querymodel.LiteralInteger
		if strings.ContainsAny(e.Text, ".eE") {
			e.LiteralKind = querymodel.LiteralDecimal
		}
	}
	return b.add(e)
}

func identifier(c generated.IIdentifierContext) string {
	text := c.GetText()
	if c.QUOTED_IDENTIFIER() != nil {
		return strings.ReplaceAll(text[1:len(text)-1], "``", "`")
	}
	return text
}

// decodeString 固定采用 MySQL 风格字符串转义，与源端 SQL mode 无关。
func decodeString(text string) string {
	quote := text[0]
	text = text[1 : len(text)-1]
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch == quote && i+1 < len(text) && text[i+1] == quote {
			i++
		} else if ch == '\\' && i+1 < len(text) {
			i++
			switch text[i] {
			case '0':
				ch = 0
			case 'b':
				ch = '\b'
			case 'n':
				ch = '\n'
			case 'r':
				ch = '\r'
			case 't':
				ch = '\t'
			case 'Z':
				ch = 26
			case '%', '_':
				out.WriteByte('\\')
				ch = text[i]
			default:
				ch = text[i]
			}
		}
		out.WriteByte(ch)
	}
	return out.String()
}
