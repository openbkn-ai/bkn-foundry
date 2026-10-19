// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// The planner resolves a graph value against the model once, so the executor
// assembles it from the plan alone: which table a node is, the typed key that
// identifies it, the properties its snapshot carries, and for a relationship
// its type, its two ends and the key pairs that joined them.

// MaxSnapshotProperties bounds the properties object_properties may ask for
// across all variables.
const MaxSnapshotProperties = 128

// PlanGraph is one resolved graph value.
type PlanGraph interface {
	planGraph()
}

// PlanNodeValue is a node: its identity, and the snapshot properties the
// request named for its variable.
type PlanNodeValue struct {
	Table    int
	Identity PlanIdentity
	Snapshot []PlanSnapshotProperty
}

func (PlanNodeValue) planGraph() {}

// PlanIdentity is what identifies a row of one table: the object type and its
// complete primary key, each key column with the type the model declares.
type PlanIdentity struct {
	Table        int
	NetworkID    string
	Branch       string
	ObjectTypeID string
	Keys         []PlanKeyColumn
}

// PlanKeyColumn is one primary key property.
type PlanKeyColumn struct {
	Property string
	Column   string
	Type     string
}

// PlanSnapshotProperty is one property a node value carries.
type PlanSnapshotProperty struct {
	Property string
	Column   string
}

// PlanRelationValue is a relationship: the relation type, the identities of
// its source and target, and the key pairs that joined them, which are the
// evidence the relationship rests on. A direct relation has no row of its
// own, so there is no edge id; the digest names the binding the model made.
type PlanRelationValue struct {
	Join           int
	RelationTypeID string
	Source         PlanIdentity
	Target         PlanIdentity
	Basis          []PlanRelationKey
	Digest         string
}

func (PlanRelationValue) planGraph() {}

// PlanRelationKey is one key pair of a relation, source side and target side.
type PlanRelationKey struct {
	SourceProperty string
	SourceColumn   string
	TargetProperty string
	TargetColumn   string
}

// PlanGraphConstant is a constant, written in the query or bound from a
// parameter.
type PlanGraphConstant struct {
	Value        Literal
	InputPointer string
}

func (PlanGraphConstant) planGraph() {}

// PlanGraphProperty is a property read inside a map.
type PlanGraphProperty struct {
	Table    int
	Column   string
	Property string
}

func (PlanGraphProperty) planGraph() {}

// PlanGraphMap is a map of graph values.
type PlanGraphMap struct {
	Keys   []string
	Values []PlanGraph
}

func (PlanGraphMap) planGraph() {}

// PlanGraphCollect is collect over a graph value.
type PlanGraphCollect struct {
	Argument PlanGraph
}

func (PlanGraphCollect) planGraph() {}

func planGraphCollects(value PlanGraph) bool {
	switch node := value.(type) {
	case PlanGraphCollect:
		return true
	case PlanGraphMap:
		for _, field := range node.Values {
			if planGraphCollects(field) {
				return true
			}
		}
	}
	return false
}

// HasGraphValues reports whether the plan returns a graph value, which only
// the bind-join executor can produce.
func (p *Plan) HasGraphValues() bool {
	for _, column := range p.Select {
		if column.Graph != nil {
			return true
		}
	}
	return false
}

// joinVariable is a relationship variable: the join it names, and which of
// the join's two tables holds the relation's source rows.
type joinVariable struct {
	join        int
	sourceTable int
	targetTable int
	mappings    []interfaces.Mapping
	digest      string
	pos         Position
}

