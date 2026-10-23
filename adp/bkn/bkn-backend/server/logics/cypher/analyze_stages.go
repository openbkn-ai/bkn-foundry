// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// A query with a WITH is read in one of four shapes:
//
//	MATCH ... WITH <grouping and aggregates> [WHERE ...] RETURN ...
//
// which is one query that filters after aggregating (see analyzeWith), and
//
//	MATCH ... WITH DISTINCT x.p AS v | collect(x.p) AS s
//	MATCH ... WHERE y.q = v | y.q IN s
//	RETURN ...
//
// where the first stage only supplies values the second filters by, which is
// a semi-join: each row of the second stage is kept once if its value is among
// the first stage's. In the first shape, more WITHs may follow the first one
// (see analyzeWiths); a MATCH may follow only the first WITH. The third shape
// collects a list in each of two stages and returns sizes of them and of what
// they share (see analyzeIntersection). The fourth passes nodes on to the
// MATCH after the WITH, with values computed for them (see analyzeCarried).
//
// Each shape may open with a WITH that names values written in the query,
// as in WITH ['a', 'b'] AS codes MATCH ... WHERE n.code IN codes: each name
// stands for its value in the conditions of the MATCH that follows.

type stageSegment struct {
	reading []parsing.IOC_ReadingClauseContext
	with    parsing.IOC_WithContext
}

func analyzeMultiPart(multi parsing.IOC_MultiPartQueryContext) (*Query, error) {
	var segments []stageSegment
	current := stageSegment{}
	for _, child := range multi.GetChildren() {
		switch node := child.(type) {
		case parsing.IOC_ReadingClauseContext:
			if node.OC_Unwind() != nil {
				// Refused before the shape of the stages is, which would
				// otherwise speak first and point elsewhere.
				return nil, unwindError(node)
			}
			current.reading = append(current.reading, node)
		case parsing.IOC_UpdatingClauseContext:
			return nil, unsupportedf(node, "writing", "this interface only runs read-only queries")
		case parsing.IOC_WithContext:
			current.with = node
			segments = append(segments, current)
			current = stageSegment{}
		}
	}
	final := multi.OC_SinglePartQuery()
	returning, err := readOnlyReturn(final)
	if err != nil {
		return nil, err
	}
	after := final.AllOC_ReadingClause()
	for _, clause := range after {
		if clause.OC_Unwind() != nil {
			return nil, unwindError(clause)
		}
	}
	constants := nameScope{}
	if len(segments[0].reading) == 0 {
		if constants, err = analyzeConstants(segments[0].with); err != nil {
			return nil, err
		}
		segments = segments[1:]
	}
	if len(segments) == 0 {
		query, err := analyzeReading(after, final, constants)
		if err != nil {
			return nil, err
		}
		if err := analyzeProjectionBody(query, returning.OC_ProjectionBody(), nil, nil); err != nil {
			return nil, err
		}
		return query, nil
	}
	segment := segments[0]
	if len(segment.reading) == 0 {
		return nil, unsupportedf(segment.with, "a WITH before any MATCH", "start the query with MATCH")
	}
	if len(segments) == 2 && len(segments[1].reading) > 0 && len(after) == 0 {
		if query, ok, err := analyzeIntersection(segments[0], segments[1], returning, constants); ok {
			return query, err
		}
	}
	// A stage after the second one would read values of a stage that is
	// itself filtered by another, which nothing here writes as one statement.
	for _, later := range segments[1:] {
		if len(later.reading) > 0 {
			return nil, unsupportedf(later.with, "a WITH after the MATCH that follows a WITH",
				"end the query with RETURN there; to compare two sets, share a node in one MATCH, "+
					"as in (p1)-[:R]->(m)<-[:R]-(p2)")
		}
	}
	if len(segments) > 1 && len(after) > 0 {
		return nil, unsupportedf(after[0], "MATCH after a second WITH",
			"MATCH may follow only the first WITH; to compare two sets, share a node in one MATCH, "+
				"as in (p1)-[:R]->(m)<-[:R]-(p2)")
	}

	if len(after) == 0 {
		query, err := analyzeReading(segment.reading, segment.with, constants)
		if err != nil {
			return nil, err
		}
		withs := make([]parsing.IOC_WithContext, len(segments))
		for i, each := range segments {
			withs[i] = each.with
		}
		if err := analyzeWiths(query, withs, returning.OC_ProjectionBody()); err != nil {
			return nil, err
		}
		// What a WITH's WHERE added is a condition like any other, so it
		// may not require an optional node either.
		if err := refuseRequiredUseOfOptional(query); err != nil {
			return nil, err
		}
		return query, nil
	}

	first, err := analyzeReading(segment.reading, segment.with, constants)
	if err != nil {
		return nil, err
	}
	if carriesNodes(segment.with, first) {
		return analyzeCarried(first, segment.with, after, final, returning, constants)
	}
	name, binding, err := analyzeStageWith(segment.with, first)
	if err != nil {
		return nil, err
	}
	scope := nameScope{name: binding}
	query, err := analyzeReading(after, final, scope)
	if err != nil {
		return nil, err
	}
	if !binding.used {
		return nil, unsupportedf(segment.with, "a WITH whose value the next MATCH does not use",
			"filter the next MATCH by it, as in WHERE n.property = %s or WHERE n.property IN %s", name, name)
	}
	var names map[string]PropertyRef
	if binding.equal != nil {
		names = map[string]PropertyRef{name: *binding.equal}
	}
	if err := analyzeProjectionBody(query, returning.OC_ProjectionBody(), names, nil); err != nil {
		return nil, err
	}
	if !binding.set && !binding.distinct {
		// Without DISTINCT the first stage repeats a value as often as it
		// matched it, and each repeat would repeat the rows it finds. That
		// is invisible only when RETURN removes duplicates anyway.
		aggregates := false
		for _, projection := range query.Return {
			aggregates = aggregates || projectionAggregates(projection)
		}
		if !query.Distinct || aggregates {
			return nil, unsupportedf(segment.with, "a WITH that repeats values before MATCH",
				"write WITH DISTINCT, so each value is looked up once")
		}
	}
	return query, nil
}

