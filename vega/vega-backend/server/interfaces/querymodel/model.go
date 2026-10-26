// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package querymodel 定义与 HTTP、ANTLR 和物理 Connector 无关的查询模型。
package querymodel

import "strconv"

const (
	OriginSQL       OriginKind = "sql"
	OriginDTO       OriginKind = "dto"
	OriginSavedView OriginKind = "saved_view"
)

const (
	LiteralNull    LiteralKind = "null"
	LiteralBoolean LiteralKind = "boolean"
	LiteralInteger LiteralKind = "integer"
	LiteralDecimal LiteralKind = "decimal"
	LiteralString  LiteralKind = "string"
)

const (
	ExprName            ExprKind = "name"
	ExprField           ExprKind = "field"
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
)

const (
	JoinInner JoinKind = "inner"
	JoinLeft  JoinKind = "left"
	JoinRight JoinKind = "right"
	JoinCross JoinKind = "cross"
)

const (
	EvaluationPure      EvaluationKind = "pure"
	EvaluationStable    EvaluationKind = "stable"
	EvaluationAggregate EvaluationKind = "aggregate"
	EvaluationVolatile  EvaluationKind = "volatile"
)

const (
	OperatorAdd          ScalarOperator = "+"
	OperatorSubtract     ScalarOperator = "-"
	OperatorMultiply     ScalarOperator = "*"
	OperatorDivide       ScalarOperator = "/"
	OperatorModulo       ScalarOperator = "%"
	OperatorEqual        ScalarOperator = "="
	OperatorNotEqual     ScalarOperator = "<>"
	OperatorLess         ScalarOperator = "<"
	OperatorLessEqual    ScalarOperator = "<="
	OperatorGreater      ScalarOperator = ">"
	OperatorGreaterEqual ScalarOperator = ">="
	OperatorAnd          ScalarOperator = "AND"
	OperatorOr           ScalarOperator = "OR"
	OperatorNot          ScalarOperator = "NOT"
)

