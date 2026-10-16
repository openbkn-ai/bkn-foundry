// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"bytes"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"reflect"
)

func queryPatternNodes(query *Query) []NodeRef {
	nodes := append([]NodeRef(nil), query.Pattern.Nodes...)
	for _, stage := range query.Stages {
		nodes = append(nodes, queryPatternNodes(stage.Query)...)
	}
	return nodes
}

func scopeBinding(name string, value *GraphExpression) *GraphExpression {
	return &GraphExpression{Binding: name, Type: value.Type, Shape: value}
}

func shapeOf(value *GraphExpression) *GraphExpression {
	for value != nil && value.Shape != nil {
		value = value.Shape
	}
	return value
}

func unwindShape(value *GraphExpression) *GraphExpression {
	value = shapeOf(value)
	if value == nil {
		return nil
	}
	if value.Aggregate == "collect" {
		return value.Element
	}
	if value.Collection != nil && value.Collection.Kind == "list" {
		return value.Collection.Projection
	}
	if value.Subquery != nil && value.Subquery.Kind == "list" {
		return value.Subquery.Projection
	}
	if value.Operation == "SLICE" || value.Operation == "FUNCTION:tail" || value.Operation == "FUNCTION:reverse" {
		return unwindShape(value.Arguments[0])
	}
	if value.Operation == "LIST" {
		return &GraphExpression{Operation: "SHAPES", Arguments: value.Arguments}
	}
	if value.Operation == "SHAPES" {
		shape := &GraphExpression{Operation: "SHAPES"}
		for _, child := range value.Arguments {
			if item := unwindShape(child); item != nil {
				shape.Arguments = append(shape.Arguments, item)
			}
		}
		return shape
	}
	if value.Type != nil && value.Type.Kind != "list" {
		return value
	}
	return nil
}

func unwindType(value *GraphExpression) *interfaces.CypherValueType {
	shape := shapeOf(unwindShape(value))
	if shape != nil {
		if shape.Operation == "SHAPES" {
			var typ *interfaces.CypherValueType
			for _, item := range shape.Arguments {
				if typ == nil {
					typ = item.Type
				} else if !reflect.DeepEqual(typ, item.Type) {
					return &interfaces.CypherValueType{Kind: "any"}
				}
			}
			if typ != nil {
				return typ
			}
		} else if shape.Type != nil {
			return shape.Type
		}
	}
	if value.Type.Element != nil {
		return value.Type.Element
	}
	return &interfaces.CypherValueType{Kind: "any"}
}

// scopedProperty resolves graph dependencies back to their original SQL
// observation. Values needed by later stages are fetched and authorized
// before the first read, without adding them to public object snapshots.
func scopedProperty(binding *GraphExpression, key string, pos Position) (*GraphExpression, error) {
	result := &GraphExpression{Operation: "INDEX", Type: &interfaces.CypherValueType{Kind: "any"}, Arguments: []*GraphExpression{binding, {Literal: key, Type: &interfaces.CypherValueType{Kind: "string"}}}}
	shapes := []*GraphExpression{}
	var typ *interfaces.CypherValueType
	mixed := false
	recordType := func(next *interfaces.CypherValueType) {
		if typ == nil {
			typ = next
		} else if !reflect.DeepEqual(typ, next) {
			mixed = true
		}
	}
	var visit func(*GraphExpression) error
	visit = func(value *GraphExpression) error {
		value = shapeOf(value)
		if value == nil {
			return nil
		}
		if value.Operation == "SHAPES" {
			for _, item := range value.Arguments {
				if err := visit(item); err != nil {
					return err
				}
			}
			return nil
		}
		if value.Object != nil {
			object := value.Object
			if object.AllowMissingProperties {
				found := false
				ot := object.owner.objectType[object.table]
				for _, property := range ot.DataProperties {
					if property.Name == key && !object.owner.schema.hidden(ot, key) {
						found = true
						break
					}
				}
				if !found {
					if result.MissingObjectTypes == nil {
						result.MissingObjectTypes = map[string]bool{}
					}
					result.MissingObjectTypes[object.TypeID] = true
					return nil
				}
			}
			field, err := object.owner.graphColumn(PropertyRef{Variable: object.owner.plan.Tables[object.table].Variable, Property: key, Pos: pos})
			if err != nil {
				return err
			}
			object.InternalProperties[key] = field
			result.PropertyRefs = append(result.PropertyRefs, semanticPropertyRef(object.owner.plan, object.table, key))
			recordType(&interfaces.CypherValueType{Kind: field.Type})
		} else if value.Fields != nil {
			if field := value.Fields[key]; field != nil {
				shapes = append(shapes, field)
				recordType(field.Type)
			}
		} else if value.Relation != nil {
			result.EmptyRelationProperty = true
		}
		return nil
	}
	if err := visit(binding); err != nil {
		return nil, err
	}
	if typ != nil && !mixed {
		result.Type = typ
	}
	if len(shapes) == 1 {
		result.Shape = shapes[0]
	} else if len(shapes) > 1 {
		result.Shape = &GraphExpression{Operation: "SHAPES", Arguments: shapes}
	}
	return result, nil
}

