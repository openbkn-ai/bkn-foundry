// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

// A graph value is what RETURN gives when it returns more than numbers and
// strings read from columns: a whole node, a relationship, a constant, a map
// of values, or collect over one of those. The SQL path cannot produce any of
// them, so a query that returns one always runs on the bind-join executor,
// which holds every matched row in memory and assembles the value there.

// GraphExpression is one graph value as the query wrote it.
type GraphExpression interface {
	graphPosition() Position
}

// GraphVariable is a node or relationship named by its variable. Which of the
// two it is, and what it is bound to, is settled against the model.
type GraphVariable struct {
	Name string
	Pos  Position
}

func (v GraphVariable) graphPosition() Position { return v.Pos }

// GraphProperty is a property read inside a graph value, as in
// {name: m.material_name}.
type GraphProperty struct {
	Property PropertyRef
}

func (p GraphProperty) graphPosition() Position { return p.Property.Pos }

// GraphConstant is a string, number or boolean written in the query, or a
// parameter.
type GraphConstant struct {
	Literal   *Literal
	Parameter *ParameterRef
	Pos       Position
}

func (c GraphConstant) graphPosition() Position { return c.Pos }

// GraphMap is a map literal. Keys are kept in the order written, which is
// the order the result lists them in.
type GraphMap struct {
	Keys   []string
	Values []GraphExpression
	Pos    Position
}

func (m GraphMap) graphPosition() Position { return m.Pos }

// GraphCollect is collect over a graph value: the list of that value across
// the rows of a group.
type GraphCollect struct {
	Argument GraphExpression
	Pos      Position
}

func (c GraphCollect) graphPosition() Position { return c.Pos }

// graphContainsCollect reports whether a graph value aggregates.
//
//nolint:unused // Intentionally retained.
func graphContainsCollect(value GraphExpression) bool {
	switch node := value.(type) {
	case GraphCollect:
		return true
	case GraphMap:
		for _, field := range node.Values {
			if graphContainsCollect(field) {
				return true
			}
		}
	}
	return false
}
