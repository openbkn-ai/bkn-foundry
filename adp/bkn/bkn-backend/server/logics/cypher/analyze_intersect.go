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

// analyzeIntersection reads two stages that each collect one property into a
// list, and a RETURN of sizes over the two lists:
//
//	MATCH ... WITH collect(DISTINCT x.p) AS a
//	MATCH ... WITH a, collect(DISTINCT y.q) AS b
//	RETURN size([v IN a WHERE v IN b]) AS common, size(a) AS n
//
// ok is false when the stages do not collect lists, and the caller then reads
// or refuses them as any other stages. The second stage does not read the
// first list: each list stands on its own, and only RETURN compares them.
func analyzeIntersection(first, second stageSegment, returning parsing.IOC_ReturnContext,
	constants nameScope) (*Query, bool, error) {
	items := first.with.OC_ProjectionBody().OC_ProjectionItems().AllOC_ProjectionItem()
	if len(items) != 1 || collectInvocation(items[0].OC_Expression()) == nil {
		return nil, false, nil
	}
	collects := false
	for _, item := range second.with.OC_ProjectionBody().OC_ProjectionItems().AllOC_ProjectionItem() {
		collects = collects || collectInvocation(item.OC_Expression()) != nil
	}
	if !collects {
		return nil, false, nil
	}
	query, err := intersection([2]stageSegment{first, second}, returning, constants)
	return query, true, err
}

func intersection(stages [2]stageSegment, returning parsing.IOC_ReturnContext, constants nameScope) (*Query, error) {
	result := &Intersection{}
	var names [2]string
	for i, stage := range stages {
		body := stage.with.OC_ProjectionBody()
		if err := refuseWithModifiers(body); err != nil {
			return nil, err
		}
		if where := stage.with.OC_Where(); where != nil {
			return nil, unsupportedf(where, "WHERE on a WITH that collects a list",
				"write the condition on the MATCH before it")
		}
		if i == 1 {
			if use := mentionsName(stage.reading, names[0]); use != nil {
				// Without this the use reads as an unknown variable, and the
				// hint for that points at a property that does not exist.
				return nil, unsupportedf(use, "using the first list in the second MATCH",
					"each list stands on its own; compare them in RETURN, as in size([x IN %s WHERE x IN other])", names[0])
			}
		}
		read, err := analyzeReading(stage.reading, stage.with, constants)
		if err != nil {
			return nil, err
		}
		var list parsing.IOC_ProjectionItemContext
		passed := false
		for _, item := range body.OC_ProjectionItems().AllOC_ProjectionItem() {
			if i == 1 && !passed {
				if name, ok := bareVariable(item.OC_Expression()); ok && name == names[0] {
					alias, err := withAlias(item)
					if err != nil {
						return nil, err
					}
					if alias == "" || alias == name {
						passed = true
						continue
					}
				}
			}
			if list != nil {
				return nil, unsupportedf(item, "this WITH between two lists",
					"pass on the first list and collect the second, as in WITH %s, collect(DISTINCT y.code) AS other", names[0])
			}
			list = item
		}
		if i == 1 && !passed {
			return nil, unsupportedf(body, "a second list without the first",
				"pass the first list on, as in WITH %s, collect(DISTINCT y.code) AS other", names[0])
		}
		name, property, distinct, err := collectedItem(list)
		if err != nil {
			return nil, err
		}
		if i == 1 && name == names[0] {
			return nil, unsupportedf(list, "a name WITH uses twice", "%q is named twice; give each list its own name", name)
		}
		names[i] = name
		result.Lists[i] = &Query{
			Pattern: read.Pattern,
			Where:   read.Where,
			Return:  []Projection{{Property: &property, Alias: "value", Pos: property.Pos}},
		}
		result.Distinct[i] = distinct
	}
	items, err := intersectionReturn(returning, names)
	if err != nil {
		return nil, err
	}
	result.Items = items
	return &Query{Intersection: result}, nil
}

