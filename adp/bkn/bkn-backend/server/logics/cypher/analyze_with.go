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
//
// Further WITHs may follow the first, each computing from what the one before
// it passed on, as in
//
//	WITH m.code AS code, b.usage AS usage, sum(i.qty) AS stock
//	WITH code, stock - usage * 50 AS shortage WHERE shortage < 0
//
// They neither group nor remove duplicates, so they still describe one query:
// each name stands for what it was computed from. RETURN may then aggregate
// the groups the first WITH made, which is the one thing that does not fold
// into that query; see Query.Totals.

// withItem is one thing a WITH passes on: a node of the pattern, or a value
// it names.
type withItem struct {
	node       string      // the pattern variable, for a node passed on
	projection *Projection // the named value otherwise
	// collected is set instead of projection for collect([DISTINCT] n.p),
	// which is read only through size(): the count of what it collected.
	collected  *Aggregate
	aggregates bool
	pos        Position
}

type withScope map[string]withItem

// analyzeWiths reads a chain of WITHs after the query's MATCH clauses, and the
// RETURN that ends it.
func analyzeWiths(query *Query, withs []parsing.IOC_WithContext, returning parsing.IOC_ProjectionBodyContext) error {
	scope, err := analyzeWith(query, withs[0])
	if err != nil {
		return err
	}
	for _, with := range withs[1:] {
		if scope, err = scope.next(with); err != nil {
			return err
		}
		if err := scope.filter(query, with); err != nil {
			return err
		}
	}
	return scope.analyzeReturn(query, returning)
}

// refuseWithModifiers refuses what would make a WITH more than naming: each of
// these changes which rows the next clause sees.
func refuseWithModifiers(body parsing.IOC_ProjectionBodyContext) error {
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
	return nil
}

// withAlias is the name a WITH item is passed on as, or empty when it has none.
func withAlias(item parsing.IOC_ProjectionItemContext) (string, error) {
	variable := item.OC_Variable()
	if variable == nil {
		return "", nil
	}
	return namedIdentifier(variable, "column name")
}

func unnamedWithValue(expression parsing.IOC_ExpressionContext) error {
	return unsupportedf(expression, "an unnamed value in WITH",
		"name it with AS, as in WITH %s AS name", sourceText(expression))
}

// analyzeWith reads the first WITH after the MATCH clauses: what it groups by,
// what it aggregates, and its WHERE.
func analyzeWith(query *Query, with parsing.IOC_WithContext) (withScope, error) {
	body := with.OC_ProjectionBody()
	if err := refuseWithModifiers(body); err != nil {
		return nil, err
	}
	items := body.OC_ProjectionItems()

	nodes := map[string]bool{}
	for _, node := range query.Pattern.Nodes {
		if node.Variable != "" && !node.Anonymous {
			nodes[node.Variable] = true
		}
	}

	scope := withScope{}
	var written []string
	for _, item := range items.AllOC_ProjectionItem() {
		expression := item.OC_Expression()
		alias, err := withAlias(item)
		if err != nil {
			return nil, err
		}
		entry := withItem{pos: positionOf(expression)}
		if name, ok := bareVariable(expression); ok {
			if !nodes[name] {
				return nil, unsupportedf(expression, "WITH of "+name,
					"%q is not a node of the MATCH pattern; WITH passes on nodes and named values", name)
			}
			entry.node = name
			if alias == "" {
				alias = name
			}
		} else if collected, ok, err := analyzeCollectedCount(expression); ok || err != nil {
			if err != nil {
				return nil, err
			}
			if alias == "" {
				return nil, unnamedWithValue(expression)
			}
			entry.collected, entry.aggregates = collected, true
		} else {
			if alias == "" {
				return nil, unnamedWithValue(expression)
			}
			projection, err := analyzeProjection(expression)
			if err != nil {
				return nil, err
			}
			projection.Alias = alias
			entry.projection = projection
			entry.aggregates = projectionAggregates(*projection)
		}
		if _, taken := scope[alias]; taken {
			return nil, unsupportedf(item, "a name WITH uses twice", "%q is named twice; give each value its own name", alias)
		}
		scope[alias] = entry
		// The grouping keys follow the order the WITH wrote them in, so
		// the statement is the same every time it is generated.
		written = append(written, alias)
	}

	for _, entry := range scope {
		if entry.aggregates {
			query.Grouped = true
		}
	}
	if query.Grouped {
		for _, alias := range written {
			entry := scope[alias]
			switch {
			case entry.aggregates:
			case entry.node != "":
				query.GroupKeys = append(query.GroupKeys, GroupKey{Node: entry.node, Pos: entry.pos})
			case entry.projection.Property != nil:
				query.GroupKeys = append(query.GroupKeys, GroupKey{Property: entry.projection.Property, Pos: entry.pos})
			default:
				return nil, unsupportedAt(entry.pos, "grouping by a computed value in WITH",
					"group by properties or nodes, and compute in RETURN")
			}
		}
	}
	if err := scope.filter(query, with); err != nil {
		return nil, err
	}
	return scope, nil
}

