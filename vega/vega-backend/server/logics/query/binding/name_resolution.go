// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"reflect"
	"slices"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

type scopeColumn struct {
	column    qm.OutputColumn
	qualifier string
	domain    qm.SemanticDomain
}

type groupBinding struct {
	source *qm.Expr
	column scopeColumn
}

type bindingScope struct {
	columns    []scopeColumn
	aliases    map[string]bool
	groups     []groupBinding
	aggregates []*qm.BoundExpr
	fallback   *bindingScope
	groupInput *bindingScope
}

func lookupColumn(scope *bindingScope, name []string, origin qm.Origin) (scopeColumn, error) {
	if len(name) < 1 || len(name) > 2 {
		return scopeColumn{}, bindFailure(origin, "字段名称结构非法")
	}
	if scope.groupInput != nil {
		if _, err := lookupColumn(scope.groupInput, name, origin); err != nil {
			return scopeColumn{}, err
		}
	}
	var matched *scopeColumn
	for i := range scope.columns {
		c := &scope.columns[i]
		if c.column.Name != name[len(name)-1] || (len(name) == 2 && c.qualifier != name[0]) {
			continue
		}
		if matched != nil {
			return scopeColumn{}, bindFailure(origin, "字段引用存在歧义")
		}
		matched = c
	}
	if matched != nil {
		return *matched, nil
	}
	if scope.fallback != nil {
		return lookupColumn(scope.fallback, name, origin)
	}
	return scopeColumn{}, bindFailure(origin, "字段不在当前作用域")
}
func columnExpr(c scopeColumn, origin qm.Origin) *qm.BoundExpr {
	return &qm.BoundExpr{Kind: qm.ExprField, Type: c.column.Type, Nullable: c.column.Nullable, Field: c.column.ID, Domain: c.domain, Origin: origin}
}

// 分组匹配使用来源字段身份，a.id 与未限定的 id 在无歧义时具有相同含义。
func sameGroupingExpr(a, b *qm.Expr, scope *bindingScope) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind == qm.ExprName && b.Kind == qm.ExprName {
		left, err := lookupColumn(scope, a.Name, a.Origin)
		if err != nil {
			return false
		}
		right, err := lookupColumn(scope, b.Name, b.Origin)
		return err == nil && left.column.ID == right.column.ID
	}
	if a.Kind != b.Kind || a.Op != b.Op || a.Distinct != b.Distinct || a.Negated != b.Negated || a.CaseBase != b.CaseBase || a.CaseElse != b.CaseElse || a.CastType != b.CastType || a.AggregateSlot != b.AggregateSlot || !slices.Equal(a.Name, b.Name) || !slices.Equal(a.CastParameters, b.CastParameters) || !reflect.DeepEqual(a.Literal, b.Literal) || !reflect.DeepEqual(a.Escape, b.Escape) || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if !sameGroupingExpr(a.Args[i], b.Args[i], scope) {
			return false
		}
	}
	return true
}

// expressionEncoding 保留字段原有的展示类型；运算类型不能反向改写公共字段契约。
func expressionEncoding(expr *qm.BoundExpr, scope *bindingScope) qm.OutputEncoding {
	if expr.Kind == qm.ExprField {
		for current := scope; current != nil; current = current.fallback {
			for _, column := range current.columns {
				if column.column.ID == expr.Field {
					return column.column.Encoding
				}
			}
		}
	}
	if len(expr.Args) == 1 && ((expr.Kind == qm.ExprCast && len(expr.Requirements) == 0 && expr.Type.Kind == expr.Args[0].Type.Kind) || (expr.Kind == qm.ExprCall && (expr.Function == "MIN" || expr.Function == "MAX"))) {
		return expressionEncoding(expr.Args[0], scope)
	}
	return outputEncoding(expr.Type)
}
