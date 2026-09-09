// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"

	"bkn-backend/interfaces"
)

// The planner turns an accepted query plus the model into the shape of one
// SELECT: which resources are read, how they are joined, and which columns
// come out. It resolves every name against the schema, so generation after it
// only formats -- it never has to look anything up, and cannot invent a column
// that the model does not have.

// Plan is one SELECT statement, still in terms the generator can format for
// any dialect.
type Plan struct {
	Tables   []PlanTable
	Joins    []PlanJoin
	Where    PlanPredicate
	Select   []PlanColumn
	Distinct bool
	OrderBy  []PlanOrder
	Skip     *int64
	Limit    *int64
}

// PlanTable is one node of the pattern, bound to the resource behind its
// object type. ResourceID goes into the {{.resource_id}} placeholder that
// vega-backend substitutes, so the physical table name never appears here.
type PlanTable struct {
	Alias      string
	ResourceID string
	Label      string
}

// PlanJoin joins two tables on the key pairs of a direct relation type.
type PlanJoin struct {
	Left  int
	Right int
	Keys  []PlanJoinKey
}

// PlanJoinKey is one equality between a column of the left table and a column
// of the right one.
type PlanJoinKey struct {
	LeftColumn  string
	RightColumn string
}

// PlanColumn is one output column.
type PlanColumn struct {
	Table  int
	Column string
	Alias  string
}

// PlanPredicate is the WHERE tree with every name resolved and every parameter
// bound. Generation walks it and formats; it looks nothing up.
type PlanPredicate interface {
	planPredicate()
}

// PlanCondition is one comparison of a column against a value.
type PlanCondition struct {
	Table    int
	Column   string
	Operator string
	Value    Literal
}

func (PlanCondition) planPredicate() {}

// PlanLogical is AND or OR over two or more conditions.
type PlanLogical struct {
	Operator string
	Operands []PlanPredicate
}

func (PlanLogical) planPredicate() {}

// PlanNegation is NOT over one condition.
type PlanNegation struct {
	Operand PlanPredicate
}

func (PlanNegation) planPredicate() {}

// PlanNullCheck is IS NULL, or IS NOT NULL when negated.
type PlanNullCheck struct {
	Table   int
	Column  string
	Negated bool
}

func (PlanNullCheck) planPredicate() {}

// PlanMembership is IN over values written in the query.
type PlanMembership struct {
	Table   int
	Column  string
	Values  []Literal
	Negated bool
}

func (PlanMembership) planPredicate() {}

// PlanOrder is one ORDER BY term.
type PlanOrder struct {
	Table      int
	Column     string
	Descending bool
}

// PlanError is a query that parses and is inside the subset but does not fit
// the model it was run against: an unknown label, a relationship whose
// endpoints do not line up, a property that is not there.
type PlanError struct {
	Pos Position
	Err error
}

func (e *PlanError) Error() string {
	return fmt.Sprintf("line %d:%d: %v", e.Pos.Line, e.Pos.Column, e.Err)
}

func (e *PlanError) Unwrap() error { return e.Err }

func planErrorf(pos Position, format string, args ...any) error {
	return &PlanError{Pos: pos, Err: fmt.Errorf(format, args...)}
}

// planner carries the state that resolution needs: the schema, the tables
// built so far, and which variable names them.
type planner struct {
	schema     *Schema
	parameters map[string]any
	plan       *Plan
	objectType []*interfaces.ObjectType // per table, parallel to plan.Tables
	tableOf    map[string]int           // variable name to table index
}

// CompileOptions carries what the query itself does not: the values its
// parameters stand for.
type CompileOptions struct {
	Parameters map[string]any
}

// Compile binds a query to a knowledge network and produces its plan.
func Compile(query *Query, schema *Schema, options CompileOptions) (*Plan, error) {
	p := &planner{
		schema:     schema,
		parameters: options.Parameters,
		plan:       &Plan{Distinct: query.Distinct, Skip: query.Skip, Limit: query.Limit},
		tableOf:    map[string]int{},
	}
	if err := p.planPattern(query.Pattern); err != nil {
		return nil, err
	}
	if err := p.planWhere(query.Where); err != nil {
		return nil, err
	}
	if err := p.planReturn(query.Return); err != nil {
		return nil, err
	}
	if err := p.planOrderBy(query.OrderBy); err != nil {
		return nil, err
	}
	return p.plan, nil
}

