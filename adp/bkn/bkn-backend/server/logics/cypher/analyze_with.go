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

// A query with one WITH, as in
//
//	MATCH (g:goals)-[:R]->(t:teams)
//	WITH t, count(*) AS n WHERE n > 200
//	RETURN t.team_name AS team, n ORDER BY n DESC
//
// is read as one query: the WITH's grouping becomes the query's grouping, the
// parts of its WHERE that test aggregates become a condition on each group,
// and RETURN is read in terms of what the WITH named. Nothing runs in stages,
// so both executors answer it the way they answer any aggregation.

// withItem is one thing a WITH passes on: a node of the pattern, or a value
// it names.
type withItem struct {
	node       string      // the pattern variable, for a node passed on
	projection *Projection // the named value otherwise
	aggregates bool
	pos        Position
}

type withScope map[string]withItem

func analyzeWith(query *Query, with parsing.IOC_WithContext, returning parsing.IOC_ProjectionBodyContext) error {
	body := with.OC_ProjectionBody()
	if body.DISTINCT() != nil {
		return unsupportedf(body, "WITH DISTINCT", "write DISTINCT on RETURN, or count distinct values with count(DISTINCT ...)")
	}
	if body.OC_Order() != nil || body.OC_Skip() != nil || body.OC_Limit() != nil {
		return unsupportedf(body, "ORDER BY, SKIP or LIMIT on WITH", "write them on RETURN")
	}
	items := body.OC_ProjectionItems()
	if strings.HasPrefix(strings.TrimSpace(items.GetText()), "*") {
		return unsupportedf(items, "WITH *", "name what the WITH passes on")
	}

	nodes := map[string]bool{}
	for _, node := range query.Pattern.Nodes {
		if node.Variable != "" && !node.Anonymous {
			nodes[node.Variable] = true
		}
	}

	scope := withScope{}
	for _, item := range items.AllOC_ProjectionItem() {
		expression := item.OC_Expression()
		alias := ""
		if variable := item.OC_Variable(); variable != nil {
			name, err := namedIdentifier(variable, "column name")
			if err != nil {
				return err
			}
			alias = name
		}
		entry := withItem{pos: positionOf(expression)}
		if name, ok := bareVariable(expression); ok {
			if !nodes[name] {
				return unsupportedf(expression, "WITH of "+name,
					"%q is not a node of the MATCH pattern; WITH passes on nodes and named values", name)
			}
			entry.node = name
			if alias == "" {
				alias = name
			}
		} else {
			if alias == "" {
				return unsupportedf(expression, "an unnamed value in WITH",
					"name it with AS, as in WITH %s AS name", sourceText(expression))
			}
			projection, err := analyzeProjection(expression)
			if err != nil {
				return err
			}
			projection.Alias = alias
			entry.projection = projection
			entry.aggregates = projectionAggregates(*projection)
		}
		if _, taken := scope[alias]; taken {
			return unsupportedf(item, "a name WITH uses twice", "%q is named twice; give each value its own name", alias)
		}
		scope[alias] = entry
	}

	for _, entry := range scope {
		if entry.aggregates {
			query.Grouped = true
		}
	}
	if query.Grouped {
		for _, entry := range scope {
			switch {
			case entry.aggregates:
			case entry.node != "":
				query.GroupKeys = append(query.GroupKeys, GroupKey{Node: entry.node, Pos: entry.pos})
			case entry.projection.Property != nil:
				query.GroupKeys = append(query.GroupKeys, GroupKey{Property: entry.projection.Property, Pos: entry.pos})
			default:
				return unsupportedAt(entry.pos, "grouping by a computed value in WITH",
					"group by properties or nodes, and compute in RETURN")
			}
		}
	}

	if where := with.OC_Where(); where != nil {
		condition, err := scope.predicate(where.OC_Expression())
		if err != nil {
			return err
		}
		var before, after []Predicate
		for _, term := range conjunctsOf(condition) {
			if predicateAggregates(term) {
				after = append(after, term)
			} else {
				before = append(before, term)
			}
		}
		// A condition on what the WITH grouped by holds for every row of a
		// group or for none, so it can filter the rows before grouping.
		if len(before) > 0 {
			query.Where = combine("AND", append(conjunctsOf(query.Where), before...), positionOf(where))
		}
		if len(after) > 0 {
			query.Having = combine("AND", after, positionOf(where))
		}
	}
	return scope.analyzeReturn(query, returning)
}