func newScopePlanner(schema *Schema, scope map[string]*GraphExpression, options CompileOptions) *planner {
	return &planner{schema: schema, parameters: options.Parameters, tableOf: map[string]int{}, bindings: scope,
		plan: &Plan{NetworkID: schema.KNID, Branch: schema.Branch}}
}

func copyScope(scope map[string]*GraphExpression) map[string]*GraphExpression {
	result := map[string]*GraphExpression{}
	for name, value := range scope {
		result[name] = value
	}
	return result
}

func compilePipeline(query *Query, schema *Schema, options CompileOptions) (*Plan, error) {
	return compilePipelineScope(query, schema, options, map[string]*GraphExpression{})
}

func compilePipelineScope(query *Query, schema *Schema, options CompileOptions, incoming map[string]*GraphExpression) (*Plan, error) {
	if err := validateObjectProperties(options); err != nil {
		return nil, err
	}
	root := &Plan{NetworkID: schema.KNID, Branch: schema.Branch}
	scope := copyScope(incoming)
	knownProperties := map[string]bool{}
	for name, value := range incoming {
		if value.Type.Kind == "object" {
			knownProperties[name] = true
		}
	}
	for _, stage := range query.Stages {
		var plan *Plan
		var shared []string
		var err error
		switch stage.Kind {
		case "match":
			plan, scope, shared, err = compileMatchStage(stage.Query, schema, scope, options)
			for _, node := range stage.Query.Pattern.Nodes {
				knownProperties[node.Variable] = true
			}
		case "unwind":
			p := newScopePlanner(schema, scope, options)
			unwind := stage.Query.Unwinds[0]
			if _, exists := scope[unwind.Variable]; exists {
				return nil, planErrorf(unwind.Pos, "variable %q is already declared", unwind.Variable)
			}
			var value *GraphExpression
			value, err = p.graphExpression(unwind.Expression, nil, options)
			if err == nil && containsGraphAggregate(value) {
				return nil, planErrorf(unwind.Pos, "aggregates are not allowed in UNWIND")
			}
			if err == nil {
				typ := unwindType(value)
				scope = copyScope(scope)
				scope[unwind.Variable] = &GraphExpression{Binding: unwind.Variable, Type: typ, Shape: unwindShape(value)}
				p.plan.Result = &GraphResultPlan{Unwinds: []GraphUnwind{{Variable: unwind.Variable, Value: value}}}
			}
			plan = p.plan
		case "with", "return":
			plan, scope, err = compileProjectionStage(stage.Query, schema, scope, options, stage.Kind == "return")
		default:
			return nil, planErrorf(Position{}, "unknown query stage")
		}
		if err != nil {
			return nil, err
		}
		root.Stages = append(root.Stages, PlanStage{Kind: stage.Kind, Optional: stage.Optional, Plan: plan, Shared: shared})
	}
	for name := range options.ObjectProperties {
		if !knownProperties[name] {
			return nil, planErrorf(Position{}, "object_properties names unknown node variable %q", name)
		}
	}
	for _, stage := range root.Stages {
		root.Properties = append(root.Properties, stage.Plan.Properties...)
	}
	root.Result = root.Stages[len(root.Stages)-1].Plan.Result
	return root, nil
}