// collectedItem reads collect([DISTINCT] x.p) AS name.
func collectedItem(item parsing.IOC_ProjectionItemContext) (string, PropertyRef, bool, error) {
	shape := unsupportedf(item, "this list", "collect one property and name it, as in collect(DISTINCT m.code) AS codes")
	name, err := withAlias(item)
	if err != nil {
		return "", PropertyRef{}, false, err
	}
	invocation := collectInvocation(item.OC_Expression())
	if name == "" || invocation == nil {
		return "", PropertyRef{}, false, shape
	}
	property, err := analyzePropertyRef(invocation.AllOC_Expression()[0])
	if err != nil {
		return "", PropertyRef{}, false, shape
	}
	return name, *property, invocation.DISTINCT() != nil, nil
}

// collectInvocation returns the call when the expression is collect(x) and
// nothing else.
func collectInvocation(expression parsing.IOC_ExpressionContext) parsing.IOC_FunctionInvocationContext {
	invocation := functionCall(expression, "collect")
	if invocation == nil || len(invocation.AllOC_Expression()) != 1 {
		return nil
	}
	return invocation
}

// functionCall returns the call when the expression is one call of the named
// function and nothing else.
func functionCall(expression parsing.IOC_ExpressionContext, function string) parsing.IOC_FunctionInvocationContext {
	term, single := singleExpressionAtom(expression)
	if !single || len(term.propertyLookups) > 0 {
		return nil
	}
	invocation := term.atom.OC_FunctionInvocation()
	if invocation == nil || !strings.EqualFold(identifier(invocation.OC_FunctionName().GetText()), function) {
		return nil
	}
	return invocation
}

// intersectionReturn reads what RETURN asks of the two lists: sizes only, as
// one row.
func intersectionReturn(returning parsing.IOC_ReturnContext, names [2]string) ([]IntersectionItem, error) {
	body := returning.OC_ProjectionBody()
	if body.DISTINCT() != nil || body.OC_Order() != nil || body.OC_Skip() != nil || body.OC_Limit() != nil {
		return nil, unsupportedf(body, "DISTINCT, ORDER BY, SKIP or LIMIT after two lists",
			"their sizes come back as one row; leave them out")
	}
	projection := body.OC_ProjectionItems()
	if strings.HasPrefix(strings.TrimSpace(projection.GetText()), "*") {
		return nil, unsupportedf(projection, "RETURN *", "name the sizes to return, as in size(%s) AS n", names[0])
	}
	var items []IntersectionItem
	seen := map[string]bool{}
	for _, item := range projection.AllOC_ProjectionItem() {
		expression := item.OC_Expression()
		alias, err := withAlias(item)
		if err != nil {
			return nil, err
		}
		if alias == "" {
			alias = sourceText(expression)
		}
		if seen[alias] {
			return nil, unsupportedf(item, "a column name used twice", "column name %q is returned twice", alias)
		}
		seen[alias] = true
		entry, err := intersectionSize(expression, names)
		if err != nil {
			return nil, err
		}
		entry.Alias, entry.Pos = alias, positionOf(item)
		items = append(items, entry)
	}
	return items, nil
}

