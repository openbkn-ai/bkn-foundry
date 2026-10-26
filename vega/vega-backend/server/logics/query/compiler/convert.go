// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"strings"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	qt "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
)

// ConvertToIR 不解析 SQL、不读取资源或权限；输出仍须经过共同 Binder。
func ConvertToIR(tree *qt.Select) (*qm.SelectBuilder, error) {
	if err := ValidateQueryTree(tree); err != nil {
		return nil, err
	}
	b := irConverter{}
	return b.query(tree), nil
}

type irConverter struct {
	nextInstance qm.RelationInstanceID
}

// SQL 位置仅用于前端语法/策略错误，不进入下游查询对象。
func origin(_ qm.Span) qm.Origin { return qm.Origin{Kind: qm.OriginSQL} }

func (b *irConverter) query(q *qt.Select) *qm.SelectBuilder {
	parts := &qm.SelectBuilder{FromPart: b.relation(q.From), DistinctPart: q.Distinct}
	if q.Where != nil {
		parts.WherePart = []*qm.Sqlizer{expression(q.Where)}
	}
	for _, column := range q.Projections {
		parts.ColumnsPart = append(parts.ColumnsPart, qm.NamedExpr{Expr: expression(column.Expr), Alias: column.Alias, Origin: origin(column.Span)})
	}
	for _, key := range q.GroupBy {
		parts.GroupByPart = append(parts.GroupByPart, expression(key))
	}
	if q.Having != nil {
		parts.HavingPart = []*qm.Sqlizer{expression(q.Having)}
	}
	for _, key := range q.OrderBy {
		parts.OrderByPart = append(parts.OrderByPart, qm.SortKey{Expr: expression(key.Expr), Descending: key.Descending, Origin: origin(key.Span)})
	}
	if q.Limit != nil {
		parts.LimitPart = &qm.QueryWindow{Count: q.Limit.Count, Offset: q.Limit.Offset}
	}
	query := parts
	return query
}

func (b *irConverter) relation(r *qt.Relation) qm.Relation {
	switch r.Kind {
	case qt.Resource:
		b.nextInstance++
		return &qm.Scan{ResourceID: r.ResourceID, Alias: r.Alias, Instance: b.nextInstance, Origin: origin(r.Span)}
	case qt.Derived:
		query := b.query(r.Query)
		b.nextInstance++
		return &qm.Subquery{Query: query, Alias: r.Alias, Instance: b.nextInstance, Origin: origin(r.Span)}
	case qt.Join:
		return &qm.Join{Left: b.relation(r.Left), Right: b.relation(r.Right), Kind: r.JoinKind, On: expression(r.On), Origin: origin(r.Span)}
	default:
		// ValidateQueryTree 已排除未知 relation；新增种类必须同步策略与 IR 转换。
		panic("未检查的关系节点")
	}
}

func expression(e *qt.Expr) *qm.Sqlizer {
	if e == nil {
		return nil
	}
	result := &qm.Sqlizer{Kind: qm.ExprKind(e.Kind), Origin: origin(e.Span), Name: append([]string(nil), e.Name...), Op: qm.ScalarOperator(e.Op), Distinct: e.Distinct, Negated: e.Negated, CaseBase: e.CaseBase, CaseElse: e.CaseElse, CastType: e.CastType, CastParameters: append([]int(nil), e.CastParameters...)}
	if e.Escape != nil {
		escape := *e.Escape
		result.Escape = &escape
	}
	if e.Kind == qt.Literal {
		result.Literal = &qm.Literal{Kind: e.LiteralKind, Text: e.Text}
	}
	if e.Kind == qt.Call {
		result.Name[0] = strings.ToUpper(result.Name[0])
	}
	for _, arg := range e.Args {
		result.Args = append(result.Args, expression(arg))
	}
	return result
}