// analyzeConstants reads a WITH that opens the query, before any MATCH. It may
// only name values written in the query -- a literal, a list of literals or a
// parameter -- which the conditions after it use by name.
func analyzeConstants(with parsing.IOC_WithContext) (nameScope, error) {
	body := with.OC_ProjectionBody()
	shape := func(at antlr.ParserRuleContext) error {
		return unsupportedf(at, "a WITH before any MATCH",
			"start the query with MATCH, or open it with a WITH that only names values, as in WITH ['a', 'b'] AS codes")
	}
	if err := refuseWithModifiers(body); err != nil {
		return nil, err
	}
	if with.OC_Where() != nil {
		return nil, shape(with.OC_Where())
	}
	scope := nameScope{}
	for _, item := range body.OC_ProjectionItems().AllOC_ProjectionItem() {
		expression := item.OC_Expression()
		name, err := withAlias(item)
		if err != nil {
			return nil, err
		}
		if name == "" {
			return nil, unnamedWithValue(expression)
		}
		sum, err := valueChain(expression)
		if err != nil || computedSum(sum) {
			return nil, shape(expression)
		}
		constant := &constantValue{}
		if term, single := singleExpressionAtom(expression); single && term.atom.OC_Literal() != nil &&
			term.atom.OC_Literal().OC_ListLiteral() != nil {
			values, _, err := analyzeListOperands(sum)
			if err != nil {
				return nil, err
			}
			constant.list, constant.values = true, values
		} else {
			value, err := analyzeAddOrSubtractOperand(sum)
			if err != nil || (value.literal == nil && value.parameter == nil) {
				return nil, shape(expression)
			}
			if value.literal != nil && value.literal.Kind == LiteralNull {
				return nil, unsupportedf(expression, "naming null", "a null matches nothing; leave the condition out")
			}
			constant.value = *value.operand()
		}
		if _, taken := scope[name]; taken {
			return nil, unsupportedf(item, "a name WITH uses twice", "%q is named twice; give each value its own name", name)
		}
		scope[name] = &nameBinding{constant: constant}
	}
	return scope, nil
}