func projectionAggregates(projection Projection) bool {
	return projection.Aggregate != nil || (projection.Expression != nil && containsAggregate(projection.Expression))
}

// analyzeReturn reads RETURN after a WITH: it lists what the WITH named, or
// properties of the nodes it passed on.
func (w withScope) analyzeReturn(query *Query, body parsing.IOC_ProjectionBodyContext) error {
	query.Distinct = body.DISTINCT() != nil
	items := body.OC_ProjectionItems()
	if strings.HasPrefix(strings.TrimSpace(items.GetText()), "*") {
		return unsupportedf(items, "RETURN * after WITH", "list what to return")
	}
	for _, item := range items.AllOC_ProjectionItem() {
		expression := item.OC_Expression()
		projection, err := w.returned(expression, query.Grouped)
		if err != nil {
			return err
		}
		if variable := item.OC_Variable(); variable != nil {
			alias, err := namedIdentifier(variable, "column name")
			if err != nil {
				return err
			}
			projection.Alias = alias
		}
		query.Return = append(query.Return, *projection)
	}

	returned := map[string]bool{}
	for _, projection := range query.Return {
		returned[projection.Alias] = true
	}
	if order := body.OC_Order(); order != nil {
		for _, sortItem := range order.AllOC_SortItem() {
			key, err := w.sortKey(sortItem.OC_Expression(), returned)
			if err != nil {
				return err
			}
			key.Descending = sortItem.DESCENDING() != nil || sortItem.DESC() != nil
			query.OrderBy = append(query.OrderBy, *key)
		}
	}
	if skip := body.OC_Skip(); skip != nil {
		value, err := analyzeRowCount(skip.OC_Expression(), "SKIP")
		if err != nil {
			return err
		}
		query.Skip = &value
	}
	if limit := body.OC_Limit(); limit != nil {
		value, err := analyzeRowCount(limit.OC_Expression(), "LIMIT")
		if err != nil {
			return err
		}
		query.Limit = &value
	}
	return nil
}

// returned reads one RETURN item after a WITH.
func (w withScope) returned(expression parsing.IOC_ExpressionContext, grouped bool) (*Projection, error) {
	if name, ok := bareVariable(expression); ok {
		entry, known := w[name]
		if !known {
			return nil, w.notPassedOn(expression, name)
		}
		if entry.node != "" {
			return nil, unsupportedf(expression, "returning a whole node",
				"return its properties, as in RETURN %s.property", name)
		}
		projection := *entry.projection
		projection.Alias = name
		return &projection, nil
	}
	if property, ok, err := w.nodeProperty(expression); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &Projection{Property: property, Alias: sourceText(expression), Pos: property.Pos}, nil
	}
	if aggregate, ok, err := analyzeAggregate(expression); err == nil && ok && aggregate.Property == nil &&
		aggregate.Argument == nil && aggregate.Variable == "" && !grouped {
		// count(*) over the rows a WITH passed on, unchanged in number, is
		// count(*) over the match.
		return &Projection{Aggregate: aggregate, Alias: aggregate.String(), Pos: aggregate.Pos}, nil
	}
	return nil, unsupportedf(expression, "this RETURN after WITH",
		"after WITH, RETURN lists what the WITH named, or properties of the nodes it passed on")
}

// nodeProperty reads n.property where n is a node the WITH passed on, under
// whatever name it passed it as, and refers to the node of the pattern.
func (w withScope) nodeProperty(expression parsing.IOC_ExpressionContext) (*PropertyRef, bool, error) {
	term, single := singleExpressionAtom(expression)
	if !single || len(term.propertyLookups) != 1 || term.atom.OC_Variable() == nil {
		return nil, false, nil
	}
	name := identifier(term.atom.OC_Variable().GetText())
	property := identifier(term.propertyLookups[0].OC_PropertyKeyName().GetText())
	entry, known := w[name]
	if !known {
		return nil, true, w.notPassedOn(expression, name)
	}
	if entry.node == "" {
		return nil, true, unsupportedf(expression, "a property of a value",
			"%q is a value the WITH named, not a node", name)
	}
	return &PropertyRef{Variable: entry.node, Property: property, Pos: positionOf(expression)}, true, nil
}

func (w withScope) notPassedOn(at antlr.ParserRuleContext, name string) error {
	return unsupportedf(at, "a variable WITH did not pass on",
		"%q is not visible after the WITH; list it in the WITH to use it later", name)
}

