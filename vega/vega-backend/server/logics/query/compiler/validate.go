// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/functions"
	"regexp"
	"strings"
	"unicode/utf8"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	qt "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
)

var resourceID = regexp.MustCompile(interfaces.RegexPattern_NonBuiltin_ID)
var number = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// ValidateQueryTree 同时保护内部构造的树：拒绝环、未知节点及超预算结构。
// 检查成功不表示字段、类型、权限或目标能力已绑定。
func ValidateQueryTree(query *qt.Select) error {
	v := validator{active: make(map[any]bool)}
	return v.query(query, 1)
}

type validator struct {
	active map[any]bool
	nodes  int
}

func failure(kind qm.ErrorKind, span qm.Span, message string) error {
	return &qm.CompileError{Kind: kind, Origin: qm.Origin{Span: span}, Message: message}
}

func (v *validator) enter(node any, span qm.Span, depth int) error {
	if v.active[node] {
		return failure(qm.ErrorInvalidTree, span, "查询树包含环")
	}
	v.nodes++
	if depth > qm.MaxDepth || v.nodes > qm.MaxNodes {
		return failure(qm.ErrorLimit, span, "查询树复杂度超限")
	}
	v.active[node] = true
	return nil
}

func (v *validator) query(q *qt.Select, depth int) error {
	if q == nil {
		return failure(qm.ErrorInvalidTree, qm.Span{}, "缺少 SELECT")
	}
	if err := v.enter(q, q.Span, depth); err != nil {
		return err
	}
	defer delete(v.active, q)
	if len(q.Projections) == 0 {
		return failure(qm.ErrorInvalidTree, q.Span, "缺少投影")
	}
	if err := v.relation(q.From, depth+1); err != nil {
		return err
	}
	for _, p := range q.Projections {
		if err := v.expr(p.Expr, depth+1, true, false, true); err != nil {
			return err
		}
		if p.Expr.Kind == qt.Wildcard && p.Alias != "" {
			return failure(qm.ErrorUnsupported, p.Span, "通配符不接受别名")
		}
	}
	if q.Where != nil {
		if err := v.expr(q.Where, depth+1, false, false, false); err != nil {
			return err
		}
	}
	for _, key := range q.GroupBy {
		if err := v.expr(key, depth+1, false, false, false); err != nil {
			return err
		}
		if key.Kind == qt.Literal && key.LiteralKind == qm.LiteralInteger {
			return failure(qm.ErrorUnsupported, key.Span, "GROUP BY 不开放位置序号")
		}
	}
	if q.Having != nil {
		if err := v.expr(q.Having, depth+1, true, false, false); err != nil {
			return err
		}
	}
	for _, key := range q.OrderBy {
		if err := v.expr(key.Expr, depth+1, true, false, false); err != nil {
			return err
		}
	}
	if q.Limit != nil && (q.Limit.Offset < 0 || q.Limit.Count < 0) {
		return failure(qm.ErrorInvalidTree, q.Limit.Span, "LIMIT 不能为负")
	}
	return nil
}

func (v *validator) relation(r *qt.Relation, depth int) error {
	if r == nil {
		return failure(qm.ErrorInvalidTree, qm.Span{}, "缺少 FROM 来源")
	}
	if err := v.enter(r, r.Span, depth); err != nil {
		return err
	}
	defer delete(v.active, r)
	switch r.Kind {
	case qt.Resource:
		if !resourceID.MatchString(r.ResourceID) || r.Query != nil || r.Left != nil || r.Right != nil || r.On != nil {
			return failure(qm.ErrorInvalidTree, r.Span, "资源引用非法")
		}
	case qt.Derived:
		if r.Alias == "" || r.ResourceID != "" || r.Left != nil || r.Right != nil || r.On != nil {
			return failure(qm.ErrorInvalidTree, r.Span, "派生查询来源非法")
		}
		return v.query(r.Query, depth+1)
	case qt.Join:
		if r.ResourceID != "" || r.Query != nil || r.Alias != "" {
			return failure(qm.ErrorInvalidTree, r.Span, "JOIN 来源非法")
		}
		switch r.JoinKind {
		case qm.JoinInner, qm.JoinLeft, qm.JoinRight:
			if r.On == nil {
				return failure(qm.ErrorInvalidTree, r.Span, "JOIN 缺少 ON")
			}
		case qm.JoinCross:
			if r.On != nil {
				return failure(qm.ErrorInvalidTree, r.Span, "CROSS JOIN 不接受 ON")
			}
		default:
			return failure(qm.ErrorInvalidTree, r.Span, "未知 JOIN 类型")
		}
		if err := v.relation(r.Left, depth+1); err != nil {
			return err
		}
		if err := v.relation(r.Right, depth+1); err != nil {
			return err
		}
		if r.On != nil {
			return v.expr(r.On, depth+1, false, false, false)
		}
	default:
		return failure(qm.ErrorInvalidTree, r.Span, "未知来源节点")
	}
	return nil
}

