// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package querymodel 定义与 HTTP、ANTLR 和物理 Connector 无关的查询模型。
package querymodel

// Span 是原始 SQL 中的 UTF-8 字节半开区间。
type Span struct{ Start, End int }

type OriginKind string

const (
	OriginSQL       OriginKind = "sql"
	OriginDTO       OriginKind = "dto"
	OriginSavedView OriginKind = "saved_view"
)

type Origin struct {
	Kind       OriginKind
	Span       Span
	Path       string
	ResourceID string
}
type ScopeID uint64
type RelationInstanceID uint64

type LiteralKind string

const (
	LiteralNull    LiteralKind = "null"
	LiteralBoolean LiteralKind = "boolean"
	LiteralInteger LiteralKind = "integer"
	LiteralDecimal LiteralKind = "decimal"
	LiteralString  LiteralKind = "string"
)

// Literal 的数值以原始精确十进制文本保存，绑定阶段再决定目标数值类型。
type Literal struct {
	Kind LiteralKind
	Text string
}

// ExprKind 是绑定前后共享的表达式节点种类。
// Name/Wildcard/AggregateRef 仅用于绑定前；Field/RuntimeSlot 仅用于绑定后。
type ExprKind string

const (
	ExprName            ExprKind = "name"
	ExprField           ExprKind = "field"
	ExprRuntimeSlot     ExprKind = "runtime_slot"
	ExprWildcard        ExprKind = "wildcard"
	ExprLiteral         ExprKind = "literal"
	ExprUnary           ExprKind = "unary"
	ExprBinary          ExprKind = "binary"
	ExprCall            ExprKind = "call"
	ExprCase            ExprKind = "case"
	ExprCast            ExprKind = "cast"
	ExprBetween         ExprKind = "between"
	ExprIn              ExprKind = "in"
	ExprIsNull          ExprKind = "is_null"
	ExprLike            ExprKind = "like"
	ExprLiteralContains ExprKind = "literal_contains"
	ExprAggregateRef    ExprKind = "aggregate_ref"
)

// Expr 是未绑定表达式；Name/Call 不能直接交给 Connector 执行。
// CASE 的 Args 按可选 base、成对 when/then、可选 else 排列。
type Expr struct {
	Kind           ExprKind
	Origin         Origin
	Name           []string
	Literal        *Literal
	Op             string
	Args           []*Expr
	Distinct       bool
	Negated        bool
	CaseBase       bool
	CaseElse       bool
	CastType       string
	CastParameters []int
	Escape         *string
	AggregateSlot  int
}

type NamedExpr struct {
	Expr   *Expr
	Alias  string
	Origin Origin
}
type SortKey struct {
	Expr       *Expr
	Descending bool
	Origin     Origin
}

type JoinKind string

const (
	JoinInner JoinKind = "inner"
	JoinLeft  JoinKind = "left"
	JoinRight JoinKind = "right"
	JoinCross JoinKind = "cross"
)

// Relation 的集合在本包内封闭；各来源实例及查询块具有独立身份。
type Relation interface{ queryRelation() }
type Scan struct {
	ResourceID string
	Alias      string
	Instance   RelationInstanceID
	Origin     Origin
}
type Join struct {
	Left, Right Relation
	Kind        JoinKind
	On          *Expr
	Origin      Origin
}
type Filter struct {
	Input     Relation
	Predicate *Expr
	Having    bool
	Origin    Origin
}
type Aggregate struct {
	Input     Relation
	GroupKeys []*Expr
	Calls     []*Expr
	Origin    Origin
}
type Project struct {
	Input   Relation
	Columns []NamedExpr
	Origin  Origin
}
type Distinct struct {
	Input  Relation
	Origin Origin
}
type Sort struct {
	Input  Relation
	Keys   []SortKey
	Origin Origin
}
type Limit struct {
	Input         Relation
	Offset, Count int64
	Origin        Origin
}
type Subquery struct {
	Input    Relation
	Scope    ScopeID
	Alias    string
	Instance RelationInstanceID
	Origin   Origin
}

func (*Scan) queryRelation()      {}
func (*Join) queryRelation()      {}
func (*Filter) queryRelation()    {}
func (*Aggregate) queryRelation() {}
func (*Project) queryRelation()   {}
func (*Distinct) queryRelation()  {}
func (*Sort) queryRelation()      {}
func (*Limit) queryRelation()     {}
func (*Subquery) queryRelation()  {}

type UnboundQuery struct {
	Root  Relation
	Scope ScopeID
}

// ViewBoundary 保留逻辑视图的输出映射和固定条件来源，不能展开为外层 Filter。
type ViewBoundary struct {
	Input         Relation
	Resource      ResourceReference
	Scope         ScopeID
	Instance      RelationInstanceID
	Columns       []NamedExpr
	Authorization ViewAuthorization
	Origin        Origin
}

func (*ViewBoundary) queryRelation() {}