// sortKey reads an ORDER BY item after a WITH: a returned column, a value the
// WITH named, or a property of a node it passed on.
func (w withScope) sortKey(expression parsing.IOC_ExpressionContext, returned map[string]bool) (*SortKey, error) {
	if name, ok := bareVariable(expression); ok {
		if returned[name] {
			return &SortKey{Alias: name, Pos: positionOf(expression)}, nil
		}
		entry, known := w[name]
		switch {
		case !known:
			return nil, w.notPassedOn(expression, name)
		case entry.node != "":
			return nil, unsupportedf(expression, "sorting by a whole node", "sort by one of its properties")
		case entry.projection.Property != nil:
			return &SortKey{Property: entry.projection.Property, Pos: positionOf(expression)}, nil
		case entry.projection.Aggregate != nil:
			return &SortKey{Aggregate: entry.projection.Aggregate, Pos: positionOf(expression)}, nil
		default:
			return &SortKey{Expression: entry.projection.Expression, Pos: positionOf(expression)}, nil
		}
	}
	if property, ok, err := w.nodeProperty(expression); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &SortKey{Property: property, Pos: property.Pos}, nil
	}
	return nil, unsupportedf(expression, "this ORDER BY after WITH",
		"sort by a returned column, a value the WITH named, or a property of a node it passed on")
}

// predicate reads the WITH's WHERE, whose names are what the WITH passed on.
func (w withScope) predicate(ctx parsing.IOC_ExpressionContext) (Predicate, error) {
	or := ctx.OC_OrExpression()
	operands := make([]Predicate, 0, len(or.AllOC_XorExpression()))
	for _, xor := range or.AllOC_XorExpression() {
		if len(xor.AllOC_AndExpression()) > 1 {
			return nil, unsupportedf(xor, "XOR", "write it with AND, OR and NOT")
		}
		and := xor.AllOC_AndExpression()[0]
		terms := make([]Predicate, 0, len(and.AllOC_NotExpression()))
		for _, not := range and.AllOC_NotExpression() {
			term, err := w.comparison(not.OC_ComparisonExpression())
			if err != nil {
				return nil, err
			}
			if len(not.AllNOT())%2 == 1 {
				term = Negation{Operand: term, Pos: positionOf(not)}
			}
			terms = append(terms, term)
		}
		operands = append(operands, combine("AND", terms, positionOf(and)))
	}
	return combine("OR", operands, positionOf(or)), nil
}

func (w withScope) comparison(ctx parsing.IOC_ComparisonExpressionContext) (Predicate, error) {
	partials := ctx.AllOC_PartialComparisonExpression()
	left := ctx.OC_StringListNullPredicateExpression()
	if len(left.AllOC_StringPredicateExpression())+len(left.AllOC_ListPredicateExpression()) > 0 {
		return nil, unsupportedf(ctx, "IN, STARTS WITH, ENDS WITH and CONTAINS in the WHERE of a WITH",
			"write such conditions on the MATCH")
	}
	nulls := left.AllOC_NullPredicateExpression()
	switch {
	case len(partials) == 0 && len(nulls) == 0:
		if inner, ok := parenthesizedExpression(left.OC_AddOrSubtractExpression()); ok {
			return w.predicate(inner)
		}
		return nil, unsupportedf(ctx, "a non-comparison predicate", "compare values, as in n > 10")
	case len(partials) == 0:
		value, err := w.value(left.OC_AddOrSubtractExpression())
		if err != nil {
			return nil, err
		}
		property, ok := value.(PropertyRef)
		if !ok {
			return nil, unsupportedf(ctx, "IS NULL over an aggregate", "test a property, as in n.property IS NULL")
		}
		return NullCheck{Property: property, Negated: nulls[0].NOT() != nil, Pos: positionOf(ctx)}, nil
	case len(partials) > 1:
		return nil, unsupportedf(ctx, "chained comparisons", "write a < b AND b < c instead")
	case len(nulls) > 0:
		return nil, unsupportedf(ctx, "comparing a condition", "compare values")
	}
	right := partials[0].OC_StringListNullPredicateExpression()
	if len(right.AllOC_StringPredicateExpression())+len(right.AllOC_ListPredicateExpression())+
		len(right.AllOC_NullPredicateExpression()) > 0 {
		return nil, unsupportedf(ctx, "comparing a condition", "compare values")
	}
	leftValue, err := w.value(left.OC_AddOrSubtractExpression())
	if err != nil {
		return nil, err
	}
	rightValue, err := w.value(right.OC_AddOrSubtractExpression())
	if err != nil {
		return nil, err
	}
	operator := comparisonOperator(partials[0])
	if _, isProperty := leftValue.(PropertyRef); !isProperty {
		if _, rightIsProperty := rightValue.(PropertyRef); rightIsProperty {
			leftValue, rightValue, operator = rightValue, leftValue, mirroredOperators[operator]
		}
	}
	if literal, ok := rightValue.(Literal); ok && literal.Kind == LiteralNull {
		return nil, unsupportedf(ctx, "comparing against null", "write IS NULL or IS NOT NULL instead")
	}
	if property, ok := leftValue.(PropertyRef); ok {
		switch value := rightValue.(type) {
		case Literal:
			return Comparison{Left: property, Operator: operator, Right: Operand{Literal: &value}, Pos: positionOf(ctx)}, nil
		case ParameterRef:
			return Comparison{Left: property, Operator: operator, Right: Operand{Parameter: &value}, Pos: positionOf(ctx)}, nil
		case PropertyRef:
			return PropertyComparison{Left: property, Operator: operator, Right: value, Pos: positionOf(ctx)}, nil
		}
	}
	if !containsAggregate(leftValue) && !containsAggregate(rightValue) {
		return nil, unsupportedf(ctx, "this comparison", "compare a value the WITH named with a value")
	}
	return ExpressionComparison{Left: leftValue, Operator: operator, Right: rightValue, Pos: positionOf(ctx)}, nil
}