func compileMatchStage(query *Query, schema *Schema, incoming map[string]*GraphExpression, options CompileOptions) (*Plan, map[string]*GraphExpression, []string, error) {
	resolved := *query
	resolved.Pattern.Nodes = append([]NodeRef(nil), query.Pattern.Nodes...)
	for i, node := range resolved.Pattern.Nodes {
		if previous, ok := incoming[node.Variable]; ok && node.Label == "" && previous.Type.Kind == "object" {
			resolved.Pattern.Nodes[i].Label = previous.Type.ObjectTypeID
		}
	}
	query = &resolved
	if options.Correlated || patternNeedsRuntime(query.Pattern) {
		return compileGraphPattern(query, schema, incoming, options)
	}
	p := newScopePlanner(schema, copyScope(incoming), options)
	pattern := Pattern{Nodes: append([]NodeRef(nil), query.Pattern.Nodes...), Edges: query.Pattern.Edges, Paths: query.Pattern.Paths}
	shared := []string{}
	for i, node := range pattern.Nodes {
		if previous, ok := incoming[node.Variable]; ok {
			if previous.Type.Kind != "object" || previous.Type.ObjectTypeID == "" {
				return nil, nil, nil, planErrorf(node.Pos, "variable %q is not a statically typed node", node.Variable)
			}
			if node.Label == "" {
				node.Label = previous.Type.ObjectTypeID
			}
			ot, err := schema.ResolveLabel(node.Label)
			if err != nil {
				return nil, nil, nil, err
			}
			if ot.OTID != previous.Type.ObjectTypeID {
				return nil, nil, nil, planErrorf(node.Pos, "node variable %q has an incompatible label", node.Variable)
			}
			shared = append(shared, node.Variable)
		}
		if node.Label == "" {
			return nil, nil, nil, planErrorf(node.Pos, "unbound node %q requires an object type label", node.Variable)
		}
		pattern.Nodes[i] = node
		delete(p.bindings, node.Variable)
	}
	if err := p.planPattern(pattern); err != nil {
		return nil, nil, nil, err
	}
	if err := p.planWhere(query.Where); err != nil {
		return nil, nil, nil, err
	}
	relations := map[string]EdgeRef{}
	for _, edge := range pattern.Edges {
		if edge.Variable == "" {
			continue
		}
		if _, ok := p.tableOf[edge.Variable]; ok {
			return nil, nil, nil, planErrorf(edge.Pos, "variable %q is already bound to a node", edge.Variable)
		}
		if _, ok := relations[edge.Variable]; ok {
			return nil, nil, nil, planErrorf(edge.Pos, "relationship variable %q is bound twice", edge.Variable)
		}
		if edge.Direction == Undirected {
			return nil, nil, nil, planErrorf(edge.Pos, "named undirected relationships are not supported")
		}
		if previous, ok := incoming[edge.Variable]; ok {
			if previous.Type.Kind != "relation" {
				return nil, nil, nil, planErrorf(edge.Pos, "variable %q is not a relationship", edge.Variable)
			}
			shared = append(shared, edge.Variable)
		}
		delete(p.bindings, edge.Variable)
		relations[edge.Variable] = edge
	}
	scope := copyScope(incoming)
	result := &GraphResultPlan{Limit: graphMatchBudget}
	localOptions := options
	localOptions.ObjectProperties = map[string][]string{}
	for _, node := range pattern.Nodes {
		localOptions.ObjectProperties[node.Variable] = options.ObjectProperties[node.Variable]
	}
	if err := p.bindPaths(pattern, relations, localOptions); err != nil {
		return nil, nil, nil, err
	}
	for index, node := range pattern.Nodes {
		name := p.plan.Tables[index].Variable
		value, err := p.graphExpression(VariableExpression{Name: name, Pos: node.Pos}, relations, localOptions)
		if err != nil {
			return nil, nil, nil, err
		}
		result.Projections = append(result.Projections, GraphProjection{Alias: name, Value: value})
		if !node.Anonymous && node.Variable != "" {
			if _, exists := incoming[node.Variable]; !exists {
				scope[node.Variable] = scopeBinding(node.Variable, value)
			}
		}
	}
	for _, name := range sortedGraphKeys(relations) {
		value, err := p.graphExpression(VariableExpression{Name: name}, relations, localOptions)
		if err != nil {
			return nil, nil, nil, err
		}
		result.Projections = append(result.Projections, GraphProjection{Alias: name, Value: value})
		if _, exists := incoming[name]; !exists {
			scope[name] = scopeBinding(name, value)
		}
	}
	for _, path := range pattern.Paths {
		value := p.bindings[path.Variable]
		result.Projections = append(result.Projections, GraphProjection{Alias: path.Variable, Value: value})
		scope[path.Variable] = scopeBinding(path.Variable, value)
	}
	if query.ResidualWhere != nil {
		value, err := p.graphExpression(query.ResidualWhere, relations, localOptions)
		if err != nil {
			return nil, nil, nil, err
		}
		if containsGraphAggregate(value) {
			return nil, nil, nil, planErrorf(Position{}, "aggregates are not allowed in WHERE")
		}
		result.Where = value
	}
	p.plan.Result = result
	prepareGraphAggregation(result)
	limit := int64(graphMatchBudget + 1)
	p.plan.Limit = &limit
	return p.plan, scope, shared, nil
}