// analyzeStageWith reads a WITH that passes one property's values to the next
// stage: WITH [DISTINCT] x.p AS v, or WITH collect([DISTINCT] x.p) AS s.
func analyzeStageWith(with parsing.IOC_WithContext, stage *Query) (string, *nameBinding, error) {
	body := with.OC_ProjectionBody()
	items := body.OC_ProjectionItems()
	all := items.AllOC_ProjectionItem()
	shape := func(at parsing.IOC_ProjectionItemsContext) error {
		return unsupportedf(at, "this WITH before MATCH",
			"pass one property to the next MATCH, as in WITH DISTINCT n.code AS code, "+
				"or a collected list, as in WITH collect(n.code) AS codes")
	}
	if strings.HasPrefix(strings.TrimSpace(items.GetText()), "*") || len(all) != 1 {
		return "", nil, shape(items)
	}
	if with.OC_Where() != nil || body.OC_Order() != nil || body.OC_Skip() != nil || body.OC_Limit() != nil {
		return "", nil, unsupportedf(with, "WHERE, ORDER BY, SKIP or LIMIT on a WITH before MATCH",
			"write the condition on the first MATCH")
	}
	item := all[0]
	if item.OC_Variable() == nil {
		return "", nil, shape(items)
	}
	name, err := namedIdentifier(item.OC_Variable(), "column name")
	if err != nil {
		return "", nil, err
	}

	binding := &nameBinding{distinct: body.DISTINCT() != nil}
	expression := item.OC_Expression()
	argument := expression
	if term, single := singleExpressionAtom(expression); single && len(term.propertyLookups) == 0 {
		if invocation := term.atom.OC_FunctionInvocation(); invocation != nil {
			if !strings.EqualFold(identifier(invocation.OC_FunctionName().GetText()), "collect") ||
				len(invocation.AllOC_Expression()) != 1 {
				return "", nil, shape(items)
			}
			binding.set = true
			argument = invocation.AllOC_Expression()[0]
		}
	}
	property, err := analyzePropertyRef(argument)
	if err != nil {
		return "", nil, shape(items)
	}
	binding.sub = &Query{
		Pattern:  stage.Pattern,
		Where:    stage.Where,
		Return:   []Projection{{Property: property, Alias: "value", Pos: property.Pos}},
		Distinct: true,
	}
	return name, binding, nil
}

// nameBinding is what a WITH gave a name: the values of one property from the
// stage before it, as one value per row or as one collected list, or a value
// written in the query.
type nameBinding struct {
	constant *constantValue
	sub      *Query
	set      bool
	distinct bool
	used     bool
	// label is set for a node a carried stage passed on, which the next
	// MATCH may write without its label; carried for a value it named.
	label   string
	carried bool
	// equal is the property the name was compared equal to, which RETURN
	// reads when it names the WITH's value.
	equal *PropertyRef
}

type nameScope map[string]*nameBinding

// constantValue is a value an opening WITH named: a list of values, or one
// literal or parameter. A parameter may stand for either, so it is read as a
// list where IN uses it and as one value where a comparison does.
type constantValue struct {
	list   bool
	values []Operand
	value  Operand
}

// resolve replaces each use of a name in the query's conditions with a
// semi-join. A use may only stand on its own at the top of WHERE: under OR or
// NOT it would say something a semi-join cannot. A name nothing introduced is
// refused the way a bare variable always was.
func (s nameScope) resolve(query *Query) error {
	terms := conjunctsOf(query.Where)
	for i, term := range terms {
		resolved, err := s.resolveTerm(term, true)
		if err != nil {
			return err
		}
		terms[i] = resolved
	}
	if len(terms) > 0 {
		query.Where = combine("AND", terms, query.Where.predicatePosition())
	}
	for _, edge := range query.Pattern.Edges {
		for i, condition := range edge.Conditions {
			resolved, err := s.resolveTerm(condition, false)
			if err != nil {
				return err
			}
			edge.Conditions[i] = resolved
		}
	}
	return nil
}

