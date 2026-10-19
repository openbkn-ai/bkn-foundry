// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

// Graph values are what a Cypher query returns when it returns a node, a
// relationship, a map or collect(...) rather than scalar columns. They travel
// in the same columns/entries shape; value_types describes them and execution
// says how complete the answer is.

// Kinds of graph value.
const (
	CYPHER_KIND_OBJECT   = "object"
	CYPHER_KIND_RELATION = "relation"
	CYPHER_KIND_MAP      = "map"
	CYPHER_KIND_LIST     = "list"
)

// CypherValueType describes one returned value recursively. A scalar carries
// its type as the kind; graph values carry their own kind and what they are
// over.
type CypherValueType struct {
	Kind           string                      `json:"kind"`
	ObjectTypeID   string                      `json:"object_type_id,omitempty"`
	RelationTypeID string                      `json:"relation_type_id,omitempty"`
	Fields         map[string]*CypherValueType `json:"fields,omitempty"`
	Element        *CypherValueType            `json:"element,omitempty"`
}

// CypherExecution says how complete a graph-valued answer is. Matching reads
// every row the pattern matches or fails, so a returned answer is always
// complete; ConclusionsLimited says LIMIT cut the result rows, never the
// evidence inside them.
type CypherExecution struct {
	MatchRows          int  `json:"match_rows"`
	MatchComplete      bool `json:"match_complete"`
	ConclusionsLimited bool `json:"conclusions_limited"`
}

// CypherKeyValue is one primary key value, carried as text with its declared
// type so that integers beyond 2^53 survive JSON.
type CypherKeyValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// CypherObjectValue is a node: the object it is, and the properties the
// request asked it to carry.
type CypherObjectValue struct {
	Kind         string                    `json:"kind"`
	NetworkID    string                    `json:"network_id"`
	Branch       string                    `json:"branch"`
	ObjectTypeID string                    `json:"object_type_id"`
	Key          map[string]CypherKeyValue `json:"key"`
	Properties   map[string]any            `json:"properties,omitempty"`
}

// CypherRelationValue is a relationship of a direct relation type. It has no
// row of its own, so it is identified by its two ends and the binding, and
// carries the key pairs that joined them as its evidence.
type CypherRelationValue struct {
	Kind           string                `json:"kind"`
	RelationTypeID string                `json:"relation_type_id"`
	Source         CypherObjectValue     `json:"source"`
	Target         CypherObjectValue     `json:"target"`
	Basis          []CypherRelationBasis `json:"basis"`
	BindingDigest  string                `json:"binding_digest"`
}

// CypherRelationBasis is one key pair a relationship joined on.
type CypherRelationBasis struct {
	SourceProperty string `json:"source_property"`
	SourceValue    any    `json:"source_value"`
	TargetProperty string `json:"target_property"`
	TargetValue    any    `json:"target_value"`
}
