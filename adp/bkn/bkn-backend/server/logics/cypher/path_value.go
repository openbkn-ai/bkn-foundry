// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"

type GraphPathPlan struct {
	Nodes, Relationships []*GraphExpression
}

func (p *planner) bindPaths(pattern Pattern, relations map[string]EdgeRef, options CompileOptions) error {
	if p.bindings == nil {
		p.bindings = map[string]*GraphExpression{}
	}
	for _, path := range pattern.Paths {
		if _, ok := p.tableOf[path.Variable]; ok {
			return planErrorf(path.Pos, "path variable %q is already declared", path.Variable)
		}
		if _, ok := relations[path.Variable]; ok {
			return planErrorf(path.Pos, "path variable %q is already declared", path.Variable)
		}
		if _, ok := p.bindings[path.Variable]; ok {
			return planErrorf(path.Pos, "path variable %q is already declared", path.Variable)
		}
		value := &GraphExpression{Type: &interfaces.CypherValueType{Kind: "path"}, Path: &GraphPathPlan{}}
		for _, node := range path.Nodes {
			object, err := p.graphObject(node, options)
			if err != nil {
				return err
			}
			value.Path.Nodes = append(value.Path.Nodes, &GraphExpression{Object: object, Type: &interfaces.CypherValueType{Kind: "object", ObjectTypeID: object.TypeID}})
		}
		for _, index := range path.Edges {
			relation, err := p.graphExpression(VariableExpression{Name: "\x00path-edge", Pos: path.Pos}, map[string]EdgeRef{"\x00path-edge": pattern.Edges[index]}, options)
			if err != nil {
				return err
			}
			value.Path.Relationships = append(value.Path.Relationships, relation)
		}
		p.bindings[path.Variable] = value
	}
	return nil
}

func materializeGraphPath(plan *Plan, path *GraphPathPlan, row map[string]any) (any, error) {
	result := interfaces.CypherPathValue{Kind: "path", Nodes: []interfaces.CypherObjectValue{}, Relationships: []interfaces.CypherRelationValue{}}
	for _, node := range path.Nodes {
		value, err := evaluateGraph(plan, node, row)
		if err != nil {
			return nil, err
		}
		result.Nodes = append(result.Nodes, value.(interfaces.CypherObjectValue))
	}
	for _, relation := range path.Relationships {
		value, err := evaluateGraph(plan, relation, row)
		if err != nil {
			return nil, err
		}
		result.Relationships = append(result.Relationships, value.(interfaces.CypherRelationValue))
	}
	return result, nil
}

func pathFunctionShape(value *GraphExpression) *GraphExpression {
	nodes := value.Operation == "FUNCTION:nodes"
	choices := []*GraphExpression{}
	var visit func(*GraphExpression)
	visit = func(item *GraphExpression) {
		item = shapeOf(item)
		if item == nil {
			return
		}
		if item.Operation == "SHAPES" {
			for _, child := range item.Arguments {
				visit(child)
			}
			return
		}
		if item.Path != nil {
			if nodes {
				choices = append(choices, item.Path.Nodes...)
			} else {
				choices = append(choices, item.Path.Relationships...)
			}
		}
	}
	visit(value.Arguments[0])
	return &GraphExpression{Operation: "LIST", Arguments: choices, Type: &interfaces.CypherValueType{Kind: "list", Element: &interfaces.CypherValueType{Kind: "any"}}}
}
