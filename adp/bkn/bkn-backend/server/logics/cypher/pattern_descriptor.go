// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

// Pattern descriptors are deliberately separate from the compiler AST, which
// may contain literal predicate values or physical compilation state.
type SemanticPattern struct {
	Nodes []SemanticPatternNode `json:"nodes"`
	Edges []SemanticPatternEdge `json:"edges,omitempty"`
	Paths []string              `json:"paths,omitempty"`
}
type SemanticPatternNode struct {
	Variable      string   `json:"variable,omitempty"`
	ObjectTypeIDs []string `json:"object_type_ids"`
}
type SemanticPatternEdge struct {
	Variable        string   `json:"variable,omitempty"`
	RelationTypeIDs []string `json:"relation_type_ids"`
	Left, Right     int
	Direction       string `json:"direction"`
	VariableLength  bool   `json:"variable_length,omitempty"`
	MinHops         int    `json:"min_hops"`
	MaxHops         int    `json:"max_hops"`
}

func describePattern(plan *GraphPatternPlan) *SemanticPattern {
	result := &SemanticPattern{Nodes: []SemanticPatternNode{}}
	for i, node := range plan.Pattern.Nodes {
		result.Nodes = append(result.Nodes, SemanticPatternNode{Variable: node.Variable, ObjectTypeIDs: sortedGraphKeys(plan.NodeChoices[i])})
	}
	for i, edge := range plan.Pattern.Edges {
		direction := "outgoing"
		switch edge.Direction {
		case Incoming:
			direction = "incoming"
		case Undirected:
			direction = "undirected"
		}
		minHops, maxHops := 1, 1
		if edge.VariableLength {
			minHops, maxHops = edge.MinHops, edge.MaxHops
		}
		result.Edges = append(result.Edges, SemanticPatternEdge{Variable: edge.Variable, RelationTypeIDs: sortedGraphKeys(plan.EdgeChoices[i]), Left: edge.Left, Right: edge.Right, Direction: direction, VariableLength: edge.VariableLength, MinHops: minHops, MaxHops: maxHops})
	}
	for _, path := range plan.Pattern.Paths {
		result.Paths = append(result.Paths, path.Variable)
	}
	return result
}
