// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// GraphPatternPlan reads bounded, policy-filtered graph facts once per pattern
// and correlates them locally. Every physical observation is authorized before
// execution; no incoming row can trigger an unplanned data-source query.
type GraphPatternPlan struct {
	Pattern      Pattern
	Observations []*Plan
	ObjectPlans  map[string]*GraphObjectPlan
	NodeChoices  []map[string]*GraphObjectPlan
	EdgeChoices  []map[string]*GraphExpression
}

func graphChoiceType(choices []*GraphExpression, kind string) *interfaces.CypherValueType {
	typ := &interfaces.CypherValueType{Kind: kind}
	if len(choices) == 1 {
		return choices[0].Type
	}
	return typ
}

func compileGraphPattern(query *Query, schema *Schema, incoming map[string]*GraphExpression, options CompileOptions) (*Plan, map[string]*GraphExpression, []string, error) {
	graph := &GraphPatternPlan{Pattern: query.Pattern, ObjectPlans: map[string]*GraphObjectPlan{}}
	root := &Plan{NetworkID: schema.KNID, Branch: schema.Branch, Pattern: graph, Result: &GraphResultPlan{Limit: graphMatchBudget}}
	objects := map[string]*interfaces.ObjectType{}
	choices := make([][]string, len(query.Pattern.Nodes))
	for i, node := range query.Pattern.Nodes {
		if previous, ok := incoming[node.Variable]; ok && previous.Type.Kind != "object" {
			return nil, nil, nil, planErrorf(node.Pos, "variable %q is not a node", node.Variable)
		}
		label := node.Label
		if previous, ok := incoming[node.Variable]; ok && label == "" {
			label = previous.Type.ObjectTypeID
		}
		if label != "" {
			object, err := schema.ResolveLabel(label)
			if err != nil {
				return nil, nil, nil, err
			}
			choices[i] = []string{object.OTID}
			objects[object.OTID] = object
		} else {
			for _, id := range sortedGraphKeys(schema.objectTypesByID) {
				choices[i] = append(choices[i], id)
				objects[id] = schema.objectTypesByID[id]
			}
		}
	}
	relationTypes := map[string]*interfaces.RelationType{}
	edgeTypes := make([][]string, len(query.Pattern.Edges))
	for i, edge := range query.Pattern.Edges {
		names := edge.Types
		if edge.Type != "" {
			names = []string{edge.Type}
		}
		if len(names) == 0 {
			for _, id := range sortedGraphKeys(schema.relationTypesByID) {
				rt := schema.relationTypesByID[id]
				if rt.Type == interfaces.RELATION_TYPE_DIRECT {
					names = append(names, id)
				}
			}
		}
		seen := map[string]bool{}
		for _, name := range names {
			rt, err := schema.ResolveRelationType(name)
			if err != nil {
				return nil, nil, nil, err
			}
			if rt.Type != interfaces.RELATION_TYPE_DIRECT {
				return nil, nil, nil, planErrorf(edge.Pos, "relation type %q is not a direct graph fact", name)
			}
			if seen[rt.RTID] {
				continue
			}
			seen[rt.RTID] = true
			edgeTypes[i] = append(edgeTypes[i], rt.RTID)
			relationTypes[rt.RTID] = rt
			for _, id := range []string{rt.SourceObjectTypeID, rt.TargetObjectTypeID} {
				ot := schema.objectTypesByID[id]
				if ot == nil {
					return nil, nil, nil, planErrorf(edge.Pos, "relation type %q has no visible endpoints", name)
				}
				objects[id] = ot
			}
		}
	}
	if len(objects)+len(relationTypes) > 64 {
		return nil, nil, nil, planErrorf(Position{}, "graph observation budget exceeds 64 typed reads")
	}
	limit := int64(graphMatchBudget + 1)
	for _, id := range sortedGraphKeys(objects) {
		p := newScopePlanner(schema, nil, options)
		if err := p.addTable(NodeRef{Variable: "\x00object", Label: id}); err != nil {
			return nil, nil, nil, err
		}
		object, err := p.graphObject(0, CompileOptions{})
		if err != nil {
			return nil, nil, nil, err
		}
		object.AllowMissingProperties = true
		graph.ObjectPlans[id] = object
		value := &GraphExpression{Object: object, Type: &interfaces.CypherValueType{Kind: "object", ObjectTypeID: id}}
		p.plan.Result = &GraphResultPlan{Limit: graphMatchBudget, Projections: []GraphProjection{{Alias: "value", Value: value}}}
		p.plan.Limit = &limit
		graph.Observations = append(graph.Observations, p.plan)
	}
	relationShapes := map[string]*GraphExpression{}
	for _, id := range sortedGraphKeys(relationTypes) {
		rt := relationTypes[id]
		p := newScopePlanner(schema, nil, options)
		pattern := Pattern{Nodes: []NodeRef{{Variable: "\x00source", Label: rt.SourceObjectTypeID}, {Variable: "\x00target", Label: rt.TargetObjectTypeID}}, Edges: []EdgeRef{{Variable: "\x00relation", Type: id, Left: 0, Right: 1, Direction: Outgoing}}}
		if err := p.planPattern(pattern); err != nil {
			return nil, nil, nil, err
		}
		value, err := p.graphExpression(VariableExpression{Name: "\x00relation"}, map[string]EdgeRef{"\x00relation": pattern.Edges[0]}, CompileOptions{})
		if err != nil {
			return nil, nil, nil, err
		}
		p.plan.Result = &GraphResultPlan{Limit: graphMatchBudget, Projections: []GraphProjection{{Alias: "value", Value: value}}}
		p.plan.Limit = &limit
		graph.Observations = append(graph.Observations, p.plan)
		shape := *value.Relation
		shape.Source, shape.Target = graph.ObjectPlans[rt.SourceObjectTypeID], graph.ObjectPlans[rt.TargetObjectTypeID]
		relationShapes[id] = &GraphExpression{Relation: &shape, Type: value.Type}
	}
	p := newScopePlanner(schema, copyScope(incoming), options)
	p.plan = root
	scope := copyScope(incoming)
	shared := []string{}
	names := map[string]bool{}
	bind := func(name string, shape *GraphExpression, pos Position) error {
		if names[name] {
			return planErrorf(pos, "variable %q is already declared", name)
		}
		names[name] = true
		shapes := []*GraphExpression{shape}
		if previous, ok := incoming[name]; ok {
			shared = append(shared, name)
			shapes = append(shapes, previous)
		}
		value := &GraphExpression{Binding: name, Type: shape.Type, Shape: &GraphExpression{Operation: "SHAPES", Arguments: shapes}}
		p.bindings[name] = value
		if _, ok := incoming[name]; !ok {
			scope[name] = value
		}
		root.Result.Projections = append(root.Result.Projections, GraphProjection{Alias: name, Value: value})
		return nil
	}
	for i, node := range query.Pattern.Nodes {
		templates := map[string]*GraphObjectPlan{}
		shapes := []*GraphExpression{}
		for _, id := range choices[i] {
			object := *graph.ObjectPlans[id]
			object.AllowMissingProperties = node.Label == ""
			object.Properties = map[string]GraphPropertyPlan{}
			for _, property := range options.ObjectProperties[node.Variable] {
				field, err := object.owner.graphColumn(PropertyRef{Variable: "\x00object", Property: property, Pos: node.Pos})
				if err != nil {
					return nil, nil, nil, err
				}
				object.InternalProperties[property] = field
				object.Properties[property] = field
			}
			templates[id] = &object
			shapes = append(shapes, &GraphExpression{Object: &object, Type: &interfaces.CypherValueType{Kind: "object", ObjectTypeID: id}})
		}
		graph.NodeChoices = append(graph.NodeChoices, templates)
		if node.Variable != "" && !node.Anonymous {
			shape := &GraphExpression{Operation: "SHAPES", Arguments: shapes, Type: graphChoiceType(shapes, "object")}
			if err := bind(node.Variable, shape, node.Pos); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	for i, edge := range query.Pattern.Edges {
		choices := map[string]*GraphExpression{}
		shapes := []*GraphExpression{}
		for _, id := range edgeTypes[i] {
			choices[id] = relationShapes[id]
			shapes = append(shapes, relationShapes[id])
		}
		graph.EdgeChoices = append(graph.EdgeChoices, choices)
		if edge.Variable != "" {
			shape := &GraphExpression{Operation: "SHAPES", Arguments: shapes, Type: graphChoiceType(shapes, "relation")}
			if edge.VariableLength {
				shape = &GraphExpression{Operation: "LIST", Arguments: shapes, Type: &interfaces.CypherValueType{Kind: "list", Element: shape.Type}}
			}
			if previous, ok := incoming[edge.Variable]; ok && previous.Type.Kind != shape.Type.Kind {
				return nil, nil, nil, planErrorf(edge.Pos, "variable %q has an incompatible relationship binding", edge.Variable)
			}
			if err := bind(edge.Variable, shape, edge.Pos); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	for _, path := range query.Pattern.Paths {
		shape := &GraphExpression{Path: &GraphPathPlan{}, Type: &interfaces.CypherValueType{Kind: "path"}}
		for _, index := range path.Nodes {
			choices := []*GraphExpression{}
			for _, id := range sortedGraphKeys(graph.NodeChoices[index]) {
				obj := graph.NodeChoices[index][id]
				choices = append(choices, &GraphExpression{Object: obj, Type: &interfaces.CypherValueType{Kind: "object", ObjectTypeID: id}})
			}
			shape.Path.Nodes = append(shape.Path.Nodes, &GraphExpression{Operation: "SHAPES", Arguments: choices, Type: graphChoiceType(choices, "object")})
		}
		for _, index := range path.Edges {
			for _, id := range sortedGraphKeys(graph.EdgeChoices[index]) {
				shape.Path.Relationships = append(shape.Path.Relationships, graph.EdgeChoices[index][id])
			}
		}
		// Intermediate nodes may have different types from either declared endpoint.
		for _, id := range sortedGraphKeys(graph.ObjectPlans) {
			obj := graph.ObjectPlans[id]
			shape.Path.Nodes = append(shape.Path.Nodes, &GraphExpression{Object: obj, Type: &interfaces.CypherValueType{Kind: "object", ObjectTypeID: id}})
		}
		if _, ok := incoming[path.Variable]; ok {
			return nil, nil, nil, planErrorf(path.Pos, "path variable %q is already declared", path.Variable)
		}
		if err := bind(path.Variable, shape, path.Pos); err != nil {
			return nil, nil, nil, err
		}
	}
	var conditions []ValueExpression
	if inline := inlinePatternExpression(query.Where); inline != nil {
		conditions = append(conditions, inline)
	}
	if query.ResidualWhere != nil {
		conditions = append(conditions, query.ResidualWhere)
	}
	for _, condition := range conditions {
		value, err := p.graphExpression(condition, nil, options)
		if err != nil {
			return nil, nil, nil, err
		}
		if containsGraphAggregate(value) {
			return nil, nil, nil, planErrorf(Position{}, "aggregates are not allowed in MATCH predicates")
		}
		if root.Result.Where == nil {
			root.Result.Where = value
		} else {
			root.Result.Where = &GraphExpression{Operation: "AND", Type: &interfaces.CypherValueType{Kind: "boolean"}, Arguments: []*GraphExpression{root.Result.Where, value}}
		}
	}
	return root, scope, shared, nil
}

func refreshPlanProperties(plan *Plan) {
	appendChild := func(child *Plan) {
		refreshPlanProperties(child)
		plan.Properties = append(plan.Properties, child.Properties...)
	}
	for _, stage := range plan.Stages {
		appendChild(stage.Plan)
	}
	for _, branch := range plan.Union {
		appendChild(branch)
	}
	for _, subquery := range plan.Subqueries {
		appendChild(subquery)
	}
	if plan.Pattern != nil {
		for _, observation := range plan.Pattern.Observations {
			appendChild(observation)
		}
	}
	seen := map[PlanProperty]bool{}
	properties := []PlanProperty{}
	for _, property := range plan.Properties {
		if !seen[property] {
			seen[property] = true
			properties = append(properties, property)
		}
	}
	plan.Properties = properties
}

func patternNodeValue(value interfaces.CypherObjectValue, template *GraphObjectPlan) (interfaces.CypherObjectValue, error) {
	value.Properties = nil
	if len(template.Properties) > 0 {
		value.Properties = map[string]any{}
	}
	for name := range template.Properties {
		observed, ok := value.EvaluationProperties[name]
		if !ok {
			return value, fmt.Errorf("pattern observation is missing a snapshot dependency")
		}
		value.Properties[name] = observed
	}
	return value, nil
}
