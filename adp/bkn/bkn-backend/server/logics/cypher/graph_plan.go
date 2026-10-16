// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const graphMatchBudget = interfaces.CYPHER_MAX_LIMIT - 1
const graphPayloadBudget = 8 << 20

// GraphResultPlan separates logical output expressions from the authorized
// physical columns. Limit applies only after grouping has finished.
type GraphResultPlan struct {
	Aggregation   *GraphAggregation
	PostWhere     *GraphExpression
	Unwinds       []GraphUnwind
	Where         *GraphExpression
	OrderBy       []GraphOrder
	Distinct      bool
	Skip          int64
	LimitExplicit bool
	Projections   []GraphProjection
	HasAggregates bool
	Limit         int64
}
type GraphUnwind struct {
	Variable string
	Value    *GraphExpression
}
type GraphProjection struct {
	Alias string
	Value *GraphExpression
}
type GraphExpression struct {
	EmptyRelationProperty bool
	Subquery              *GraphSubquery
	MissingObjectTypes    map[string]bool
	Path                  *GraphPathPlan
	Collection            *GraphCollection
	PropertyRefs          []string
	Shape                 *GraphExpression
	Binding               string
	Operation             string
	Arguments             []*GraphExpression
	Aggregate             string
	Distinct              bool
	InputPointer          string
	PropertyRef           string
	Type                  *interfaces.CypherValueType
	Column                string
	Literal               any
	Object                *GraphObjectPlan
	Relation              *GraphRelationPlan
	Fields                map[string]*GraphExpression
	Element               *GraphExpression
}
type GraphPropertyPlan struct {
	Column string
	Type   string
}
type GraphObjectPlan struct {
	AllowMissingProperties bool
	owner                  *planner
	table                  int
	InternalProperties     map[string]GraphPropertyPlan
	TypeID                 string
	Keys                   map[string]GraphPropertyPlan
	Properties             map[string]GraphPropertyPlan
}
type GraphRelationPlan struct {
	Basis                 []GraphRelationBasisPlan
	TypeID, BindingDigest string
	Source, Target        *GraphObjectPlan
}