// intersectionSize reads size(a) or size([v IN a WHERE v IN b]).
func intersectionSize(expression parsing.IOC_ExpressionContext, names [2]string) (IntersectionItem, error) {
	shape := unsupportedf(expression, "this value after two collected lists",
		"return sizes: size(%[1]s), size(%[2]s), or what they share, as in size([x IN %[1]s WHERE x IN %[2]s])",
		names[0], names[1])
	list := func(name string) int {
		for i, each := range names {
			if each == name {
				return i
			}
		}
		return -1
	}
	if term, single := singleExpressionAtom(expression); single && len(term.propertyLookups) == 0 {
		if _, ok := bareVariable(expression); ok || term.atom.OC_ListComprehension() != nil {
			return IntersectionItem{}, unsupportedf(expression, "returning a list",
				"return its size, as in size([x IN %[1]s WHERE x IN %[2]s]); to list the shared values, share a node "+
					"in one MATCH, as in MATCH (a)-[:R]->(m)<-[:R]-(b) RETURN DISTINCT m.code", names[0], names[1])
		}
	}
	invocation := functionCall(expression, "size")
	if invocation == nil || invocation.DISTINCT() != nil || len(invocation.AllOC_Expression()) != 1 {
		return IntersectionItem{}, shape
	}
	argument := invocation.AllOC_Expression()[0]
	if name, ok := bareVariable(argument); ok {
		if i := list(name); i >= 0 {
			return IntersectionItem{List: i}, nil
		}
		return IntersectionItem{}, shape
	}
	term, single := singleExpressionAtom(argument)
	if !single || len(term.propertyLookups) > 0 || term.atom.OC_ListComprehension() == nil {
		return IntersectionItem{}, shape
	}
	comprehension := term.atom.OC_ListComprehension()
	filter := comprehension.OC_FilterExpression()
	if comprehension.OC_Expression() != nil || filter.OC_Where() == nil {
		return IntersectionItem{}, shape
	}
	element, err := namedIdentifier(filter.OC_IdInColl().OC_Variable(), "variable")
	if err != nil {
		return IntersectionItem{}, err
	}
	from, ok := bareVariable(filter.OC_IdInColl().OC_Expression())
	i := list(from)
	if !ok || i < 0 {
		return IntersectionItem{}, shape
	}
	member, in, ok := membershipTest(filter.OC_Where().OC_Expression())
	if !ok || member != element || in != names[1-i] {
		return IntersectionItem{}, shape
	}
	return IntersectionItem{List: i, Common: true}, nil
}

// membershipTest reads v IN b, where both are bare names.
func membershipTest(ctx parsing.IOC_ExpressionContext) (string, string, bool) {
	xors := ctx.OC_OrExpression().AllOC_XorExpression()
	if len(xors) != 1 {
		return "", "", false
	}
	ands := xors[0].AllOC_AndExpression()
	if len(ands) != 1 {
		return "", "", false
	}
	nots := ands[0].AllOC_NotExpression()
	if len(nots) != 1 || len(nots[0].AllNOT()) > 0 {
		return "", "", false
	}
	comparison := nots[0].OC_ComparisonExpression()
	if len(comparison.AllOC_PartialComparisonExpression()) > 0 {
		return "", "", false
	}
	predicate := comparison.OC_StringListNullPredicateExpression()
	lists := predicate.AllOC_ListPredicateExpression()
	if len(lists) != 1 || len(predicate.AllOC_StringPredicateExpression())+len(predicate.AllOC_NullPredicateExpression()) > 0 {
		return "", "", false
	}
	element, ok := sumBareVariable(predicate.OC_AddOrSubtractExpression())
	if !ok {
		return "", "", false
	}
	list, ok := sumBareVariable(lists[0].OC_AddOrSubtractExpression())
	return element, list, ok
}

// mentionsName returns the first use of a variable of that name in the
// clauses, or nil.
func mentionsName(clauses []parsing.IOC_ReadingClauseContext, name string) antlr.ParserRuleContext {
	var find func(antlr.Tree) antlr.ParserRuleContext
	find = func(node antlr.Tree) antlr.ParserRuleContext {
		if variable, ok := node.(parsing.IOC_VariableContext); ok && identifier(variable.GetText()) == name {
			return variable
		}
		for _, child := range node.GetChildren() {
			if found := find(child); found != nil {
				return found
			}
		}
		return nil
	}
	for _, clause := range clauses {
		if found := find(clause); found != nil {
			return found
		}
	}
	return nil
}

func unwindError(clause antlr.ParserRuleContext) error {
	return unsupportedf(clause, "UNWIND",
		"to count the values two collected lists share, return size([x IN a WHERE x IN b]); "+
			"to filter by a list, write WHERE n.property IN list")
}
