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
	GroupBy  []PlanColumn
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
//
// Readings holds one set of key pairs per way the relation can be read. A
// directed pattern has one; an undirected one between two nodes of the same
// object type has two, and a row matches if either holds.
type PlanJoin struct {
	Left     int
	Right    int
	Readings [][]PlanJoinKey
}

// PlanJoinKey is one equality between a column of the left table and a column
// of the right one.
type PlanJoinKey struct {
	LeftColumn  string
	RightColumn string
}

// PlanColumn is one output column: a column of a table, or an aggregate over
// one. Exactly one of the two is set.
type PlanColumn struct {
	Table     int
	Column    string
	Alias     string
	Aggregate *PlanAggregate
}

// PlanAggregate is COUNT, SUM, AVG, MIN or MAX. Star is count(*), which counts
// rows and names no column.
type PlanAggregate struct {
	Function string
	Distinct bool
	Star     bool
	Table    int
	Column   string
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

// PlanColumnComparison compares two columns. The analyzer does not produce one
// -- a query comparing two properties is refused -- but the planner needs it
// to say that two hops of a pattern did not traverse the same relationship.
type PlanColumnComparison struct {
	LeftTable   int
	LeftColumn  string
	Operator    string
	RightTable  int
	RightColumn string
}

func (PlanColumnComparison) planPredicate() {}

// PlanOrder is one ORDER BY term: a column, an aggregate, or the name of an
// output column. Ordering by the output name is how a grouped result is sorted
// by something the query already computed, without computing it twice.
type PlanOrder struct {
	Table      int
	Column     string
	Aggregate  *PlanAggregate
	Alias      string
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
	// hops records what each relationship of the pattern ended up connecting,
	// which is what the relationship-uniqueness rule compares.
	hops []plannedHop
	// distinctness holds the conditions that keep one pattern from traversing
	// the same relationship twice. They are conditions, but they come from the
	// pattern rather than from anything the author wrote.
	distinctness []PlanPredicate
	objectType   []*interfaces.ObjectType // per table, parallel to plan.Tables
	tableOf      map[string]int           // variable name to table index
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
	return p.keepRelationshipsDistinct(pattern)
}

// keepRelationshipsDistinct adds what Cypher requires and SQL has no notion
// of: one pattern may not traverse the same relationship twice.
//
// A relationship here is a pair of rows -- one on the relation's source side,
// one on its target side -- so two hops over the same relation type are the
// same relationship exactly when both pairs coincide. Every pair of such hops
// is compared, not only neighbouring ones: hops with another hop between them
// can coincide just as easily, and two hops in the same direction coincide on
// a row that points at itself.
func (p *planner) keepRelationshipsDistinct(pattern Pattern) error {
	if err := p.refuseUndirectedRepeats(pattern); err != nil {
		return err
	}
	for i := 0; i < len(p.hops); i++ {
		for j := i + 1; j < len(p.hops); j++ {
			if p.hops[i].relationType != p.hops[j].relationType {
				continue
			}
			distinct, err := p.differentEdges(p.hops[i], p.hops[j])
			if err != nil {
				return err
			}
			p.distinctness = append(p.distinctness, distinct)
		}
	}
	return nil
}

// refuseUndirectedRepeats turns away the one shape the rule cannot be stated
// for: which reading of an undirected hop matched decides whether it repeats
// another hop, and a condition cannot ask that after the fact.
func (p *planner) refuseUndirectedRepeats(pattern Pattern) error {
	for i, edge := range pattern.Edges {
		if edge.Direction != Undirected {
			continue
		}
		for j, other := range pattern.Edges {
			if i == j || other.Type != edge.Type {
				continue
			}
			return planErrorf(edge.Pos,
				"a pattern with two hops over %q cannot have one of them undirected; write the directions out",
				edge.Type)
		}
	}
	return nil
}

// differentEdges says two hops did not traverse the same relationship: their
// source rows and their target rows are not both the same.
func (p *planner) differentEdges(first, second plannedHop) (PlanPredicate, error) {
	var same []PlanPredicate
	for _, side := range [][2]int{{first.source, second.source}, {first.target, second.target}} {
		if side[0] == side[1] {
			// Both hops meet the same table on this side, so the rows are the
			// same by construction and there is nothing to compare.
			continue
		}
		equal, err := p.sameRow(side[0], side[1], second.pos)
		if err != nil {
			return nil, err
		}
		same = append(same, equal...)
	}
	if len(same) == 1 {
		return PlanNegation{Operand: same[0]}, nil
	}
	return PlanNegation{Operand: PlanLogical{Operator: "AND", Operands: same}}, nil
}

// sameRow compares two tables of one object type by primary key, which is the
// only thing that identifies a row here.
func (p *planner) sameRow(left, right int, pos Position) ([]PlanPredicate, error) {
	keys := p.objectType[left].PrimaryKeys
	if len(keys) == 0 {
		return nil, planErrorf(pos,
			"object type %q has no primary key, so this pattern cannot be checked for traversing the same relationship twice",
			p.objectType[left].OTID)
	}

	equal := make([]PlanPredicate, 0, len(keys))
	for _, key := range keys {
		leftColumn, err := p.schema.Column(p.objectType[left], key)
		if err != nil {
			return nil, &PlanError{Pos: pos, Err: err}
		}
		rightColumn, err := p.schema.Column(p.objectType[right], key)
		if err != nil {
			return nil, &PlanError{Pos: pos, Err: err}
		}
		equal = append(equal, PlanColumnComparison{
			LeftTable: left, LeftColumn: leftColumn,
			Operator:   "=",
			RightTable: right, RightColumn: rightColumn,
		})
	}
	return equal, nil
}

// plannedHop is one relationship of the pattern after direction is settled:
// which table holds the relation's source rows and which holds its targets.
type plannedHop struct {
	relationType string
	source       int
	target       int
	pos          Position
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
// decides which pattern node plays which role. An undirected relationship
// leaves that open, so every reading the object types allow is kept and the
// row matches if any of them holds.
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

	mappings, ok := relationType.MappingRules.([]interfaces.Mapping)
	if !ok || len(mappings) == 0 {
		return planErrorf(edge.Pos, "relation type %q has no key mapping to join on", relationType.RTName)
	}

	join := PlanJoin{Left: left, Right: right}
	for _, forwards := range p.readings(edge.Direction) {
		source, target := left, right
		if !forwards {
			source, target = right, left
		}
		if p.objectType[source].OTID != relationType.SourceObjectTypeID ||
			p.objectType[target].OTID != relationType.TargetObjectTypeID {
			continue
		}
		keys, err := p.joinKeys(mappings, source, target, forwards)
		if err != nil {
			return err
		}
		join.Readings = append(join.Readings, keys)
		// One hop is recorded per edge, not per reading. An undirected edge
		// has two readings and no recorded hop: a lone one has nothing to
		// coincide with, and one beside another over the same relation type
		// is refused before this.
		if edge.Direction != Undirected {
			p.hops = append(p.hops, plannedHop{
				relationType: relationType.RTID,
				source:       source,
				target:       target,
				pos:          edge.Pos,
			})
		}
	}

	if len(join.Readings) == 0 {
		return planErrorf(edge.Pos,
			"relation type %q goes from %q to %q, which does not connect %q and %q the way this pattern reads it",
			relationType.RTName, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID,
			p.objectType[left].OTID, p.objectType[right].OTID)
	}
	p.plan.Joins = append(p.plan.Joins, join)
	return nil
}

// readings lists the ways an edge may be read: one for a directed pattern,
// both for an undirected one.
func (p *planner) readings(direction Direction) []bool {
	switch direction {
	case Outgoing:
		return []bool{true}
	case Incoming:
		return []bool{false}
	default:
		return []bool{true, false}
	}
}

func (p *planner) joinKeys(mappings []interfaces.Mapping, source, target int, forwards bool) ([]PlanJoinKey, error) {
	keys := make([]PlanJoinKey, 0, len(mappings))
	for _, mapping := range mappings {
		sourceColumn, err := p.schema.Column(p.objectType[source], mapping.SourceProp.Name)
		if err != nil {
			return nil, &PlanError{Err: err}
		}
		targetColumn, err := p.schema.Column(p.objectType[target], mapping.TargetProp.Name)
		if err != nil {
			return nil, &PlanError{Err: err}
		}
		// Keys are stored left-to-right in pattern order, so the generator
		// does not have to know the direction again.
		leftColumn, rightColumn := sourceColumn, targetColumn
		if !forwards {
			leftColumn, rightColumn = targetColumn, sourceColumn
		}
		keys = append(keys, PlanJoinKey{LeftColumn: leftColumn, RightColumn: rightColumn})
	}
	return keys, nil
}

func (p *planner) planWhere(predicate Predicate) error {
	conditions := p.distinctness
	if predicate != nil {
		planned, err := p.planPredicate(predicate)
		if err != nil {
			return err
		}
		conditions = append(conditions, planned)
	}

	switch len(conditions) {
	case 0:
	case 1:
		p.plan.Where = conditions[0]
	default:
		p.plan.Where = PlanLogical{Operator: "AND", Operands: conditions}
	}
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
		column, err := p.planProjection(projection)
		if err != nil {
			return err
		}
		if seen[projection.Alias] {
			// Two columns with one name would make the result unreadable by
			// key, and the caller reads rows as objects.
			return planErrorf(projectionPosition(projection),
				"column name %q is returned twice; give one of them a different alias", projection.Alias)
		}
		seen[projection.Alias] = true
		p.plan.Select = append(p.plan.Select, *column)
	}
	p.planGrouping()
	return nil
}

