// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

const CYPHER_GRAPH_V1 = "graph_v1"

// CypherValueType recursively describes logical graph results. Scalar types
// follow the model; graph values always carry an explicit kind.
type CypherValueType struct {
	Kind           string                      `json:"kind"`
	ObjectTypeID   string                      `json:"object_type_id,omitempty"`
	RelationTypeID string                      `json:"relation_type_id,omitempty"`
	Fields         map[string]*CypherValueType `json:"fields,omitempty"`
	Element        *CypherValueType            `json:"element,omitempty"`
}

type CypherExecution struct {
	CandidateRows      int  `json:"candidate_rows,omitempty"`
	MatchRows          int  `json:"match_rows"`
	MatchComplete      bool `json:"match_complete"`
	EvidenceComplete   bool `json:"evidence_complete"`
	ConclusionsLimited bool `json:"conclusions_limited"`
}

// CypherKeyValue keeps large integers lossless on the JSON wire.
type CypherKeyValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type CypherObjectValue struct {
	// EvaluationProperties are authorized query dependencies, never implicit
	// additions to the caller's explicitly requested snapshot fields.
	EvaluationProperties map[string]any            `json:"-"`
	Kind                 string                    `json:"kind"`
	NetworkID            string                    `json:"network_id"`
	Branch               string                    `json:"branch"`
	ObjectTypeID         string                    `json:"object_type_id"`
	Key                  map[string]CypherKeyValue `json:"key"`
	Properties           map[string]any            `json:"properties,omitempty"`
}

// CypherRelationValue identifies a model-derived join fact, not a stored edge.
type CypherRelationValue struct {
	Basis          []CypherRelationBasis `json:"basis"`
	Kind           string                `json:"kind"`
	RelationTypeID string                `json:"relation_type_id"`
	Source         CypherObjectValue     `json:"source"`
	Target         CypherObjectValue     `json:"target"`
	BindingDigest  string                `json:"binding_digest"`
}

// CypherRelationBasis is the authorized observation that satisfied a join key.
type CypherRelationBasis struct {
	SourceProperty string `json:"source_property"`
	SourceValue    any    `json:"source_value"`
	TargetProperty string `json:"target_property"`
	TargetValue    any    `json:"target_value"`
}

// CypherPathValue orders nodes and relationships by traversal. Relationships
// retain their canonical model direction even during a reverse traversal.
type CypherPathValue struct {
	Kind          string                `json:"kind"`
	Nodes         []CypherObjectValue   `json:"nodes"`
	Relationships []CypherRelationValue `json:"relationships"`
}