func (p *planner) planGraphReturn(query *Query, options CompileOptions) error {
	if err := validateObjectProperties(options); err != nil {
		return err
	}

	relations := map[string]EdgeRef{}
	for _, edge := range query.Pattern.Edges {
		if edge.Variable == "" {
			continue
		}
		if _, exists := p.tableOf[edge.Variable]; exists {
			return planErrorf(edge.Pos, "variable %q is already bound to a node", edge.Variable)
		}
		if _, exists := relations[edge.Variable]; exists {
			return planErrorf(edge.Pos, "relationship variable %q is bound twice", edge.Variable)
		}
		if edge.Direction == Undirected {
			return planErrorf(edge.Pos, "named undirected relationships are not supported in graph_v1")
		}
		relations[edge.Variable] = edge
	}
	for variable := range options.ObjectProperties {
		if _, exists := p.tableOf[variable]; !exists {
			return planErrorf(Position{}, "object_properties names unknown node variable %q", variable)
		}
	}
	p.bindings = map[string]*GraphExpression{}
	if err := p.bindPaths(query.Pattern, relations, options); err != nil {
		return err
	}
	result := &GraphResultPlan{Limit: interfaces.CYPHER_DEFAULT_LIMIT, LimitExplicit: query.Limit != nil}
	if query.Limit != nil {
		result.Limit = *query.Limit
	}
	if result.Limit < 0 || result.Limit > interfaces.CYPHER_MAX_LIMIT {
		return planErrorf(Position{}, "invalid graph result LIMIT")
	}
	seen := map[string]bool{}
	// MATCH filters have no access to variables introduced by later UNWINDs.
	if query.ResidualWhere != nil {
		where, err := p.graphExpression(query.ResidualWhere, relations, options)
		if err != nil {
			return err
		}
		if containsGraphAggregate(where) {
			return planErrorf(Position{}, "aggregates are not allowed in WHERE")
		}
		result.Where = where
	}
	for _, unwind := range query.Unwinds {
		if _, ok := p.tableOf[unwind.Variable]; ok {
			return planErrorf(unwind.Pos, "variable %q is already declared", unwind.Variable)
		}
		if _, ok := relations[unwind.Variable]; ok {
			return planErrorf(unwind.Pos, "variable %q is already declared", unwind.Variable)
		}
		if _, ok := p.bindings[unwind.Variable]; ok {
			return planErrorf(unwind.Pos, "variable %q is already declared", unwind.Variable)
		}
		value, err := p.graphExpression(unwind.Expression, relations, options)
		if err != nil {
			return err
		}
		if containsGraphAggregate(value) {
			return planErrorf(unwind.Pos, "aggregates are not allowed in UNWIND")
		}
		typ := unwindType(value)
		p.bindings[unwind.Variable] = &GraphExpression{Binding: unwind.Variable, Type: typ, Shape: unwindShape(value)}
		result.Unwinds = append(result.Unwinds, GraphUnwind{Variable: unwind.Variable, Value: value})
	}
	for _, projection := range query.Return {
		if seen[projection.Alias] {
			return planErrorf(Position{}, "column name %q is returned twice", projection.Alias)
		}
		seen[projection.Alias] = true
		value, err := p.graphExpression(projection.Value, relations, options)
		if err != nil {
			return err
		}
		if containsGraphAggregate(value) {
			result.HasAggregates = true
		}
		result.Projections = append(result.Projections, GraphProjection{Alias: projection.Alias, Value: value})
	}

	result.Distinct = query.Distinct
	if query.Skip != nil {
		result.Skip = *query.Skip
	}
	for _, order := range query.OrderBy {
		item := GraphOrder{Alias: order.Alias, Descending: order.Descending}
		if item.Alias != "" {
			if !seen[item.Alias] {
				return planErrorf(order.Pos, "ORDER BY alias %q is not returned", item.Alias)
			}
		} else {
			expression := order.Value
			if expression == nil && order.Property != nil {
				expression = PropertyExpression{Ref: *order.Property}
			}
			if expression == nil {
				return planErrorf(order.Pos, "this ORDER BY expression is not supported")
			}
			// Reuse syntax before compiling another correlated observation.
			// Equivalent subquery plans have distinct pointers and cannot be
			// recognized by comparing compiled graph expressions.
			for _, projection := range query.Return {
				if reflect.DeepEqual(expressionShape(expression), expressionShape(projection.Value)) {
					item.Alias = projection.Alias
					break
				}
			}
			if item.Alias != "" {
				result.OrderBy = append(result.OrderBy, item)
				continue
			}
			value, err := p.graphExpression(expression, relations, options)
			if err != nil {
				return err
			}
			for _, projection := range result.Projections {
				if reflect.DeepEqual(value, projection.Value) {
					item.Alias = projection.Alias
					break
				}
			}
			if item.Alias == "" && (result.HasAggregates || result.Distinct) && graphDependsOnRow(value, nil) {
				return planErrorf(order.Pos, "grouped or DISTINCT results can only be sorted by returned values")
			}
			item.Value = value
		}
		result.OrderBy = append(result.OrderBy, item)
	}
	// Constant-only projections still need one physical column to preserve the
	// number of matching facts. Its value is never exposed to the caller.
	if len(p.plan.Select) == 0 && len(p.plan.Tables) > 0 {
		ot := p.objectType[0]
		if len(ot.DataProperties) == 0 {
			return planErrorf(Position{}, "graph query has no mapped fact column")
		}
		column, err := p.schema.Column(ot, ot.DataProperties[0].Name)
		if err != nil {
			return err
		}
		p.plan.Select = append(p.plan.Select, PlanColumn{Table: 0, Column: column, Alias: "_g0"})
	}
	physicalLimit := int64(graphMatchBudget + 1)
	p.plan.Limit = &physicalLimit
	p.plan.Skip = nil
	p.plan.Distinct = false
	p.plan.Result = result
	prepareGraphAggregation(result)
	return nil
}

func (p *planner) graphColumn(ref PropertyRef) (GraphPropertyPlan, error) {
	table, column, err := p.resolveProperty(ref)
	if err != nil {
		return GraphPropertyPlan{}, err
	}
	alias := ""
	for _, selected := range p.plan.Select {
		if selected.Table == table && selected.Column == column {
			alias = selected.Alias
			break
		}
	}
	if alias == "" {
		alias = fmt.Sprintf("_g%d", len(p.plan.Select))
		p.plan.Select = append(p.plan.Select, PlanColumn{Table: table, Column: column, Property: ref.Property, Alias: alias})
	}
	typ := ""
	for _, dp := range p.objectType[table].DataProperties {
		if dp.Name == ref.Property {
			typ = dp.Type
			break
		}
	}
	return GraphPropertyPlan{Column: alias, Type: typ}, nil
}