func compileProjectionStage(query *Query, schema *Schema, incoming map[string]*GraphExpression, options CompileOptions, final bool) (*Plan, map[string]*GraphExpression, error) {
	bound, err := bindQueryPagination(query, options.Parameters)
	if err != nil {
		return nil, nil, err
	}
	query = bound
	p := newScopePlanner(schema, incoming, options)
	result := &GraphResultPlan{Distinct: query.Distinct, Limit: graphMatchBudget, LimitExplicit: query.Limit != nil}
	if final {
		result.Limit = interfaces.CYPHER_DEFAULT_LIMIT
	}
	if query.Limit != nil {
		result.Limit = *query.Limit
	}
	if query.Skip != nil {
		result.Skip = *query.Skip
	}
	projections := append([]Projection(nil), query.Return...)
	if query.Star {
		projections = nil
		for _, name := range sortedGraphKeys(incoming) {
			projections = append(projections, Projection{Alias: name, Value: VariableExpression{Name: name}})
		}
		projections = append(projections, query.Return...)
	}
	if len(projections) == 0 {
		return nil, nil, planErrorf(Position{}, "projection has no variables")
	}
	scope := map[string]*GraphExpression{}
	for _, projection := range projections {
		if _, ok := scope[projection.Alias]; ok {
			return nil, nil, planErrorf(Position{}, "column name %q is returned twice", projection.Alias)
		}
		value, err := p.graphExpression(projection.Value, nil, options)
		if err != nil {
			return nil, nil, err
		}
		if containsGraphAggregate(value) {
			result.HasAggregates = true
		}
		result.Projections = append(result.Projections, GraphProjection{Alias: projection.Alias, Value: value})
		scope[projection.Alias] = scopeBinding(projection.Alias, value)
	}
	orderScope := copyScope(scope)
	if !result.HasAggregates && !result.Distinct {
		orderScope = copyScope(incoming)
		for name, value := range scope {
			orderScope[name] = value
		}
	}
	p.bindings = orderScope
	for _, order := range query.OrderBy {
		expression := order.Value
		if order.Alias != "" {
			expression = VariableExpression{Name: order.Alias, Pos: order.Pos}
		}
		if expression == nil && order.Property != nil {
			expression = PropertyExpression{Ref: *order.Property}
		}
		if expression == nil {
			return nil, nil, planErrorf(order.Pos, "ORDER BY an aggregate must use its projected alias")
		}
		expression = rewriteProjectedOrder(expression, projections, scope)
		value, err := p.graphExpression(expression, nil, options)
		if err != nil {
			return nil, nil, err
		}
		if containsGraphAggregate(value) {
			return nil, nil, planErrorf(order.Pos, "ORDER BY an aggregate must use its projected alias")
		}
		result.OrderBy = append(result.OrderBy, GraphOrder{Post: true, Value: value, Descending: order.Descending})
	}
	if query.PostWhere != nil {
		p.bindings = orderScope
		// A repeated volatile call is a fresh evaluation, not the value of
		// the similarly written projection. Correlated expressions also
		// keep their own planned observation and evaluation scope.
		filterProjections := []Projection{}
		for i, projection := range projections {
			if !containsVolatileExpression(result.Projections[i].Value) && !queryHasPatternReads(&Query{Return: []Projection{projection}}) {
				filterProjections = append(filterProjections, projection)
			}
		}
		predicate := rewriteProjectedOrder(query.PostWhere, filterProjections, scope)
		value, err := p.graphExpression(predicate, nil, options)
		if err != nil {
			return nil, nil, err
		}
		if containsGraphAggregate(value) {
			return nil, nil, planErrorf(Position{}, "aggregates are not allowed directly in WHERE")
		}
		result.PostWhere = value
	}
	p.plan.Result = result
	prepareGraphAggregation(result)
	return p.plan, scope, nil
}