const (
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

const (
	NullsFirst NullOrder = "first"
	NullsLast  NullOrder = "last"
)

// Span 是原始 SQL 中的 UTF-8 字节半开区间。
type Span struct{ Start, End int }

type OriginKind string

type Origin struct {
	Kind       OriginKind
	Span       Span
	Path       string
	ResourceID string
}

type RelationInstanceID uint64

type LiteralKind string

// Literal 的数值以原始精确十进制文本保存，绑定阶段再决定目标数值类型。
type Literal struct {
	Kind LiteralKind
	Text string
}

// ExprKind 是绑定前后共享的表达式节点种类。
// Name/Wildcard 用于构造阶段；Prepare 将名称解析为 Field，函数保持为 Call。
type ExprKind string

// Sqlizer 是结构化表达式。构造阶段保存名称/常量，Prepare 在同类型的新节点中补齐字段、类型和值。
// Connector 只消费已解析的 Field/Value/Function，不接受原始 SQL 片段。
// CASE 的 Args 按可选 base、成对 when/then、可选 else 排列。
type Sqlizer struct {
	Type           LogicalType
	Nullable       bool
	Field          FieldID
	Value          *Value
	Function       FunctionID
	Domain         SemanticDomain
	Requirements   []SemanticRequirement
	Kind           ExprKind
	Origin         Origin
	Name           []string
	Literal        *Literal
	Op             ScalarOperator
	Args           []*Sqlizer
	Distinct       bool
	Negated        bool
	CaseBase       bool
	CaseElse       bool
	CastType       string
	CastParameters []int
	Escape         *string
}

type JoinKind string

// Relation 的集合在本包内封闭；各来源实例及查询块具有独立身份。
type Relation interface{ queryRelation() }

type Scan struct {
	Identifier   PhysicalIdentifier
	Resource     ResourceReference
	Fields       []SourceField
	OutputSchema Schema
	ResourceID   string
	Alias        string
	Instance     RelationInstanceID
	Origin       Origin
}

type Join struct {
	OutputSchema Schema
	Left, Right  Relation
	Kind         JoinKind
	On           *Sqlizer
	Origin       Origin
}

// Subquery 是显式的 SELECT 边界，不把外层条件写入内部查询。
type Subquery struct {
	Query        *SelectBuilder
	Alias        string
	Instance     RelationInstanceID
	Origin       Origin
	Resource     ResourceReference
	Columns      []ColumnMapping
	OutputSchema Schema
}

type EvaluationKind string

type FunctionID string

type ScalarOperator string

type SortDirection string

type NullOrder string

// ResourceReference 只描述逻辑资源身份，不含物理表名或凭据。
type ResourceReference struct {
	ID        string
	CatalogID string
	Category  string
	Version   string
}

// PhysicalIdentifier 是分段物理名称，不接受 SQL 表达式。
type PhysicalIdentifier []string

type SourceField struct {
	Identifier  PhysicalIdentifier
	ID          FieldID
	LogicalName string
	NativeType  NativeType
}

func Field(parts ...string) *Sqlizer {
	return &Sqlizer{Kind: ExprName, Name: append([]string(nil), parts...)}
}

func String(value string) *Sqlizer {
	return &Sqlizer{Kind: ExprLiteral, Literal: &Literal{Kind: LiteralString, Text: value}}
}

func Int64(value int64) *Sqlizer {
	return &Sqlizer{Kind: ExprLiteral, Literal: &Literal{Kind: LiteralInteger, Text: strconv.FormatInt(value, 10)}}
}

func Bool(value bool) *Sqlizer {
	return &Sqlizer{Kind: ExprLiteral, Literal: &Literal{Kind: LiteralBoolean, Text: strconv.FormatBool(value)}}
}

func Null() *Sqlizer { return &Sqlizer{Kind: ExprLiteral, Literal: &Literal{Kind: LiteralNull}} }

func Compare(op string, left, right *Sqlizer) *Sqlizer {
	return &Sqlizer{Kind: ExprBinary, Op: ScalarOperator(op), Args: []*Sqlizer{left, right}}
}

func Eq(left, right *Sqlizer) *Sqlizer { return Compare("=", left, right) }

func And(conditions ...*Sqlizer) *Sqlizer {
	result := Bool(true)
	for i, condition := range conditions {
		if i == 0 {
			result = condition
		} else {
			result = Compare("AND", result, condition)
		}
	}
	return result
}

func Or(conditions ...*Sqlizer) *Sqlizer {
	result := Bool(false)
	for i, condition := range conditions {
		if i == 0 {
			result = condition
		} else {
			result = Compare("OR", result, condition)
		}
	}
	return result
}

func (expr *Sqlizer) WithOrigin(origin Origin) *Sqlizer {
	copy := *expr
	copy.Origin = origin
	return &copy
}

func Resource(id, alias string) *Scan { return &Scan{ResourceID: id, Alias: alias} }

// Call 和 Star 只构造结构化表达式，函数白名单及通配符位置由 Prepare 检查。
func Call(name string, args ...*Sqlizer) *Sqlizer {
	return &Sqlizer{Kind: ExprCall, Name: []string{name}, Args: append([]*Sqlizer(nil), args...)}
}

func Star(qualifier ...string) *Sqlizer {
	return &Sqlizer{Kind: ExprWildcard, Name: append([]string(nil), qualifier...)}
}

func (*Scan) queryRelation() {}

func (*Join) queryRelation() {}

func (*Subquery) queryRelation() {}

// FieldIDs 按表达式顺序列出引用字段。DAG 共用节点只访问一次，非法环不会导致无限递归；
// 表达式结构和预算仍由绑定及 Connector Prepare 校验。
func (e *Sqlizer) FieldIDs() []FieldID {
	var result []FieldID
	seenNodes := map[*Sqlizer]bool{}
	seenFields := map[FieldID]bool{}
	pending := []*Sqlizer{e}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if node == nil || seenNodes[node] {
			continue
		}
		seenNodes[node] = true
		if node.Kind == ExprField && !seenFields[node.Field] {
			result = append(result, node.Field)
			seenFields[node.Field] = true
		}
		for i := len(node.Args) - 1; i >= 0; i-- {
			pending = append(pending, node.Args[i])
		}
	}
	return result
}