func (p *planner) graphObject(table int, options CompileOptions) (*GraphObjectPlan, error) {
	ot := p.objectType[table]
	if len(ot.PrimaryKeys) == 0 {
		return nil, planErrorf(Position{}, "object type %q requires a complete primary key for graph values", ot.OTID)
	}
	object := &GraphObjectPlan{owner: p, table: table, TypeID: ot.OTID, Keys: map[string]GraphPropertyPlan{}, Properties: map[string]GraphPropertyPlan{}, InternalProperties: map[string]GraphPropertyPlan{}}
	for _, key := range ot.PrimaryKeys {
		if p.schema.hidden(ot, key) {
			return nil, planErrorf(Position{}, "object type %q has no accessible complete graph identity", ot.OTID)
		}
		field, err := p.graphColumn(PropertyRef{Variable: p.plan.Tables[table].Variable, Property: key})
		if err != nil {
			return nil, err
		}
		if !interfaces.ValidPrimaryKeyTypes[field.Type] {
			return nil, planErrorf(Position{}, "object type %q has an unsupported graph identity type", ot.OTID)
		}
		object.Keys[key] = field
	}
	for _, property := range options.ObjectProperties[p.plan.Tables[table].Variable] {
		field, err := p.graphColumn(PropertyRef{Variable: p.plan.Tables[table].Variable, Property: property})
		if err != nil {
			return nil, err
		}
		object.Properties[property] = field
	}
	return object, nil
}