func (p *planner) planPattern(pattern Pattern) error {
	for _, node := range pattern.Nodes {
		if err := p.addTable(node); err != nil {
			return err
		}
	}
	for i, edge := range pattern.Edges {
		if err := p.addJoin(edge, i, i+1); err != nil {
			return err
		}
	}
	return nil
}

func (p *planner) addTable(node NodeRef) error {
	objectType, err := p.schema.ResolveLabel(node.Label)
	if err != nil {
		return &PlanError{Pos: node.Pos, Err: err}
	}
	resourceID, err := p.schema.ResourceID(objectType)
	if err != nil {
		return &PlanError{Pos: node.Pos, Err: err}
	}

	index := len(p.plan.Tables)
	if node.Variable != "" {
		if previous, taken := p.tableOf[node.Variable]; taken {
			// Reusing a variable means the same rows on both sides, which is a
			// self-join the planner does not build yet -- and silently
			// treating it as two independent nodes would return the wrong
			// rows.
			return planErrorf(node.Pos, "variable %q is already bound to %q; use a different name",
				node.Variable, p.plan.Tables[previous].Label)
		}
		p.tableOf[node.Variable] = index
	}
	p.plan.Tables = append(p.plan.Tables, PlanTable{
		Alias:      fmt.Sprintf("t%d", index),
		ResourceID: resourceID,
		Label:      node.Label,
	})
	p.objectType = append(p.objectType, objectType)
	return nil
}

// addJoin turns one relationship of the pattern into a join. The relation type
// decides which side is source and which is target; the arrow in the query
// decides which pattern node plays which role, and the two must agree.
func (p *planner) addJoin(edge EdgeRef, left, right int) error {
	relationType, err := p.schema.ResolveRelationType(edge.Type)
	if err != nil {
		return &PlanError{Pos: edge.Pos, Err: err}
	}
	if relationType.Type != interfaces.RELATION_TYPE_DIRECT {
		// A filtered cross join has no key pairs to join on: it pairs every
		// row that passes one side's filter with every row that passes the
		// other. That is a different SQL shape, so it is refused rather than
		// approximated.
		return planErrorf(edge.Pos,
			"relation type %q is a %s relation; only direct relations can be used in a pattern yet",
			relationType.RTName, relationType.Type)
	}

	source, target := left, right
	if edge.Direction == Incoming {
		source, target = right, left
	}
	if got, want := p.objectType[source].OTID, relationType.SourceObjectTypeID; got != want {
		return planErrorf(edge.Pos,
			"relation type %q goes from %q to %q, but the pattern starts it at %q",
			relationType.RTName, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID, got)
	}
	if got, want := p.objectType[target].OTID, relationType.TargetObjectTypeID; got != want {
		return planErrorf(edge.Pos,
			"relation type %q goes from %q to %q, but the pattern ends it at %q",
			relationType.RTName, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID, got)
	}

	mappings, ok := relationType.MappingRules.([]interfaces.Mapping)
	if !ok || len(mappings) == 0 {
		return planErrorf(edge.Pos, "relation type %q has no key mapping to join on", relationType.RTName)
	}

	join := PlanJoin{Left: left, Right: right}
	for _, mapping := range mappings {
		sourceColumn, err := p.schema.Column(p.objectType[source], mapping.SourceProp.Name)
		if err != nil {
			return &PlanError{Pos: edge.Pos, Err: err}
		}
		targetColumn, err := p.schema.Column(p.objectType[target], mapping.TargetProp.Name)
		if err != nil {
			return &PlanError{Pos: edge.Pos, Err: err}
		}
		// Keys are stored left-to-right in pattern order, so the generator
		// does not have to know the direction again.
		leftColumn, rightColumn := sourceColumn, targetColumn
		if edge.Direction == Incoming {
			leftColumn, rightColumn = targetColumn, sourceColumn
		}
		join.Keys = append(join.Keys, PlanJoinKey{LeftColumn: leftColumn, RightColumn: rightColumn})
	}
	p.plan.Joins = append(p.plan.Joins, join)
	return nil
}

func (p *planner) planWhere(predicate Predicate) error {
	if predicate == nil {
		return nil
	}
	planned, err := p.planPredicate(predicate)
	if err != nil {
		return err
	}
	p.plan.Where = planned
	return nil
}