// value reads one side of a comparison in the WITH's WHERE: a name the WITH
// passed on, a property of a node it passed on, a literal or a parameter.
func (w withScope) value(ctx parsing.IOC_AddOrSubtractExpressionContext) (Expression, error) {
	if computedSum(ctx) {
		return nil, unsupportedf(ctx, "arithmetic in the WHERE of a WITH",
			"name the computed value in the WITH, as in WITH sum(x) * 2 AS doubled")
	}
	atom := ctx.AllOC_MultiplyDivideModuloExpression()[0].AllOC_PowerOfExpression()[0].
		AllOC_UnaryAddOrSubtractExpression()[0].OC_ListOperatorExpression().OC_PropertyOrLabelsExpression()
	lookups := atom.AllOC_PropertyLookup()
	if variable := atom.OC_Atom().OC_Variable(); variable != nil {
		name := identifier(variable.GetText())
		entry, known := w[name]
		if !known {
			return nil, w.notPassedOn(ctx, name)
		}
		switch {
		case len(lookups) == 1 && entry.node != "":
			property := identifier(lookups[0].OC_PropertyKeyName().GetText())
			return PropertyRef{Variable: entry.node, Property: property, Pos: positionOf(ctx)}, nil
		case len(lookups) > 0:
			return nil, unsupportedf(ctx, "a property of a value", "%q is a value the WITH named, not a node", name)
		case entry.node != "":
			return nil, unsupportedf(ctx, "comparing a whole node", "compare one of its properties")
		case entry.projection.Property != nil:
			return *entry.projection.Property, nil
		case entry.projection.Aggregate != nil:
			return *entry.projection.Aggregate, nil
		case entry.projection.Expression != nil:
			if _, condition := entry.projection.Expression.(ConditionValue); condition {
				return nil, unsupportedf(ctx, "comparing a condition", "test the condition itself in WHERE")
			}
			return entry.projection.Expression, nil
		}
	}
	operand, err := analyzeAddOrSubtractOperand(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case operand.literal != nil:
		return *operand.literal, nil
	case operand.parameter != nil:
		return *operand.parameter, nil
	default:
		return nil, unsupportedf(ctx, "this value in the WHERE of a WITH",
			"use a name the WITH passed on, a literal or a parameter")
	}
}

// predicateAggregates reports whether a condition tests an aggregate, which
// only a group can answer.
func predicateAggregates(predicate Predicate) bool {
	switch node := predicate.(type) {
	case ExpressionComparison:
		return containsAggregate(node.Left) || containsAggregate(node.Right)
	case Negation:
		return predicateAggregates(node.Operand)
	case LogicalOperator:
		for _, operand := range node.Operands {
			if predicateAggregates(operand) {
				return true
			}
		}
	}
	return false
}

// conjunctsOf splits a condition at its top-level ANDs.
func conjunctsOf(predicate Predicate) []Predicate {
	switch node := predicate.(type) {
	case nil:
		return nil
	case LogicalOperator:
		if node.Operator == "AND" {
			return node.Operands
		}
	}
	return []Predicate{predicate}
}