func (v *validator) expr(e *qt.Expr, depth int, allowAggregate, insideAggregate, allowWildcard bool) error {
	if e == nil {
		return failure(qm.ErrorInvalidTree, qm.Span{}, "缺少表达式")
	}
	if err := v.enter(e, e.Span, depth); err != nil {
		return err
	}
	defer delete(v.active, e)
	arity := len(e.Args)
	childrenWildcard := false
	switch e.Kind {
	case qt.Name:
		if len(e.Name) < 1 || len(e.Name) > 2 || arity != 0 {
			return failure(qm.ErrorInvalidTree, e.Span, "名称结构非法")
		}
		for _, part := range e.Name {
			if part == "" {
				return failure(qm.ErrorInvalidTree, e.Span, "空名称")
			}
		}
	case qt.Wildcard:
		if !allowWildcard || len(e.Name) > 1 || arity != 0 {
			return failure(qm.ErrorUnsupported, e.Span, "当前位置不接受通配符")
		}
	case qt.Literal:
		if arity != 0 {
			return failure(qm.ErrorInvalidTree, e.Span, "常量结构非法")
		}
		switch e.LiteralKind {
		case qm.LiteralNull, qm.LiteralString:
		case qm.LiteralBoolean:
			if e.Text != "true" && e.Text != "false" {
				return failure(qm.ErrorInvalidTree, e.Span, "布尔常量非法")
			}
		case qm.LiteralInteger, qm.LiteralDecimal:
			if !number.MatchString(e.Text) || (e.LiteralKind == qm.LiteralInteger && strings.ContainsAny(e.Text, ".eE")) {
				return failure(qm.ErrorInvalidTree, e.Span, "数值常量非法")
			}
		default:
			return failure(qm.ErrorInvalidTree, e.Span, "未知常量类型")
		}
	case qt.Unary:
		if arity != 1 || (e.Op != "+" && e.Op != "-" && e.Op != "NOT") {
			return failure(qm.ErrorInvalidTree, e.Span, "一元表达式非法")
		}
	case qt.Binary:
		if arity != 2 {
			return failure(qm.ErrorInvalidTree, e.Span, "二元表达式非法")
		}
		switch e.Op {
		case "+", "-", "*", "/", "%", "=", "!=", "<>", "<", "<=", ">", ">=", "AND", "OR":
		default:
			return failure(qm.ErrorUnsupported, e.Span, "运算符未开放")
		}
	case qt.Call:
		if len(e.Name) != 1 {
			return failure(qm.ErrorInvalidTree, e.Span, "函数身份非法")
		}
		signature, found := functions.Lookup(e.Name[0])
		if !found {
			return failure(qm.ErrorUnsupported, e.Span, "函数未开放")
		}
		if arity < signature.MinArgs || (signature.MaxArgs > 0 && arity > signature.MaxArgs) || (signature.MaxArgs == 0 && signature.MinArgs == 0 && arity != 0) {
			return failure(qm.ErrorUnsupported, e.Span, "函数参数数量不支持")
		}
		if e.Distinct && !signature.AllowDistinct {
			return failure(qm.ErrorUnsupported, e.Span, "函数不支持 DISTINCT")
		}
		if signature.Evaluation == qm.EvaluationAggregate {
			if !allowAggregate || insideAggregate {
				return failure(qm.ErrorUnsupported, e.Span, "聚合位置或嵌套非法")
			}
			insideAggregate = true
		}
		childrenWildcard = signature.AllowStar && !e.Distinct
	case qt.Cast:
		if arity != 1 {
			return failure(qm.ErrorInvalidTree, e.Span, "CAST 结构非法")
		}
		switch e.CastType {
		case "SIGNED", "UNSIGNED", "INTEGER", "INT", "BIGINT", "DECIMAL", "FLOAT", "DOUBLE", "CHAR", "VARCHAR", "STRING", "BOOLEAN", "DATE", "DATETIME", "TIME", "TIMESTAMP":
		default:
			return failure(qm.ErrorUnsupported, e.Span, "CAST 类型未开放")
		}
		if len(e.CastParameters) > 2 {
			return failure(qm.ErrorInvalidTree, e.Span, "CAST 参数结构非法")
		}
		for _, param := range e.CastParameters {
			if param < 0 {
				return failure(qm.ErrorInvalidTree, e.Span, "CAST 参数非法")
			}
		}
	case qt.Case:
		branches := arity
		if e.CaseBase {
			branches--
		}
		if e.CaseElse {
			branches--
		}
		if branches < 2 || branches%2 != 0 {
			return failure(qm.ErrorInvalidTree, e.Span, "CASE 结构非法")
		}
	case qt.Between:
		if arity != 3 {
			return failure(qm.ErrorInvalidTree, e.Span, "BETWEEN 结构非法")
		}
	case qt.In:
		if arity < 2 {
			return failure(qm.ErrorInvalidTree, e.Span, "IN 集合不能为空")
		}
		if arity-1 > qm.MaxINValues {
			return failure(qm.ErrorLimit, e.Span, "IN 元素数超限")
		}
		for _, arg := range e.Args[1:] {
			if !constant(arg) {
				return failure(qm.ErrorUnsupported, e.Span, "IN 仅接受常量列表")
			}
		}
	case qt.IsNull:
		if arity != 1 {
			return failure(qm.ErrorInvalidTree, e.Span, "IS NULL 结构非法")
		}
	case qt.Like:
		if arity != 2 || e.Escape == nil || utf8.RuneCountInString(*e.Escape) != 1 {
			return failure(qm.ErrorInvalidTree, e.Span, "LIKE 结构非法")
		}
	default:
		return failure(qm.ErrorInvalidTree, e.Span, "未知表达式")
	}
	for _, arg := range e.Args {
		if err := v.expr(arg, depth+1, allowAggregate, insideAggregate, childrenWildcard); err != nil {
			return err
		}
	}
	return nil
}

func constant(e *qt.Expr) bool {
	if e == nil {
		return false
	}
	if e.Kind == qt.Literal {
		return true
	}
	return e.Kind == qt.Unary && (e.Op == "+" || e.Op == "-") && len(e.Args) == 1 && e.Args[0] != nil && e.Args[0].Kind == qt.Literal && (e.Args[0].LiteralKind == qm.LiteralInteger || e.Args[0].LiteralKind == qm.LiteralDecimal)
}