func (p *planner) planPredicate(predicate Predicate) (PlanPredicate, error) {
	switch node := predicate.(type) {
	case Comparison:
		table, column, err := p.resolveProperty(node.Left)
		if err != nil {
			return nil, err
		}
		value, err := p.resolveValue(node.Right, node.Pos)
		if err != nil {
			return nil, err
		}
		return PlanCondition{Table: table, Column: column, Operator: node.Operator, Value: value}, nil

	case LogicalOperator:
		operands := make([]PlanPredicate, 0, len(node.Operands))
		for _, operand := range node.Operands {
			planned, err := p.planPredicate(operand)
			if err != nil {
				return nil, err
			}
			operands = append(operands, planned)
		}
		return PlanLogical{Operator: node.Operator, Operands: operands}, nil

	case Negation:
		operand, err := p.planPredicate(node.Operand)
		if err != nil {
			return nil, err
		}
		return PlanNegation{Operand: operand}, nil

	case NullCheck:
		table, column, err := p.resolveProperty(node.Property)
		if err != nil {
			return nil, err
		}
		return PlanNullCheck{Table: table, Column: column, Negated: node.Negated}, nil

	case Membership:
		table, column, err := p.resolveProperty(node.Property)
		if err != nil {
			return nil, err
		}
		values := make([]Literal, 0, len(node.Values))
		for _, value := range node.Values {
			resolved, err := p.resolveValue(value, node.Pos)
			if err != nil {
				return nil, err
			}
			values = append(values, resolved)
		}
		return PlanMembership{Table: table, Column: column, Values: values, Negated: node.Negated}, nil

	default:
		return nil, planErrorf(predicate.predicatePosition(), "unsupported predicate %T", predicate)
	}
}

// resolveValue turns what the query wrote into the value the statement will
// carry. A parameter is looked up here, once, so generation only ever sees
// literals and the escaping rules apply to both the same way.
func (p *planner) resolveValue(value Operand, pos Position) (Literal, error) {
	if value.Literal != nil {
		return *value.Literal, nil
	}
	if value.Parameter == nil {
		return Literal{}, planErrorf(pos, "a comparison with no value")
	}

	name := value.Parameter.Name
	supplied, ok := p.parameters[name]
	if !ok {
		return Literal{}, planErrorf(value.Parameter.Pos, "parameter %q was not supplied", name)
	}
	literal, err := literalFromParameter(supplied)
	if err != nil {
		return Literal{}, planErrorf(value.Parameter.Pos, "parameter %q %v", name, err)
	}
	literal.Pos = value.Parameter.Pos
	return literal, nil
}

func (p *planner) planReturn(projections []Projection) error {
	seen := make(map[string]bool, len(projections))
	for _, projection := range projections {
		table, column, err := p.resolveProperty(projection.Property)
		if err != nil {
			return err
		}
		if seen[projection.Alias] {
			// Two columns with one name would make the result unreadable by
			// key, and the caller reads rows as objects.
			return planErrorf(projection.Property.Pos,
				"column name %q is returned twice; give one of them a different alias", projection.Alias)
		}
		seen[projection.Alias] = true
		p.plan.Select = append(p.plan.Select, PlanColumn{Table: table, Column: column, Alias: projection.Alias})
	}
	return nil
}

func (p *planner) planOrderBy(keys []SortKey) error {
	for _, key := range keys {
		table, column, err := p.resolveProperty(key.Property)
		if err != nil {
			return err
		}
		// DISTINCT collapses rows before they are ordered, so sorting by
		// something that was not returned has no defined answer: both MySQL
		// and PostgreSQL refuse the statement. Refusing it here says which
		// key is the problem, instead of letting the database report a
		// statement the caller never wrote.
		if p.plan.Distinct && !p.isProjected(table, column) {
			return planErrorf(key.Property.Pos,
				"%s is not returned, and DISTINCT can only be sorted by a returned value; add it to RETURN or drop DISTINCT",
				key.Property)
		}
		p.plan.OrderBy = append(p.plan.OrderBy, PlanOrder{
			Table:      table,
			Column:     column,
			Descending: key.Descending,
		})
	}
	return nil
}

func (p *planner) isProjected(table int, column string) bool {
	for _, projected := range p.plan.Select {
		if projected.Table == table && projected.Column == column {
			return true
		}
	}
	return false
}

func (p *planner) resolveProperty(ref PropertyRef) (int, string, error) {
	table, bound := p.tableOf[ref.Variable]
	if !bound {
		return 0, "", planErrorf(ref.Pos, "variable %q is not defined in the MATCH pattern", ref.Variable)
	}
	column, err := p.schema.Column(p.objectType[table], ref.Property)
	if err != nil {
		return 0, "", &PlanError{Pos: ref.Pos, Err: err}
	}
	return table, column, nil
}