func (p *planner) planProjection(projection Projection) (*PlanColumn, error) {
	if projection.Aggregate != nil {
		aggregate, err := p.planAggregate(*projection.Aggregate)
		if err != nil {
			return nil, err
		}
		return &PlanColumn{Alias: projection.Alias, Aggregate: aggregate}, nil
	}

	table, column, err := p.resolveProperty(*projection.Property)
	if err != nil {
		return nil, err
	}
	return &PlanColumn{Table: table, Column: column, Alias: projection.Alias}, nil
}

func (p *planner) planAggregate(aggregate Aggregate) (*PlanAggregate, error) {
	if aggregate.Property == nil {
		return &PlanAggregate{Function: aggregate.Function, Star: true}, nil
	}
	table, column, err := p.resolveProperty(*aggregate.Property)
	if err != nil {
		return nil, err
	}
	return &PlanAggregate{
		Function: aggregate.Function,
		Distinct: aggregate.Distinct,
		Table:    table,
		Column:   column,
	}, nil
}

// planGrouping derives GROUP BY from what is returned, which is where Cypher
// puts it: aggregating anything groups by everything else returned. Deriving
// it rather than accepting one is also what stops a statement from grouping by
// something the caller never sees.
func (p *planner) planGrouping() {
	aggregates := 0
	for _, column := range p.plan.Select {
		if column.Aggregate != nil {
			aggregates++
		}
	}
	// Either nothing is aggregated, or everything is and the result is one
	// row. Neither needs a GROUP BY.
	if aggregates == 0 || aggregates == len(p.plan.Select) {
		return
	}
	for _, column := range p.plan.Select {
		if column.Aggregate == nil {
			p.plan.GroupBy = append(p.plan.GroupBy, column)
		}
	}
}

