// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import "fmt"

// The types here are the compiler's own reading of a query, not the parse
// tree. The parse tree covers all of openCypher; this covers only what the
// subset accepts, so every later stage can assume it is looking at something
// compilable and does not have to re-check for constructs that were already
// refused.

// Position locates a construct in the submitted query text so a rejection can
// point at it.
type Position struct {
	Line   int
	Column int
}

// Direction is which way an edge in the pattern points. Undirected patterns
// are not part of the subset, so there is no third value.
type Direction int

const (
	// Outgoing is (a)-[:R]->(b): a is the relation's source.
	Outgoing Direction = iota
	// Incoming is (a)<-[:R]-(b): b is the relation's source.
	Incoming
	// Undirected is (a)-[:R]-(b): either node may be the source, and which
	// readings are possible depends on what the relation type connects.
	Undirected
)

// Query is one accepted read-only query.
type Query struct {
	Pattern  Pattern
	Where    Predicate // nil when the query has no WHERE
	Return   []Projection
	Distinct bool
	OrderBy  []SortKey
	Skip     *int64
	Limit    *int64
}

// Pattern is a linear path. Edges[i] connects Nodes[i] to Nodes[i+1], so
// len(Edges) is always len(Nodes)-1.
type Pattern struct {
	Nodes []NodeRef
	Edges []EdgeRef
}

// NodeRef is one node of the pattern. Variable is empty for an anonymous node,
// which can be matched but not projected.
type NodeRef struct {
	Variable string
	Label    string
	// Anonymous marks a variable the compiler invented for a node the query
	// did not name, so a message about it can say "the node" rather than
	// quote a name the author never wrote.
	Anonymous bool
	Pos       Position
}

// EdgeRef is one relationship of the pattern.
type EdgeRef struct {
	Type      string
	Direction Direction
	Pos       Position
}

// PropertyRef is a variable.property reference.
type PropertyRef struct {
	Variable string
	Property string
	Pos      Position
}

func (p PropertyRef) String() string { return p.Variable + "." + p.Property }

// Predicate is one node of a WHERE expression. The shapes below are the whole
// set: anything else the grammar allows is refused while reading the query, so
// later stages never meet a predicate they cannot generate.
type Predicate interface {
	predicatePosition() Position
}

// Comparison is a property against a value.
type Comparison struct {
	Left     PropertyRef
	Operator string
	Right    Operand
	Pos      Position
}

func (c Comparison) predicatePosition() Position { return c.Pos }

// LogicalOperator is AND, OR or XOR over two or more predicates. It keeps the
// operands of one operator flat rather than nesting them pairwise, which is
// how the source reads and how the generated SQL is written.
type LogicalOperator struct {
	Operator string // AND, OR, XOR
	Operands []Predicate
	Pos      Position
}

func (l LogicalOperator) predicatePosition() Position { return l.Pos }

// Negation is NOT applied to one predicate.
type Negation struct {
	Operand Predicate
	Pos     Position
}

func (n Negation) predicatePosition() Position { return n.Pos }

// NullCheck is IS NULL, or IS NOT NULL when negated.
type NullCheck struct {
	Property PropertyRef
	Negated  bool
	Pos      Position
}

func (n NullCheck) predicatePosition() Position { return n.Pos }

// Membership is IN over a list written in the query. An empty list matches
// nothing, which is what Cypher says and what the generator has to write
// explicitly because SQL has no empty IN.
type Membership struct {
	Property PropertyRef
	Values   []Operand
	Negated  bool
	Pos      Position
}

func (m Membership) predicatePosition() Position { return m.Pos }

// LiteralKind tags which field of Literal carries the value.
type LiteralKind int

const (
	LiteralString LiteralKind = iota
	LiteralInteger
	LiteralFloat
	LiteralBoolean
	LiteralNull
)

// Literal is a constant written in the query. It keeps the decoded Go value,
// not the source text: escaping belongs to the target dialect and is applied
// once, at SQL generation.
type Literal struct {
	Kind    LiteralKind
	String  string
	Integer int64
	Float   float64
	Boolean bool
	Pos     Position
}

func (l Literal) describe() string {
	switch l.Kind {
	case LiteralString:
		return fmt.Sprintf("string %q", l.String)
	case LiteralInteger:
		return fmt.Sprintf("integer %d", l.Integer)
	case LiteralFloat:
		return fmt.Sprintf("float %v", l.Float)
	case LiteralBoolean:
		return fmt.Sprintf("boolean %v", l.Boolean)
	default:
		return "null"
	}
}

// Operand is a value written in the query: a literal, or a parameter that the
// request supplies. Both are values and never identifiers -- a parameter can
// change which rows come back, never which table or column is read.
type Operand struct {
	Literal   *Literal
	Parameter *ParameterRef
}

// ParameterRef is $name, resolved against the request's parameters before the
// statement is generated.
type ParameterRef struct {
	Name string
	Pos  Position
}

func (o Operand) describe() string {
	if o.Parameter != nil {
		return "parameter $" + o.Parameter.Name
	}
	if o.Literal != nil {
		return o.Literal.describe()
	}
	return "an empty operand"
}

// Projection is one RETURN item: a property, or an aggregate over one. Alias
// is what the column is called in the result; it defaults to the source text
// of what was projected.
type Projection struct {
	Property  *PropertyRef
	Aggregate *Aggregate
	Alias     string
}

// Aggregate is count, sum, avg, min or max. Property is nil for count(*),
// which counts rows rather than values.
type Aggregate struct {
	// Function is the SQL spelling, taken from a fixed set. Name is what the
	// author wrote, which is what an unaliased column is called: a result read
	// by key should carry the name the query used.
	Function string
	Name     string
	Distinct bool
	Property *PropertyRef
	Pos      Position
}

// String renders the aggregate the way it was written, which is what an
// unaliased column is named after.
func (a Aggregate) String() string {
	inner := "*"
	if a.Property != nil {
		inner = a.Property.String()
	}
	if a.Distinct {
		inner = "DISTINCT " + inner
	}
	return a.Name + "(" + inner + ")"
}

// SortKey is one ORDER BY item. It is a property, an aggregate written out
// again, or the name of something the query returns.
type SortKey struct {
	Property   *PropertyRef
	Aggregate  *Aggregate
	Alias      string
	Descending bool
	Pos        Position
}