// filter adds a WITH's WHERE to the query.
func (w withScope) filter(query *Query, with parsing.IOC_WithContext) error {
	if where := with.OC_Where(); where != nil {
		condition, err := w.predicate(where.OC_Expression())
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
			query.Having = combine("AND", append(conjunctsOf(query.Having), after...), positionOf(where))
		}
	}
	return nil
}

// next reads a WITH that follows another: it passes on what the one before it
// passed on, under the same or new names, and values computed from those. It
// may not aggregate again, since that would group the groups.
func (w withScope) next(with parsing.IOC_WithContext) (withScope, error) {
	body := with.OC_ProjectionBody()
	if err := refuseWithModifiers(body); err != nil {
		return nil, err
	}
	scope := withScope{}
	for _, item := range body.OC_ProjectionItems().AllOC_ProjectionItem() {
		expression := item.OC_Expression()
		alias, err := withAlias(item)
		if err != nil {
			return nil, err
		}
		var entry withItem
		if name, ok := bareVariable(expression); ok {
			known, passed := w[name]
			if !passed {
				return nil, w.notPassedOn(expression, name)
			}
			entry = known
			if alias == "" {
				alias = name
			}
		} else {
			if alias == "" {
				return nil, unnamedWithValue(expression)
			}
			value, err := w.expression(expression)
			if err != nil {
				return nil, err
			}
			if len(expressionProperties(value)) == 0 && !containsAggregate(value) {
				return nil, unsupportedf(expression, "a constant in WITH", "write the value where it is used")
			}
			entry = withItem{projection: valueProjection(value, alias), aggregates: containsAggregate(value)}
		}
		entry.pos = positionOf(expression)
		if _, taken := scope[alias]; taken {
			return nil, unsupportedf(item, "a name WITH uses twice", "%q is named twice; give each value its own name", alias)
		}
		scope[alias] = entry
	}
	return scope, nil
}

// valueProjection keeps a property or an aggregate that stands on its own in
// its own field, as analyzeComputedProjection does.
func valueProjection(value Expression, alias string) *Projection {
	switch value := value.(type) {
	case PropertyRef:
		return &Projection{Property: &value, Alias: alias, Pos: value.Pos}
	case Aggregate:
		return &Projection{Aggregate: &value, Alias: alias, Pos: value.Pos}
	default:
		return &Projection{Expression: value, Alias: alias, Pos: value.expressionPosition()}
	}
}

