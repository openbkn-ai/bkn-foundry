// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package querytree 保存 SQL 的语法结构，不含 ANTLR、源端绑定或执行状态。
package querytree

import "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"

type Select struct {
	Span        querymodel.Span
	Distinct    bool
	Projections []Projection
	From        *Relation
	Where       *Expr
	GroupBy     []*Expr
	Having      *Expr
	OrderBy     []SortItem
	Limit       *Limit
}

type Projection struct {
	Expr  *Expr
	Alias string
	Span  querymodel.Span
}
type SortItem struct {
	Expr       *Expr
	Descending bool
	Span       querymodel.Span
}
type Limit struct {
	Offset, Count int64
	Span          querymodel.Span
}

type RelationKind string

const (
	Resource RelationKind = "resource"
	Join     RelationKind = "join"
	Derived  RelationKind = "derived"
)

type Relation struct {
	Kind        RelationKind
	Span        querymodel.Span
	ResourceID  string
	Alias       string
	Query       *Select
	Left, Right *Relation
	JoinKind    querymodel.JoinKind
	On          *Expr
}

type ExprKind string

const (
	Name     ExprKind = "name"
	Wildcard ExprKind = "wildcard"
	Literal  ExprKind = "literal"
	Unary    ExprKind = "unary"
	Binary   ExprKind = "binary"
	Call     ExprKind = "call"
	Case     ExprKind = "case"
	Cast     ExprKind = "cast"
	Between  ExprKind = "between"
	In       ExprKind = "in"
	IsNull   ExprKind = "is_null"
	Like     ExprKind = "like"
)

// Expr 只持有值和位置，不引用解析树或 token stream。
type Expr struct {
	Kind           ExprKind
	Span           querymodel.Span
	Name           []string
	LiteralKind    querymodel.LiteralKind
	Text           string
	Op             string
	Args           []*Expr
	Distinct       bool
	Negated        bool
	CaseBase       bool
	CaseElse       bool
	CastType       string
	CastParameters []int
	Escape         *string
}
