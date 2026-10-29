// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package sqlbuilder 保存方言无关的结构化查询节点，提供可变链式构造。
// SQL 生成属于目标方言包；资源解析、授权及类型检查属于准备阶段。
package sqlbuilder

const (
	InnerJoin JoinKind = "inner"
	LeftJoin  JoinKind = "left"
	RightJoin JoinKind = "right"
	CrossJoin JoinKind = "cross"
)

const (
	NullsDefault NullOrder = ""
	NullsFirst   NullOrder = "first"
	NullsLast    NullOrder = "last"
)

// SqlNode 表示结构化 SQL 节点，统一以指针传递，不包含 SQL 生成或执行方法。
type SqlNode interface {
	sqlNode()
}

// SelectBuilder 只保存查询子句；各 Part 组合结构化节点，不接受原始 SQL 片段。
// WherePart、HavingPart 中的多个条件按 AND 组合。
// LimitPart=nil 表示未设置 LIMIT；非 nil 的零值表示 LIMIT 0。
// 方法调用顺序不改变子句含义，先 LIMIT 再过滤必须用显式子查询表达。
// 链式方法原地修改并返回同一个 Builder，无需接收返回值。
// 每个查询独立构造，子查询直接引用其 Builder，不自动复制或派生。
type SelectBuilder struct {
	ColumnsPart  []SqlNode
	FromPart     SqlNode
	JoinsPart    []Join
	WherePart    []SqlNode
	GroupByPart  []SqlNode
	HavingPart   []SqlNode
	OrderByPart  []Order
	DistinctPart bool
	LimitPart    *uint64
	OffsetPart   uint64
}

// Table 保存资源引用及表别名。SourceIdentifier 直接使用已授权资源提供的
// SQL-ready 表引用，由连接器在发现资源时生成；SQL 输入仅提供 ResourceID。
// 目录和授权信息由准备服务管理，不重复拆分或转义 SourceIdentifier。
type Table struct {
	ResourceID       string
	SourceIdentifier string
	Alias            string
	// ColumnNames 保存资源 schema 中的源字段名，仅用于区分源字段与投影别名。
	// nil 表示字段信息未知，生成器不得据此假定源字段不存在。
	ColumnNames []string
}

// Subquery 显式保留内部 SELECT 的过滤、排序和 LIMIT 边界。
type Subquery struct {
	Query *SelectBuilder
	Alias string
}

type JoinKind string

// Join 是连接子句；Source 为 Table、Subquery 或 JoinedTable。
// CROSS JOIN 不设置 On；其他连接的 On 是结构化条件。
type Join struct {
	Kind   JoinKind
	Source SqlNode
	On     SqlNode
}

// JoinedTable 表达带括号的连接来源，例如 a LEFT JOIN (b JOIN c ON ...) ON ...。
// From 和 Joins 共同组成一个来源，不借助额外 SELECT 改写连接语义。
type JoinedTable struct {
	From  SqlNode
	Joins []Join
}

type NullOrder string

// Order 只保存排序表达式与规则，不依赖数据库缺省的 NULL 排序。
// NullsDefault 按 Vega 规则取 ASC 在前、DESC 在后，由目标生成器明确兑现。
type Order struct {
	// Position 表示 SQL ORDER BY 的正整数投影位置，与 Expr 二选一。
	Position   uint64
	Expr       SqlNode
	Descending bool
	Nulls      NullOrder
}

func Select(columns ...SqlNode) *SelectBuilder {
	return (&SelectBuilder{}).Columns(columns...)
}

func (q *SelectBuilder) Columns(columns ...SqlNode) *SelectBuilder {
	q.ColumnsPart = append(q.ColumnsPart, columns...)
	return q
}

func (q *SelectBuilder) From(source SqlNode) *SelectBuilder {
	q.FromPart = source
	return q
}

func (q *SelectBuilder) FromSelect(source *SelectBuilder, alias string) *SelectBuilder {
	return q.From(&Subquery{Query: source, Alias: alias})
}

func (q *SelectBuilder) JoinClause(join Join) *SelectBuilder {
	q.JoinsPart = append(q.JoinsPart, join)
	return q
}

func (q *SelectBuilder) Join(source, on SqlNode) *SelectBuilder {
	return q.JoinClause(Join{Kind: InnerJoin, Source: source, On: on})
}

func (q *SelectBuilder) LeftJoin(source, on SqlNode) *SelectBuilder {
	return q.JoinClause(Join{Kind: LeftJoin, Source: source, On: on})
}

func (q *SelectBuilder) RightJoin(source, on SqlNode) *SelectBuilder {
	return q.JoinClause(Join{Kind: RightJoin, Source: source, On: on})
}

func (q *SelectBuilder) CrossJoin(source SqlNode) *SelectBuilder {
	return q.JoinClause(Join{Kind: CrossJoin, Source: source})
}

func (q *SelectBuilder) Where(conditions ...SqlNode) *SelectBuilder {
	for _, condition := range conditions {
		if condition != nil {
			q.WherePart = append(q.WherePart, condition)
		}
	}
	return q
}

func (q *SelectBuilder) GroupBy(expressions ...SqlNode) *SelectBuilder {
	q.GroupByPart = append(q.GroupByPart, expressions...)
	return q
}

func (q *SelectBuilder) Having(conditions ...SqlNode) *SelectBuilder {
	for _, condition := range conditions {
		if condition != nil {
			q.HavingPart = append(q.HavingPart, condition)
		}
	}
	return q
}

func (q *SelectBuilder) OrderBy(orders ...Order) *SelectBuilder {
	q.OrderByPart = append(q.OrderByPart, orders...)
	return q
}

func (q *SelectBuilder) Distinct() *SelectBuilder {
	q.DistinctPart = true
	return q
}

func (q *SelectBuilder) Limit(count uint64) *SelectBuilder {
	q.LimitPart = &count
	return q
}

func (q *SelectBuilder) Offset(offset uint64) *SelectBuilder {
	q.OffsetPart = offset
	return q
}

func (q *SelectBuilder) RemoveLimit() *SelectBuilder {
	q.LimitPart = nil
	return q
}

func (q *SelectBuilder) RemoveOffset() *SelectBuilder {
	q.OffsetPart = 0
	return q
}

func (*SelectBuilder) sqlNode() {}

func (*Table) sqlNode() {}

func (*Subquery) sqlNode() {}

func (*Join) sqlNode() {}

func (*JoinedTable) sqlNode() {}

func (*Order) sqlNode() {}