// analyzeCollectedCount reads collect([DISTINCT] n.p) in a WITH as the count
// size() gives of it: collect skips nulls as count does, and DISTINCT means
// the same in both.
func analyzeCollectedCount(expression parsing.IOC_ExpressionContext) (*Aggregate, bool, error) {
	term, single := singleExpressionAtom(expression)
	if !single || len(term.propertyLookups) > 0 {
		return nil, false, nil
	}
	invocation := term.atom.OC_FunctionInvocation()
	if invocation == nil || !strings.EqualFold(identifier(invocation.OC_FunctionName().GetText()), "collect") {
		return nil, false, nil
	}
	if arguments := invocation.AllOC_Expression(); len(arguments) == 1 {
		if property, err := analyzePropertyRef(arguments[0]); err == nil {
			return &Aggregate{
				Function: "COUNT", Name: "count", Distinct: invocation.DISTINCT() != nil,
				Property: property, Pos: positionOf(invocation),
			}, true, nil
		}
	}
	return nil, true, unsupportedf(expression, "this collect in WITH",
		"collect one property, as in collect(DISTINCT n.code) AS codes, and test its size, as in WHERE size(codes) = 3")
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
	if query.Grouped && returnsAggregate(items) {
		if err := w.analyzeTotals(query, body); err != nil {
			return err
		}
		return analyzeRowWindow(query, body)
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
	return analyzeRowWindow(query, body)
}

// returnsAggregate reports whether a RETURN item is an aggregate written on
// its own, which after a grouping WITH aggregates the groups.
func returnsAggregate(items parsing.IOC_ProjectionItemsContext) bool {
	for _, item := range items.AllOC_ProjectionItem() {
		if _, ok, _ := analyzeAggregate(item.OC_Expression()); ok {
			return true
		}
		if term, single := singleExpressionAtom(item.OC_Expression()); single && len(term.propertyLookups) == 0 {
			if invocation := term.atom.OC_FunctionInvocation(); invocation != nil {
				if _, known := aggregateFunctions[strings.ToLower(identifier(invocation.OC_FunctionName().GetText()))]; known {
					return true
				}
			}
		}
	}
	return false
}

// analyzeTotals reads a RETURN that aggregates the groups a WITH made, as in
// counting the materials a WITH kept: count(*), or an aggregate of one value
// the WITH named. Each group yields the values the totals read, and the query
// returns the totals over those rows.
func (w withScope) analyzeTotals(query *Query, body parsing.IOC_ProjectionBodyContext) error {
	if query.Distinct || body.OC_Order() != nil {
		return unsupportedf(body, "DISTINCT or ORDER BY on a RETURN that aggregates a WITH's groups",
			"it returns one row; leave them out")
	}
	shape := func(at antlr.ParserRuleContext) error {
		return unsupportedf(at, "this RETURN over a WITH's groups",
			"return only aggregates of what the WITH named, as in RETURN count(*) AS groups, sum(n) AS total")
	}
	read := map[string]bool{}
	for _, item := range body.OC_ProjectionItems().AllOC_ProjectionItem() {
		expression := item.OC_Expression()
		term, single := singleExpressionAtom(expression)
		if !single || len(term.propertyLookups) > 0 {
			return shape(expression)
		}
		total := Total{Alias: sourceText(expression), Pos: positionOf(expression)}
		if count := term.atom.COUNT(); count != nil {
			total.Function, total.Name = "COUNT", count.GetText()
		} else {
			invocation := term.atom.OC_FunctionInvocation()
			if invocation == nil {
				return shape(expression)
			}
			written := identifier(invocation.OC_FunctionName().GetText())
			function, known := aggregateFunctions[strings.ToLower(written)]
			arguments := invocation.AllOC_Expression()
			if !known || len(arguments) != 1 {
				return shape(expression)
			}
			name, bare := bareVariable(arguments[0])
			if !bare {
				return shape(arguments[0])
			}
			entry, passed := w[name]
			switch {
			case !passed:
				return w.notPassedOn(arguments[0], name)
			case entry.node != "" || entry.collected != nil:
				return unsupportedf(arguments[0], "aggregating "+name+" over the groups",
					"aggregate a value the WITH named, as in sum(n), or count the groups with count(*)")
			}
			total.Function, total.Name, total.Distinct, total.Of = function, written, invocation.DISTINCT() != nil, name
			if !read[name] {
				read[name] = true
				projection := *entry.projection
				projection.Alias = name
				query.Return = append(query.Return, projection)
			}
		}
		if variable := item.OC_Variable(); variable != nil {
			alias, err := namedIdentifier(variable, "column name")
			if err != nil {
				return err
			}
			total.Alias = alias
		}
		query.Totals = append(query.Totals, total)
	}
	if len(query.Return) == 0 {
		// count(*) alone reads nothing of a group, but each group still has
		// to yield a row to be counted.
		query.Return = []Projection{{Aggregate: &Aggregate{Function: "COUNT", Name: "count"}, Alias: "group_rows"}}
	}
	return nil
}

// analyzeRowWindow reads SKIP and LIMIT after a WITH.
func analyzeRowWindow(query *Query, body parsing.IOC_ProjectionBodyContext) error {
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
			return &Projection{Value: GraphVariable{Name: entry.node, Pos: positionOf(expression)}, Alias: name, Pos: positionOf(expression)}, nil
		}
		if entry.collected != nil {
			return nil, collectedListError(expression, name)
		}
		projection := *entry.projection
		projection.Alias = name
		return &projection, nil
	}
	if constant, ok := literalOrParameterAtom(expression); ok {
		return &Projection{Value: constant, Alias: sourceText(expression), Pos: constant.Pos}, nil
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
	if _, aggregate, _ := analyzeAggregate(expression); !aggregate {
		if sum, err := valueChain(expression); err == nil {
			// A value computed from what the WITH named, as in
			// RETURN stock - usage * 50 AS shortage.
			value, err := readSum(sum, w.term)
			if err != nil {
				return nil, err
			}
			if len(expressionProperties(value)) == 0 && !containsAggregate(value) {
				return nil, unsupportedf(expression, "returning a constant",
					"a computed column must use a property or an aggregate")
			}
			return valueProjection(value, sourceText(expression)), nil
		}
	}
	return nil, unsupportedf(expression, "this RETURN after WITH",
		"after WITH, RETURN lists what the WITH named, or properties of the nodes it passed on")
}

