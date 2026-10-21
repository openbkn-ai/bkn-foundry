// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"slices"
	"strings"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	qt "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
)

// ConvertToIR 不解析 SQL、不读取资源或权限；输出仍须经过共同 Binder。
func ConvertToIR(tree *qt.Select) (*qm.UnboundQuery, error) {
	if err := ValidateQueryTree(tree); err != nil {
		return nil, err
	}
	b := irConverter{}
	return b.query(tree), nil
}

type irConverter struct {
	nextScope    qm.ScopeID
	nextInstance qm.RelationInstanceID
}

func origin(span qm.Span) qm.Origin { return qm.Origin{Span: span} }

func (b *irConverter) query(q *qt.Select) *qm.UnboundQuery {
	b.nextScope++
	scope := b.nextScope
	root := b.relation(q.From)
	if q.Where != nil {
		root = &qm.Filter{Input: root, Predicate: expression(q.Where, nil), Origin: origin(q.Where.Span)}
	}
	// 所有聚合输出先收集，再构造 HAVING/投影/排序；每个查询块独立编号。
	calls := []*qm.Expr{}
	columns := make([]qm.NamedExpr, 0, len(q.Projections))
	for _, projection := range q.Projections {
		columns = append(columns, qm.NamedExpr{Expr: expression(projection.Expr, &calls), Alias: projection.Alias, Origin: origin(projection.Span)})
	}
	having := expression(q.Having, &calls)
	keys := make([]qm.SortKey, 0, len(q.OrderBy))
	for _, item := range q.OrderBy {
		keys = append(keys, qm.SortKey{Expr: expression(item.Expr, &calls), Descending: item.Descending, Origin: origin(item.Span)})
	}
	if len(q.GroupBy) > 0 || len(calls) > 0 || q.Having != nil {
		group := make([]*qm.Expr, 0, len(q.GroupBy))
		for _, key := range q.GroupBy {
			group = append(group, expression(key, nil))
		}
		root = &qm.Aggregate{Input: root, GroupKeys: group, Calls: calls, Origin: origin(q.Span)}
	}
	if having != nil {
		root = &qm.Filter{Input: root, Predicate: having, Having: true, Origin: origin(q.Having.Span)}
	}
	// 尚未绑定的分组/排序名称保留到 Binder 分配槽，不在 IR 转换 猜测字段。
	root = &qm.Project{Input: root, Columns: columns, Origin: origin(q.Span)}
	if q.Distinct {
		root = &qm.Distinct{Input: root, Origin: origin(q.Span)}
	}
	if len(keys) > 0 {
		root = &qm.Sort{Input: root, Keys: keys, Origin: origin(q.Span)}
	}
	if q.Limit != nil {
		root = &qm.Limit{Input: root, Offset: q.Limit.Offset, Count: q.Limit.Count, Origin: origin(q.Limit.Span)}
	}
	return &qm.UnboundQuery{Root: root, Scope: scope}
}

func (b *irConverter) relation(r *qt.Relation) qm.Relation {
	switch r.Kind {
	case qt.Resource:
		b.nextInstance++
		return &qm.Scan{ResourceID: r.ResourceID, Alias: r.Alias, Instance: b.nextInstance, Origin: origin(r.Span)}
	case qt.Derived:
		query := b.query(r.Query)
		b.nextInstance++
		return &qm.Subquery{Input: query.Root, Scope: query.Scope, Alias: r.Alias, Instance: b.nextInstance, Origin: origin(r.Span)}
	case qt.Join:
		return &qm.Join{Left: b.relation(r.Left), Right: b.relation(r.Right), Kind: r.JoinKind, On: expression(r.On, nil), Origin: origin(r.Span)}
	default:
		// ValidateQueryTree 已排除未知 relation；新增种类必须同步策略与 IR 转换。
		panic("未检查的关系节点")
	}
}

func expression(e *qt.Expr, calls *[]*qm.Expr) *qm.Expr {
	if e == nil {
		return nil
	}
	result := &qm.Expr{Kind: qm.ExprKind(e.Kind), Origin: origin(e.Span), Name: append([]string(nil), e.Name...), Op: e.Op, Distinct: e.Distinct, Negated: e.Negated, CaseBase: e.CaseBase, CaseElse: e.CaseElse, CastType: e.CastType, CastParameters: append([]int(nil), e.CastParameters...)}
	if e.Escape != nil {
		escape := *e.Escape
		result.Escape = &escape
	}
	if e.Kind == qt.Literal {
		result.Literal = &qm.Literal{Kind: e.LiteralKind, Text: e.Text}
	}
	isAggregate := false
	if e.Kind == qt.Call {
		result.Name[0] = strings.ToUpper(result.Name[0])
		signature, _ := lookupFunction(result.Name[0])
		isAggregate = signature.Evaluation == qm.EvaluationAggregate
	}
	for _, arg := range e.Args {
		result.Args = append(result.Args, expression(arg, calls))
	}
	if isAggregate && calls != nil {
		index := -1
		for i, existing := range *calls {
			if equivalent(existing, result) {
				index = i
				break
			}
		}
		if index < 0 {
			index = len(*calls)
			*calls = append(*calls, result)
		}
		return &qm.Expr{Kind: qm.ExprAggregateRef, AggregateSlot: index, Origin: result.Origin}
	}
	return result
}

// equivalent 仅用于同一查询块内精确相同的聚合复用，位置不参与身份。
func equivalent(a, b *qm.Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Op != b.Op || a.Distinct != b.Distinct || a.Negated != b.Negated || a.CaseBase != b.CaseBase || a.CaseElse != b.CaseElse || a.CastType != b.CastType || a.AggregateSlot != b.AggregateSlot || !slices.Equal(a.Name, b.Name) || !slices.Equal(a.CastParameters, b.CastParameters) || len(a.Args) != len(b.Args) {
		return false
	}
	if (a.Literal == nil) != (b.Literal == nil) || (a.Escape == nil) != (b.Escape == nil) {
		return false
	}
	if a.Literal != nil && *a.Literal != *b.Literal {
		return false
	}
	if a.Escape != nil && *a.Escape != *b.Escape {
		return false
	}
	for i := range a.Args {
		if !equivalent(a.Args[i], b.Args[i]) {
			return false
		}
	}
	return true
}