func projectionPosition(projection Projection) Position {
	if projection.Aggregate != nil {
		return projection.Aggregate.Pos
	}
	return projection.Property.Pos
}

func (p *planner) planOrderBy(keys []SortKey) error {
	for _, key := range keys {
		order, err := p.planSortKey(key)
		if err != nil {
			return err
		}
		order.Descending = key.Descending
		p.plan.OrderBy = append(p.plan.OrderBy, *order)
	}
	return nil
}

func (p *planner) planSortKey(key SortKey) (*PlanOrder, error) {
	switch {
	case key.Aggregate != nil:
		// Sorting by an aggregate over a projection that has none would group
		// the whole result into one row on the way to ordering it, quietly
		// answering a different question than the one asked.
		if !p.aggregating() {
			return nil, planErrorf(key.Pos,
				"sorting by %s needs the query to return an aggregate too; add it to RETURN",
				key.Aggregate)
		}
		aggregate, err := p.planAggregate(*key.Aggregate)
		if err != nil {
			return nil, err
		}
		return &PlanOrder{Aggregate: aggregate}, nil

	case key.Alias != "":
		// An empty name never reaches here: the analyzer refuses one.
		// A bare name in ORDER BY is a returned column. It is the only way to
		// sort a grouped result by something the query already computed, and
		// checking it here keeps an unknown name from reaching the database.
		for _, column := range p.plan.Select {
			if column.Alias == key.Alias {
				return &PlanOrder{Alias: key.Alias}, nil
			}
		}
		return nil, planErrorf(key.Pos,
			"%q is not returned by this query, so there is nothing to sort by", key.Alias)

	case key.Property != nil:
		table, column, err := p.resolveProperty(*key.Property)
		if err != nil {
			return nil, err
		}
		// DISTINCT and aggregation both collapse rows before they are
		// ordered, so sorting by a value that survived neither has no defined
		// answer, and both MySQL and PostgreSQL refuse the statement. The
		// test is on aggregating rather than on GROUP BY, because a query
		// that aggregates every column derives no GROUP BY and collapses just
		// as hard.
		if (p.plan.Distinct || p.aggregating()) && !p.isProjected(table, column) {
			collapsed := "DISTINCT"
			if p.aggregating() {
				collapsed = "an aggregate"
			}
			return nil, planErrorf(key.Property.Pos,
				"%s is not returned, and a query with %s can only be sorted by a returned value; add it to RETURN",
				key.Property, collapsed)
		}
		return &PlanOrder{Table: table, Column: column}, nil

	default:
		// The three forms above are the whole set the analyzer produces.
		// Saying so here means a fourth one added later fails as an error
		// rather than as a nil dereference in whichever branch it fell into.
		return nil, planErrorf(key.Pos, "this sort key names nothing to sort by")
	}
}

// aggregating reports whether the projection collapses rows. A GROUP BY is not
// the test: aggregating every column derives no GROUP BY and still collapses
// the result to a single row.
func (p *planner) aggregating() bool {
	for _, column := range p.plan.Select {
		if column.Aggregate != nil {
			return true
		}
	}
	return false
}

func (p *planner) isProjected(table int, column string) bool {
	for _, projected := range p.plan.Select {
		if projected.Aggregate == nil && projected.Table == table && projected.Column == column {
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