func (s nameScope) resolveTerm(predicate Predicate, top bool) (Predicate, error) {
	switch node := predicate.(type) {
	case Comparison:
		if node.Right.Name == nil {
			return node, nil
		}
		if binding, known := s[node.Right.Name.Name]; known && binding.constant != nil {
			if binding.constant.list {
				return nil, unsupportedAt(node.Right.Name.Pos, "comparing with a list",
					"test membership instead, as in n.property IN %s", node.Right.Name.Name)
			}
			node.Right = binding.constant.value
			return node, nil
		}
		return s.semiJoin(*node.Right.Name, node.Left, node.Operator == "=", false, top, node.Pos)
	case Membership:
		if node.ListName == nil {
			return node, nil
		}
		if binding, known := s[node.ListName.Name]; known && binding.constant != nil {
			constant := binding.constant
			switch {
			case constant.list:
				node.Values = constant.values
			case constant.value.Parameter != nil:
				node.ListParameter = constant.value.Parameter
			default:
				return nil, unsupportedAt(node.ListName.Pos, "IN over a single value",
					"compare with it instead, as in n.property = %s", node.ListName.Name)
			}
			node.ListName = nil
			return node, nil
		}
		if node.Negated {
			return nil, unsupportedAt(node.ListName.Pos, "NOT IN over a WITH's values",
				"only membership, as in n.property IN %s, can filter by them", node.ListName.Name)
		}
		return s.semiJoin(*node.ListName, node.Property, false, true, top, node.Pos)
	case Negation:
		if membership, ok := node.Operand.(Membership); ok && membership.ListName != nil {
			if binding, known := s[membership.ListName.Name]; known && binding.constant == nil {
				return nil, unsupportedAt(membership.ListName.Pos, "NOT IN over a WITH's values",
					"only membership, as in n.property IN %s, can filter by them", membership.ListName.Name)
			}
		}
		operand, err := s.resolveTerm(node.Operand, false)
		if err != nil {
			return nil, err
		}
		node.Operand = operand
		return node, nil
	case LogicalOperator:
		// An AND nested in a top-level AND is still a conjunction of the
		// whole WHERE, as when inline properties and WHERE combine.
		nested := top && node.Operator == "AND"
		operands := make([]Predicate, len(node.Operands))
		for i, operand := range node.Operands {
			resolved, err := s.resolveTerm(operand, nested)
			if err != nil {
				return nil, err
			}
			operands[i] = resolved
		}
		node.Operands = operands
		return node, nil
	default:
		return predicate, nil
	}
}

func (s nameScope) semiJoin(name NameRef, outer PropertyRef, equality, membership, top bool, pos Position) (Predicate, error) {
	binding, known := s[name.Name]
	switch {
	case !known || binding.label != "":
		return nil, unsupportedAt(name.Pos, "referring to a node as a value", "use %s.property", name.Name)
	case binding.carried:
		return nil, unsupportedAt(name.Pos, "a carried value in the next MATCH's conditions",
			"test %s in the WHERE of the WITH that named it, or compute with it in RETURN", name.Name)
	case !top:
		return nil, unsupportedAt(name.Pos, "using a WITH's value under OR or NOT",
			"compare with %s as a condition of its own, joined by AND", name.Name)
	case binding.set && !membership:
		return nil, unsupportedAt(name.Pos, "comparing with a collected list",
			"test membership instead, as in n.property IN %s", name.Name)
	case !binding.set && !equality:
		return nil, unsupportedAt(name.Pos, "this comparison with a WITH's value",
			"only equality, as in n.property = %s, can filter by it", name.Name)
	}
	binding.used = true
	if equality && binding.equal != nil {
		// Every use of one value names the same value: a.x = v AND b.y = v
		// keeps rows where both equal it, so the second use compares with
		// the first rather than looking the set up again on its own.
		return PropertyComparison{Left: outer, Operator: "=", Right: *binding.equal, Pos: pos}, nil
	}
	if equality {
		equal := outer
		binding.equal = &equal
	}
	return SemiJoin{Outer: outer, Sub: binding.sub, Pos: pos}, nil
}

// returnsAlias reports whether RETURN names a column alias.
func returnsAlias(query *Query, alias string) bool {
	for _, projection := range query.Return {
		if projection.Alias == alias {
			return true
		}
	}
	return false
}

// stages lists the earlier stages a query reads: those its semi-joins read,
// and the two an intersection compares.
func (q *Query) stages() []*Query {
	var stages []*Query
	if q.Intersection != nil {
		stages = append(stages, q.Intersection.Lists[:]...)
	}
	if q.Carried != nil {
		stages = append(stages, q.Carried.Stage)
	}
	if q.Union != nil {
		stages = append(stages, q.Union.Parts...)
	}
	var walk func(Predicate)
	walk = func(predicate Predicate) {
		switch node := predicate.(type) {
		case SemiJoin:
			stages = append(stages, node.Sub)
		case LogicalOperator:
			for _, operand := range node.Operands {
				walk(operand)
			}
		case Negation:
			walk(node.Operand)
		}
	}
	walk(q.Where)
	return stages
}