func (p *planner) graphExpression(expr ValueExpression, relations map[string]EdgeRef, options CompileOptions) (*GraphExpression, error) {
	result := &GraphExpression{Type: &interfaces.CypherValueType{}}
	switch value := expr.(type) {
	case PatternExpression:
		return p.graphSubquery(value, relations, options)
	case CollectionExpression:
		return p.graphCollection(value, relations, options)
	case OperationExpression:
		if err := validateExpressionOperation(value.Operator, len(value.Arguments)); err != nil {
			return nil, planErrorf(value.Pos, "%v", err)
		}
		result.Operation = value.Operator
		result.Type.Kind = operationResultType(value.Operator)
		for _, argument := range value.Arguments {
			child, err := p.graphExpression(argument, relations, options)
			if err != nil {
				return nil, err
			}
			result.Arguments = append(result.Arguments, child)
		}
		if value.Operator == "HAS_LABEL" {
			if label, ok := result.Arguments[1].Literal.(string); ok {
				if object, err := p.schema.ResolveLabel(label); err == nil {
					result.Arguments[1].Literal = object.OTID
				}
			}
		}
		if value.Operator == "LIST" {
			result.Type.Element = &interfaces.CypherValueType{Kind: "any"}
		}
		if value.Operator == "INDEX" {
			if key, ok := result.Arguments[1].Literal.(string); ok && result.Arguments[1].Operation == "" && result.Arguments[1].Binding == "" {
				return scopedProperty(result.Arguments[0], key, value.Pos)
			}
			result.Shape = unwindShape(result.Arguments[0])
		}
		propagateExpressionShape(result)
	case ParameterExpression:
		supplied, exists := options.Parameters[value.Ref.Name]
		if !exists {
			supplied, exists = p.parameters[value.Ref.Name]
		}
		if !exists {
			return nil, planErrorf(value.Ref.Pos, "parameter %q was not supplied", value.Ref.Name)
		}
		parameter, err := normalizeExpressionParameter(supplied)
		if err != nil {
			return nil, planErrorf(value.Ref.Pos, "parameter %q: %v", value.Ref.Name, err)
		}
		result.Literal = parameter
		result.Type.Kind = expressionValueType(parameter)
		result.InputPointer = parameterInputPointer(value.Ref.Name)
	case AggregateExpression:
		child, err := p.graphExpression(value.Value, relations, options)
		if err != nil {
			return nil, err
		}
		if containsVolatileExpression(child) {
			return nil, planErrorf(value.Pos, "aggregate arguments must be deterministic; rand() is not constant")
		}
		if containsGraphAggregate(child) {
			return nil, planErrorf(value.Pos, "nested aggregates are not supported")
		}
		if value.Percentile != nil {
			point, err := p.graphExpression(value.Percentile, relations, options)
			if err != nil {
				return nil, err
			}
			if containsGraphAggregate(point) || containsVolatileExpression(point) {
				return nil, planErrorf(value.Pos, "percentile cannot contain aggregates or volatile functions")
			}
			result.Arguments = []*GraphExpression{point}
		}
		result.Aggregate = value.Function
		result.Element = child
		result.Distinct = value.Distinct
		result.Type.Kind = "any"
		if value.Function == "collect" {
			result.Type.Kind = "list"
			result.Type.Element = child.Type
		} else if value.Function == "count" {
			result.Type.Kind = "integer"
		} else if value.Function == "avg" || value.Function == "stdev" || value.Function == "stdevp" || value.Percentile != nil {
			result.Type.Kind = "float"
		}
		if (value.Function == "avg" || value.Function == "sum") && child.Type.Kind == "duration" {
			result.Type.Kind = "duration"
		}

	case PropertyExpression:
		if binding, ok := p.bindings[value.Ref.Variable]; ok {
			return scopedProperty(binding, value.Ref.Property, value.Ref.Pos)
		}
		if _, ok := relations[value.Ref.Variable]; ok {
			relation, err := p.graphExpression(VariableExpression{Name: value.Ref.Variable, Pos: value.Ref.Pos}, relations, options)
			if err != nil {
				return nil, err
			}
			return scopedProperty(relation, value.Ref.Property, value.Ref.Pos)
		}
		field, err := p.graphColumn(value.Ref)
		if err != nil {
			return nil, err
		}
		result.Type.Kind = field.Type
		result.Column = field.Column
		result.PropertyRef = semanticPropertyRef(p.plan, p.tableOf[value.Ref.Variable], value.Ref.Property)
	case LiteralExpression:
		switch value.Value.Kind {
		case LiteralString:
			result.Type.Kind = "string"
			result.Literal = value.Value.String
		case LiteralInteger:
			result.Type.Kind = "integer"
			result.Literal = value.Value.Integer
		case LiteralFloat:
			result.Type.Kind = "float"
			result.Literal = value.Value.Float
		case LiteralBoolean:
			result.Type.Kind = "boolean"
			result.Literal = value.Value.Boolean
		default:
			result.Type.Kind = "null"
		}
	case VariableExpression:
		if binding, ok := p.bindings[value.Name]; ok {
			return binding, nil
		}
		if table, ok := p.tableOf[value.Name]; ok {
			object, err := p.graphObject(table, options)
			if err != nil {
				return nil, err
			}
			result.Object = object
			result.Type.Kind = "object"
			result.Type.ObjectTypeID = object.TypeID
		} else if edge, ok := relations[value.Name]; ok {
			source, target := edge.Left, edge.Right
			if edge.Direction == Incoming {
				source, target = target, source
			}
			src, err := p.graphObject(source, options)
			if err != nil {
				return nil, err
			}
			dst, err := p.graphObject(target, options)
			if err != nil {
				return nil, err
			}
			rt, err := p.schema.ResolveRelationType(edge.Type)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(struct {
				ID      string
				Version int64
				Mapping any
			}{rt.RTID, rt.UpdateTime, rt.MappingRules})
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(raw)
			result.Relation = &GraphRelationPlan{TypeID: rt.RTID, BindingDigest: "sha256:" + hex.EncodeToString(digest[:]), Source: src, Target: dst}
			for _, mapping := range rt.MappingRules.([]interfaces.Mapping) {
				if p.schema.hidden(p.objectType[source], mapping.SourceProp.Name) || p.schema.hidden(p.objectType[target], mapping.TargetProp.Name) {
					return nil, planErrorf(value.Pos, "relation type %q has unavailable graph evidence fields", rt.RTID)
				}
				sourceField, err := p.graphColumn(PropertyRef{Variable: p.plan.Tables[source].Variable, Property: mapping.SourceProp.Name})
				if err != nil {
					return nil, err
				}
				targetField, err := p.graphColumn(PropertyRef{Variable: p.plan.Tables[target].Variable, Property: mapping.TargetProp.Name})
				if err != nil {
					return nil, err
				}
				result.Relation.Basis = append(result.Relation.Basis, GraphRelationBasisPlan{SourceProperty: mapping.SourceProp.Name, TargetProperty: mapping.TargetProp.Name, SourceColumn: sourceField.Column, TargetColumn: targetField.Column})
			}
			result.Type.Kind = "relation"
			result.Type.RelationTypeID = rt.RTID
		} else {
			return nil, planErrorf(value.Pos, "variable %q is not defined in the MATCH pattern", value.Name)
		}
	case MapExpression:
		result.Type.Kind = "map"
		result.Type.Fields = map[string]*interfaces.CypherValueType{}
		result.Fields = map[string]*GraphExpression{}
		for _, field := range value.Fields {
			planned, err := p.graphExpression(field.Value, relations, options)
			if err != nil {
				return nil, err
			}
			result.Fields[field.Name] = planned
			result.Type.Fields[field.Name] = planned.Type
		}

	default:
		return nil, planErrorf(Position{}, "unsupported graph result expression %T", expr)
	}
	if err := validatePlannedExpression(result); err != nil {
		return nil, planErrorf(Position{}, "%v", err)
	}
	return result, nil
}

func sortedGraphKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type GraphRelationBasisPlan struct {
	SourceProperty, TargetProperty string
	SourceColumn, TargetColumn     string
}

type GraphOrder struct {
	Post       bool
	Alias      string
	Value      *GraphExpression
	Descending bool
}

func validateObjectProperties(options CompileOptions) error {
	propertyCount := 0
	for _, properties := range options.ObjectProperties {
		propertyCount += len(properties)
	}
	if len(options.ObjectProperties) > interfaces.CYPHER_MAX_PATTERN_NODES || propertyCount > 128 {
		return planErrorf(Position{}, "object_properties exceeds the graph projection budget")
	}

	return nil
}