// expressionShape compares repeated projection expressions without source
// positions. ORDER BY may reuse a projected expression through its new alias.
func expressionShape(expression ValueExpression) any {
	switch value := expression.(type) {
	case VariableExpression:
		return []any{"variable", value.Name}
	case PropertyExpression:
		return []any{"property", value.Ref.Variable, value.Ref.Property}
	case LiteralExpression:
		literal := value.Value
		literal.Pos = Position{}
		return []any{"literal", literal}
	case ParameterExpression:
		return []any{"parameter", value.Ref.Name}
	case AggregateExpression:
		return []any{"aggregate", value.Function, value.Distinct, expressionShape(value.Value), expressionShape(value.Percentile)}
	case OperationExpression:
		items := []any{"operation", value.Operator}
		for _, argument := range value.Arguments {
			items = append(items, expressionShape(argument))
		}
		return items
	case PatternExpression:
		return []any{"pattern", positionlessPatternShape(value)}
	case CollectionExpression:
		return []any{"collection", value.Kind, value.Variable, expressionShape(value.Source), expressionShape(value.Predicate), expressionShape(value.Projection)}
	case MapExpression:
		items := []any{"map"}
		for _, field := range value.Fields {
			items = append(items, []any{field.Name, expressionShape(field.Value)})
		}
		return items
	}
	return nil
}

func rewriteProjectedOrder(expression ValueExpression, projections []Projection, scope map[string]*GraphExpression) ValueExpression {
	if variable, ok := expression.(VariableExpression); ok {
		if _, exists := scope[variable.Name]; exists {
			return expression
		}
	}
	for _, projection := range projections {
		if reflect.DeepEqual(expressionShape(expression), expressionShape(projection.Value)) && !referencesReboundProjection(expression, projections, scope) {
			return VariableExpression{Name: projection.Alias}
		}
	}
	if operation, ok := expression.(OperationExpression); ok {
		operation.Arguments = append([]ValueExpression(nil), operation.Arguments...)
		for i, argument := range operation.Arguments {
			operation.Arguments[i] = rewriteProjectedOrder(argument, projections, scope)
		}
		return operation
	}
	return expression
}

