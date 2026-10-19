// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// A query with a WITH is read in one of two shapes:
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
// the first stage's. Anything longer is refused.

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
	if len(segments) > 1 {
		return nil, unsupportedf(segments[1].with, "more than one WITH",
			"a query may have one WITH; to compare two sets, share a node in one MATCH, "+
				"as in (p1)-[:R]->(m)<-[:R]-(p2)")
	}
	segment := segments[0]
	if len(segment.reading) == 0 {
		return nil, unsupportedf(segment.with, "a WITH before any MATCH", "start the query with MATCH")
	}

	after := final.AllOC_ReadingClause()
	if len(after) == 0 {
		query, err := analyzeReading(segment.reading, segment.with, nameScope{})
		if err != nil {
			return nil, err
		}
		if err := analyzeWith(query, segment.with, returning.OC_ProjectionBody()); err != nil {
			return nil, err
		}
		// What the WITH's WHERE added is a condition like any other, so it
		// may not require an optional node either.
		if err := refuseRequiredUseOfOptional(query); err != nil {
			return nil, err
		}
		return query, nil
	}

	first, err := analyzeReading(segment.reading, segment.with, nameScope{})
	if err != nil {
		return nil, err
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
	if err := analyzeProjectionBody(query, returning.OC_ProjectionBody(), names); err != nil {
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
// stage before it, as one value per row or as one collected list.
type nameBinding struct {
	sub      *Query
	set      bool
	distinct bool
	used     bool
	// equal is the property the name was compared equal to, which RETURN
	// reads when it names the WITH's value.
	equal *PropertyRef
}

type nameScope map[string]*nameBinding

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
		for _, condition := range edge.Conditions {
			if _, err := s.resolveTerm(condition, false); err != nil {
				return err
			}
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
		return s.semiJoin(*node.Right.Name, node.Left, node.Operator == "=", false, top, node.Pos)
	case Membership:
		if node.ListName == nil {
			return node, nil
		}
		if node.Negated {
			return nil, unsupportedAt(node.ListName.Pos, "NOT IN over a WITH's values",
				"only membership, as in n.property IN %s, can filter by them", node.ListName.Name)
		}
		return s.semiJoin(*node.ListName, node.Property, false, true, top, node.Pos)
	case Negation:
		if membership, ok := node.Operand.(Membership); ok && membership.ListName != nil {
			if _, known := s[membership.ListName.Name]; known {
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
		operands := make([]Predicate, len(node.Operands))
		for i, operand := range node.Operands {
			resolved, err := s.resolveTerm(operand, false)
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
	case !known:
		return nil, unsupportedAt(name.Pos, "referring to a node as a value", "use %s.property", name.Name)
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
	if equality && binding.equal == nil {
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

// stages lists the earlier stages a query's semi-joins read.
func (q *Query) stages() []*Query {
	var stages []*Query
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