func collectedListError(at antlr.ParserRuleContext, name string) error {
	return unsupportedf(at, "using a list a WITH collected",
		"test or return its size, as in size(%s); to return the list itself, collect in RETURN", name)
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
		case entry.collected != nil:
			return nil, collectedListError(expression, name)
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
	if len(expressionProperties(leftValue))+len(expressionProperties(rightValue)) == 0 &&
		!containsAggregate(leftValue) && !containsAggregate(rightValue) {
		return nil, unsupportedf(ctx, "a condition over constants",
			"a condition has to read a property or an aggregate to say anything about the rows")
	}
	return ExpressionComparison{Left: leftValue, Operator: operator, Right: rightValue, Pos: positionOf(ctx)}, nil
}

// value reads one side of a comparison in a WITH's WHERE.
func (w withScope) value(ctx parsing.IOC_AddOrSubtractExpressionContext) (Expression, error) {
	return readSum(ctx, w.term)
}

// expression reads a value computed from what a WITH passed on.
func (w withScope) expression(ctx parsing.IOC_ExpressionContext) (Expression, error) {
	sum, err := valueChain(ctx)
	if err != nil {
		return nil, err
	}
	return readSum(sum, w.term)
}

// term reads one term of a value after a WITH: a name the WITH passed on, a
// property of a node it passed on, the size of a list it collected, a literal
// or a parameter. Aggregating is the first WITH's to do; a name it gave the
// result is how anything after it reads one.
func (w withScope) term(ctx parsing.IOC_PropertyOrLabelsExpressionContext, atom parsing.IOC_AtomContext) (Expression, error) {
	lookups := ctx.AllOC_PropertyLookup()
	if variable := atom.OC_Variable(); variable != nil {
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
		case entry.collected != nil:
			return nil, collectedListError(ctx, name)
		case entry.projection.Property != nil:
			return *entry.projection.Property, nil
		case entry.projection.Aggregate != nil:
			return *entry.projection.Aggregate, nil
		case entry.projection.Expression != nil:
			if _, condition := entry.projection.Expression.(ConditionValue); condition {
				return nil, unsupportedf(ctx, "comparing a condition", "test the condition itself in WHERE")
			}
			return entry.projection.Expression, nil
		default:
			return nil, unsupportedf(ctx, "computing with "+name, "%q is not a property, a number or an aggregate", name)
		}
	}
	if len(lookups) == 0 {
		if parenthesized := atom.OC_ParenthesizedExpression(); parenthesized != nil {
			return w.expression(parenthesized.OC_Expression())
		}
		if caseOrCoalesce(atom) {
			// CASE after a WITH reads the WITH's names in its conditions as
			// well, so CASE WHEN n > 10 THEN ... can test an aggregate.
			return analyzeCaseOrCoalesce(atom, caseReaders{value: w.expression, condition: w.predicate})
		}
		if invocation := atom.OC_FunctionInvocation(); invocation != nil &&
			strings.EqualFold(identifier(invocation.OC_FunctionName().GetText()), "size") {
			return w.size(invocation)
		}
		if _, aggregate, err := analyzeAggregateAtom(atom); aggregate || err != nil {
			return nil, unsupportedf(ctx, "an aggregate after WITH",
				"aggregate in the first WITH and name the result, as in WITH count(*) AS n")
		}
	}
	operand, err := analyzeAtomOperand(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case operand.literal != nil:
		return *operand.literal, nil
	case operand.parameter != nil:
		return *operand.parameter, nil
	default:
		return nil, unsupportedf(ctx, "this value after WITH",
			"use a name the WITH passed on, a literal or a parameter")
	}
}

// size reads size(list) over a list a WITH collected, which is the count of
// what it collected.
func (w withScope) size(invocation parsing.IOC_FunctionInvocationContext) (Expression, error) {
	if arguments := invocation.AllOC_Expression(); len(arguments) == 1 {
		if name, ok := bareVariable(arguments[0]); ok {
			entry, passed := w[name]
			if !passed {
				return nil, w.notPassedOn(arguments[0], name)
			}
			if entry.collected != nil {
				return *entry.collected, nil
			}
		}
	}
	return nil, unsupportedf(invocation, "this size()",
		"size() takes a list a WITH collected, as in WITH collect(DISTINCT n.code) AS codes ... size(codes)")
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

// literalOrParameterAtom reads a literal or a parameter written on its own as
// a graph constant, as in RETURN 'USES' AS relationType.
func literalOrParameterAtom(expression parsing.IOC_ExpressionContext) (GraphConstant, bool) {
	term, single := singleExpressionAtom(expression)
	if !single || len(term.propertyLookups) > 0 || (term.atom.OC_Literal() == nil && term.atom.OC_Parameter() == nil) ||
		(term.atom.OC_Literal() != nil && (term.atom.OC_Literal().OC_MapLiteral() != nil || term.atom.OC_Literal().OC_ListLiteral() != nil)) {
		return GraphConstant{}, false
	}
	constant, err := analyzeGraphConstant(expression)
	return constant, err == nil
}
