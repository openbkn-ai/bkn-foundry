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
)

// Query is one accepted read-only query.
type Query struct {
	Pattern  Pattern
	Where    []Comparison // conjunctive: every entry must hold
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
	Pos      Position
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

// Comparison is one predicate: a property against a literal.
type Comparison struct {
	Left     PropertyRef
	Operator string
	Right    Literal
	Pos      Position
}

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

// Projection is one RETURN item. Alias is what the column is called in the
// result; it defaults to the source text of the property reference.
type Projection struct {
	Property PropertyRef
	Alias    string
}

// SortKey is one ORDER BY item.
type SortKey struct {
	Property   PropertyRef
	Descending bool
}