// A repeated expression names the output scope. Reusing its input projection
// is valid only when its variables still refer to the same values. Consult
// the complete scope, including projections excluded from reuse (e.g. rand()).
func referencesReboundProjection(expression ValueExpression, projections []Projection, scope map[string]*GraphExpression) bool {
	rebound := func(name string) bool {
		if _, exists := scope[name]; !exists {
			return false
		}
		for _, projection := range projections {
			if projection.Alias == name {
				variable, ok := projection.Value.(VariableExpression)
				return !ok || variable.Name != name
			}
		}
		// A filtered-out projection is not known to be a pass-through.
		return true
	}
	uses := func(value ValueExpression) bool { return referencesReboundProjection(value, projections, scope) }
	switch value := expression.(type) {
	case VariableExpression:
		return rebound(value.Name)
	case PropertyExpression:
		return rebound(value.Ref.Variable)
	case OperationExpression:
		for _, argument := range value.Arguments {
			if uses(argument) {
				return true
			}
		}
	case AggregateExpression:
		return uses(value.Value) || uses(value.Percentile)
	case MapExpression:
		for _, field := range value.Fields {
			if uses(field.Value) {
				return true
			}
		}
	case CollectionExpression:
		return uses(value.Source) || uses(value.Predicate) || uses(value.Projection)
	case PatternExpression:
		for _, node := range value.Pattern.Nodes {
			if !node.Anonymous && rebound(node.Variable) {
				return true
			}
		}
		for _, edge := range value.Pattern.Edges {
			if edge.Variable != "" && rebound(edge.Variable) {
				return true
			}
		}
		for _, path := range value.Pattern.Paths {
			if path.Variable != "" && rebound(path.Variable) {
				return true
			}
		}
		return uses(value.Predicate) || uses(value.Projection) || queryReferencesReboundProjection(value.Query, projections, scope)
	}
	return false
}

func queryReferencesReboundProjection(query *Query, projections []Projection, scope map[string]*GraphExpression) bool {
	if query == nil {
		return false
	}
	uses := func(value ValueExpression) bool { return referencesReboundProjection(value, projections, scope) }
	if uses(PatternExpression{Pattern: query.Pattern, Predicate: query.FullWhere, Projection: query.PostWhere}) || uses(query.ResidualWhere) || uses(query.SkipExpression) || uses(query.LimitExpression) {
		return true
	}
	for _, projection := range query.Return {
		if uses(projection.Value) {
			return true
		}
	}
	for _, order := range query.OrderBy {
		if uses(order.Value) {
			return true
		}
		if order.Property != nil && uses(PropertyExpression{Ref: *order.Property}) {
			return true
		}
	}
	for _, unwind := range query.Unwinds {
		if uses(unwind.Expression) {
			return true
		}
	}
	for _, stage := range query.Stages {
		if queryReferencesReboundProjection(stage.Query, projections, scope) {
			return true
		}
	}
	for _, branch := range query.Union {
		if queryReferencesReboundProjection(branch, projections, scope) {
			return true
		}
	}
	return false
}

// Pattern expressions contain complete nested query trees. Compare their
// syntax without positions so ORDER BY can reuse an identical projection.
func positionlessPatternShape(value PatternExpression) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		return value
	}
	var strip func(any)
	strip = func(item any) {
		switch item := item.(type) {
		case map[string]any:
			delete(item, "Pos")
			for _, child := range item {
				strip(child)
			}
		case []any:
			for _, child := range item {
				strip(child)
			}
		}
	}
	strip(result)
	return result
}