// relationDigest names one binding of a relation type: its id, its key
// mapping and when the model last changed it. It changes when the binding
// changes, and identifies nothing beyond that.
func relationDigest(relationType *interfaces.RelationType, mappings []interfaces.Mapping) string {
	var b strings.Builder
	b.WriteString(relationType.RTID)
	for _, mapping := range mappings {
		b.WriteString("|" + mapping.SourceProp.Name + "=" + mapping.TargetProp.Name)
	}
	b.WriteString("|" + strconv.FormatInt(relationType.UpdateTime, 10))
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// planGraphColumn resolves one RETURN item that is a graph value.
func (p *planner) planGraphColumn(projection Projection) (*PlanColumn, error) {
	value, err := p.planGraphValue(projection.Value)
	if err != nil {
		return nil, err
	}
	return &PlanColumn{Alias: projection.Alias, Graph: value}, nil
}

func (p *planner) planGraphValue(value GraphExpression) (PlanGraph, error) {
	switch node := value.(type) {
	case GraphVariable:
		if table, isNode := p.tableOf[node.Name]; isNode {
			return p.planNodeValue(table, node)
		}
		if relationship, isRelationship := p.relationshipOf[node.Name]; isRelationship {
			return p.planRelationValue(relationship)
		}
		return nil, planErrorf(node.Pos, "variable %q is not defined in the MATCH pattern", node.Name)

	case GraphProperty:
		if _, isRelationship := p.relationshipOf[node.Property.Variable]; isRelationship {
			return nil, planErrorf(node.Property.Pos,
				"relationship %q has no properties: a relation here is a join between two object types",
				node.Property.Variable)
		}
		table, column, err := p.resolveProperty(node.Property)
		if err != nil {
			return nil, err
		}
		return PlanGraphProperty{Table: table, Column: column, Property: node.Property.Property}, nil

	case GraphConstant:
		if node.Parameter != nil {
			literal, err := p.resolveValue(Operand{Parameter: node.Parameter}, node.Pos)
			if err != nil {
				return nil, err
			}
			return PlanGraphConstant{Value: literal, InputPointer: parameterInputPointer(node.Parameter.Name)}, nil
		}
		return PlanGraphConstant{Value: *node.Literal, InputPointer: "$.query"}, nil

	case GraphMap:
		planned := PlanGraphMap{Keys: node.Keys}
		for _, field := range node.Values {
			resolved, err := p.planGraphValue(field)
			if err != nil {
				return nil, err
			}
			planned.Values = append(planned.Values, resolved)
		}
		return planned, nil

	case GraphCollect:
		argument, err := p.planGraphValue(node.Argument)
		if err != nil {
			return nil, err
		}
		return PlanGraphCollect{Argument: argument}, nil

	default:
		return nil, planErrorf(value.graphPosition(), "unsupported graph value %T", value)
	}
}

// planNodeValue resolves a node and the snapshot the request asked of it.
// Returning a node reads its identity, so every primary key property has to
// be readable in full, as any property the query names does.
func (p *planner) planNodeValue(table int, variable GraphVariable) (PlanGraph, error) {
	identity, err := p.planIdentity(table, variable.Pos)
	if err != nil {
		return nil, err
	}
	node := PlanNodeValue{Table: table, Identity: identity}
	for _, property := range p.snapshotProperties[variable.Name] {
		_, column, err := p.resolveProperty(PropertyRef{Variable: variable.Name, Property: property, Pos: variable.Pos})
		if err != nil {
			return nil, err
		}
		node.Snapshot = append(node.Snapshot, PlanSnapshotProperty{Property: property, Column: column})
	}
	p.snapshotUsed[variable.Name] = true
	return node, nil
}

func (p *planner) planIdentity(table int, pos Position) (PlanIdentity, error) {
	objectType := p.objectType[table]
	if len(objectType.PrimaryKeys) == 0 {
		return PlanIdentity{}, planErrorf(pos,
			"object type %q has no primary key, so its nodes have no identity to return", objectType.OTID)
	}
	identity := PlanIdentity{Table: table, NetworkID: p.plan.NetworkID, Branch: p.plan.Branch, ObjectTypeID: objectType.OTID}
	for _, key := range objectType.PrimaryKeys {
		column, err := p.resolvePropertyColumn(table, key, pos)
		if err != nil {
			return PlanIdentity{}, err
		}
		declared, _ := p.dataPropertyType(table, key)
		identity.Keys = append(identity.Keys, PlanKeyColumn{Property: key, Column: column, Type: declared})
	}
	return identity, nil
}

// planRelationValue resolves a relationship: both ends' identities and the
// key pairs that joined them, each of which the caller must be able to read.
func (p *planner) planRelationValue(relationship joinVariable) (PlanGraph, error) {
	source, err := p.planIdentity(relationship.sourceTable, relationship.pos)
	if err != nil {
		return nil, err
	}
	target, err := p.planIdentity(relationship.targetTable, relationship.pos)
	if err != nil {
		return nil, err
	}
	value := PlanRelationValue{
		Join:           relationship.join,
		RelationTypeID: p.plan.Joins[relationship.join].RelationTypeID,
		Source:         source,
		Target:         target,
		Digest:         relationship.digest,
	}
	for _, mapping := range relationship.mappings {
		sourceColumn, err := p.resolvePropertyColumn(relationship.sourceTable, mapping.SourceProp.Name, relationship.pos)
		if err != nil {
			return nil, err
		}
		targetColumn, err := p.resolvePropertyColumn(relationship.targetTable, mapping.TargetProp.Name, relationship.pos)
		if err != nil {
			return nil, err
		}
		value.Basis = append(value.Basis, PlanRelationKey{
			SourceProperty: mapping.SourceProp.Name, SourceColumn: sourceColumn,
			TargetProperty: mapping.TargetProp.Name, TargetColumn: targetColumn,
		})
	}
	return value, nil
}

// resolvePropertyColumn resolves a property of a table the way a property the
// query names is resolved, so that it is authorized like one. A property the
// caller may not know exists reads as unknown, without its name.
func (p *planner) resolvePropertyColumn(table int, property string, pos Position) (string, error) {
	objectType := p.objectType[table]
	column, err := p.schema.PropertyColumn(objectType, property)
	if err != nil {
		return "", &PlanError{Pos: pos, Err: fmt.Errorf(
			"returning a value of object type %q needs a property the caller cannot read", objectType.OTID)}
	}
	p.plan.Properties = append(p.plan.Properties, PlanProperty{ObjectTypeID: objectType.OTID, Property: property})
	return column, nil
}

// checkGraphReturn applies the limits of this version to a query that returns
// graph values, and checks object_properties against the pattern.
func (p *planner) checkGraphReturn(projections []Projection) error {
	total := 0
	for variable, properties := range p.snapshotProperties {
		total += len(properties)
		if _, isNode := p.tableOf[variable]; !isNode {
			return planErrorf(Position{}, "object_properties names %q, which is not a node of the MATCH pattern", variable)
		}
		if !p.snapshotUsed[variable] {
			return planErrorf(Position{}, "object_properties names %q, but the query does not return that node", variable)
		}
	}
	if total > MaxSnapshotProperties {
		return planErrorf(Position{}, "object_properties asks for %d properties; at most %d are allowed",
			total, MaxSnapshotProperties)
	}
	if !p.plan.HasGraphValues() {
		return nil
	}

	collects, aggregates := false, false
	var collectPos, graphPos Position
	for i, column := range p.plan.Select {
		if column.Graph != nil && graphPos == (Position{}) {
			graphPos = projectionPosition(projections[i])
		}
		switch {
		case column.Graph != nil && planGraphCollects(column.Graph):
			if _, top := column.Graph.(PlanGraphCollect); !top {
				return planErrorf(projectionPosition(projections[i]),
					"collect has to be the whole column; collect a map instead of putting collect inside one")
			}
			if !collects {
				collectPos = projectionPosition(projections[i])
			}
			collects = true
		case column.Aggregate != nil || (column.Expression != nil && planContainsAggregate(column.Expression)):
			aggregates = true
		}
	}
	if collects && aggregates {
		return planErrorf(collectPos, "collect cannot be returned beside count, sum, avg, min or max yet; "+
			"run the count as a query of its own")
	}
	if p.plan.Distinct {
		return planErrorf(graphPos, "DISTINCT over graph values is not supported yet; "+
			"nodes and relationships already group by identity, so return them with an aggregate or drop DISTINCT")
	}
	for _, order := range p.plan.OrderBy {
		if order.Alias == "" {
			continue
		}
		for i, column := range p.plan.Select {
			if column.Alias == order.Alias && column.Graph != nil {
				if _, constant := column.Graph.(PlanGraphConstant); !constant {
					return planErrorf(projectionPosition(projections[i]),
						"sorting by the graph value %q is not supported yet; return one of its properties "+
							"with an alias and sort by that", order.Alias)
				}
			}
		}
	}
	return nil
}

// graphValueTypes describes every returned column of a query that returns
// graph values.
func graphValueTypes(plan *Plan, schema *Schema) map[string]*interfaces.CypherValueType {
	types := make(map[string]*interfaces.CypherValueType, len(plan.Select))
	for _, column := range plan.Select {
		if column.Graph != nil {
			types[column.Alias] = graphValueType(plan, schema, column.Graph)
			continue
		}
		types[column.Alias] = &interfaces.CypherValueType{Kind: columnType(plan, schema, column)}
	}
	return types
}

func graphValueType(plan *Plan, schema *Schema, value PlanGraph) *interfaces.CypherValueType {
	switch node := value.(type) {
	case PlanNodeValue:
		return &interfaces.CypherValueType{Kind: interfaces.CYPHER_KIND_OBJECT, ObjectTypeID: node.Identity.ObjectTypeID}
	case PlanRelationValue:
		return &interfaces.CypherValueType{Kind: interfaces.CYPHER_KIND_RELATION, RelationTypeID: node.RelationTypeID}
	case PlanGraphConstant:
		return &interfaces.CypherValueType{Kind: literalTypeName(node.Value)}
	case PlanGraphProperty:
		return &interfaces.CypherValueType{Kind: propertyType(plan, schema, node.Table, node.Property)}
	case PlanGraphMap:
		fields := make(map[string]*interfaces.CypherValueType, len(node.Keys))
		for i, key := range node.Keys {
			fields[key] = graphValueType(plan, schema, node.Values[i])
		}
		return &interfaces.CypherValueType{Kind: interfaces.CYPHER_KIND_MAP, Fields: fields}
	case PlanGraphCollect:
		return &interfaces.CypherValueType{Kind: interfaces.CYPHER_KIND_LIST, Element: graphValueType(plan, schema, node.Argument)}
	default:
		return &interfaces.CypherValueType{Kind: "string"}
	}
}

func literalTypeName(literal Literal) string {
	switch literal.Kind {
	case LiteralInteger:
		return "integer"
	case LiteralFloat:
		return "float"
	case LiteralBoolean:
		return "boolean"
	default:
		return "string"
	}
}
