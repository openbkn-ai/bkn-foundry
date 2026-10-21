// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

type ScalarOperator string

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

// BoundExpr 只允许绑定后的节点，不得含 name、wildcard 或 aggregate_ref。
// 字段和运行时参数均引用稳定 ID。
// Kind 决定有效 payload；绑定/结构验证负责检查组合、类型及输入可见性。
// CAST 的目标类型为 Type；CASE 的 Args 排列与未绑定表达式一致。
type BoundExpr struct {
	Kind         ExprKind
	Type         LogicalType
	Nullable     bool
	Origin       Origin
	Field        FieldID
	Literal      *Value
	Operator     ScalarOperator
	Function     FunctionID
	Args         []*BoundExpr
	RuntimeSlot  RuntimeSlotID
	Distinct     bool
	Negated      bool
	CaseBase     bool
	CaseElse     bool
	Escape       *string
	Domain       SemanticDomain
	Requirements []SemanticRequirement
}

type BoundNamedExpr struct {
	Output FieldID
	Expr   *BoundExpr
	Origin Origin
}

type SortDirection string

const (
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

type NullOrder string

const (
	NullsFirst NullOrder = "first"
	NullsLast  NullOrder = "last"
)

type BoundSortKey struct {
	Expr      *BoundExpr
	Direction SortDirection
	Nulls     NullOrder
	Origin    Origin
}

// BoundRelation 与未绑定 Relation 为不同接口，不能互相赋值。
type BoundRelation interface{ boundRelation() }

type BoundRelationBase struct {
	OutputSchema Schema
	Origin       Origin
}

// ResourceReference 只描述逻辑资源身份，不含物理表名或凭据。
type ResourceReference struct {
	ID        string
	CatalogID string
	Category  string
	Version   string
}

type BoundSourceField struct {
	ID          FieldID
	LogicalName string
	NativeType  NativeType
}

type BoundScan struct {
	BoundRelationBase
	Resource ResourceReference
	Instance RelationInstanceID
	Fields   []BoundSourceField
}

type BoundJoin struct {
	BoundRelationBase
	Left, Right BoundRelation
	Kind        JoinKind
	On          *BoundExpr
}

type BoundFilter struct {
	BoundRelationBase
	Input     BoundRelation
	Predicate *BoundExpr
	Having    bool
}

type BoundAggregate struct {
	BoundRelationBase
	Input     BoundRelation
	GroupKeys []BoundNamedExpr
	Calls     []BoundNamedExpr
}

type BoundProject struct {
	BoundRelationBase
	Input   BoundRelation
	Columns []BoundNamedExpr
}

type BoundDistinct struct {
	BoundRelationBase
	Input BoundRelation
	Keys  []FieldID
}

type BoundSort struct {
	BoundRelationBase
	Input BoundRelation
	Keys  []BoundSortKey
}

type BoundLimit struct {
	BoundRelationBase
	Input         BoundRelation
	Offset, Count int64
}

type BoundSubquery struct {
	BoundRelationBase
	Input    BoundRelation
	Scope    ScopeID
	Instance RelationInstanceID
	Alias    string
	Columns  []ColumnMapping
}

// ViewBoundary 阻止外层跨过视图输出作用域，固定条件保留在 Input 子树内。
type BoundViewBoundary struct {
	BoundRelationBase
	Input         BoundRelation
	Resource      ResourceReference
	Scope         ScopeID
	Instance      RelationInstanceID
	Columns       []ColumnMapping
	Authorization ViewAuthorization
}

type BoundSearch struct {
	BoundRelationBase
	Input     BoundRelation
	Fields    []FieldID
	Query     *BoundExpr
	Prefilter *BoundExpr
	Options   SearchOptions
	Score     FieldID
}

func (*BoundScan) boundRelation()         {}
func (*BoundJoin) boundRelation()         {}
func (*BoundFilter) boundRelation()       {}
func (*BoundAggregate) boundRelation()    {}
func (*BoundProject) boundRelation()      {}
func (*BoundDistinct) boundRelation()     {}
func (*BoundSort) boundRelation()         {}
func (*BoundLimit) boundRelation()        {}
func (*BoundSubquery) boundRelation()     {}
func (*BoundViewBoundary) boundRelation() {}
func (*BoundSearch) boundRelation()       {}

// BoundQuery 是绑定阶段制品；不能以反序列化或设置某个 bool 获得绑定身份。
// 对外不可作为可信请求接收，执行前仍须检查权限、依赖及目标支持。
type BoundQuery struct {
	Root         BoundRelation
	Scope        ScopeID
	OutputSchema Schema
	Dependencies []Dependency
	Semantics    SemanticContext
	RuntimeSlots []RuntimeSlot
}
