// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"github.com/antlr4-go/antlr/v4"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// carriesNodes reports whether a WITH before a MATCH passes on a node of the
// stage before it, which makes the stage a carried one rather than the source
// of a semi-join's values.
func carriesNodes(with parsing.IOC_WithContext, stage *Query) bool {
	nodes := patternVariables(stage)
	for _, item := range with.OC_ProjectionBody().OC_ProjectionItems().AllOC_ProjectionItem() {
		if name, ok := bareVariable(item.OC_Expression()); ok && nodes[name] {
			return true
		}
	}
	return false
}

// analyzeCarried reads a stage whose WITH passes nodes on, and the MATCH and
// RETURN after it:
//
//	MATCH (p:players)<-[:R]-(g:goals) WITH p, count(g) AS total
//	MATCH (p)<-[:R]-(g2:goals)-[:S]->(t:teams)
//	RETURN p, t, count(g2) AS n, total
//
// The stage folds its WITH in as a query of its own does, and each of its rows
// joins the rows of the next MATCH that share its nodes, which is what Cypher
// does with the rows a WITH passes on. The nodes keep their labels in the next
// MATCH, and the values the WITH named may be returned, sorted by and computed
// with there.
func analyzeCarried(stage *Query, with parsing.IOC_WithContext, after []parsing.IOC_ReadingClauseContext,
	at antlr.ParserRuleContext, returning parsing.IOC_ReturnContext, constants nameScope) (*Query, error) {
	// DISTINCT, ORDER BY and LIMIT change which rows the next MATCH meets,
	// and moved to RETURN they would mean something else, so the general
	// hint to write them there does not apply.
	body := with.OC_ProjectionBody()
	if body.DISTINCT() != nil {
		// Dropping DISTINCT alone would pass each node on once per row it
		// matched, and multiply what the next MATCH counts.
		return nil, unsupportedf(body, "WITH DISTINCT before a MATCH that a node is passed on to",
			"aggregate in the WITH instead, as in WITH p, count(*) AS n, which passes each node on once")
	}
	if body.OC_Skip() != nil || body.OC_Limit() != nil {
		return nil, unsupportedf(body, "SKIP or LIMIT on a WITH that passes nodes on",
			"the next MATCH would meet only some of the rows; filter them with the WITH's WHERE instead")
	}
	if body.OC_Order() != nil {
		return nil, unsupportedf(body, "ORDER BY on a WITH that passes nodes on", "sort in RETURN instead")
	}
	scope, err := analyzeWith(stage, with)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for _, node := range stage.Pattern.Nodes {
		labels[node.Variable] = node.Label
	}
	carried := &CarriedStage{Stage: stage}
	next := nameScope{}
	for name, binding := range constants {
		next[name] = binding
	}
	values := map[string]bool{}
	for _, item := range with.OC_ProjectionBody().OC_ProjectionItems().AllOC_ProjectionItem() {
		alias, err := withAlias(item)
		if err != nil {
			return nil, err
		}
		name, bare := bareVariable(item.OC_Expression())
		if alias == "" && bare {
			alias = name
		}
		entry := scope[alias]
		switch {
		case entry.node != "":
			if alias != entry.node {
				return nil, unsupportedf(item, "renaming a node a WITH passes on",
					"pass it on under its own name, as in WITH %s", entry.node)
			}
			carried.Nodes = append(carried.Nodes, entry.node)
			next[alias] = &nameBinding{label: labels[entry.node]}
		case entry.collected != nil:
			return nil, unsupportedf(item, "a collected list passed on with nodes",
				"count in the WITH instead, as in count(DISTINCT x.p) AS %s", alias)
		default:
			projection := *entry.projection
			projection.Alias = alias
			stage.Return = append(stage.Return, projection)
			carried.Values = append(carried.Values, alias)
			next[alias] = &nameBinding{carried: true}
			values[alias] = true
		}
	}

	query, err := analyzeReading(after, at, next)
	if err != nil {
		return nil, err
	}
	used := patternVariables(query)
	for _, node := range query.Pattern.Nodes {
		if node.Optional {
			return nil, unsupportedAt(node.Pos, "OPTIONAL MATCH after a WITH that passes nodes on",
				"write MATCH; a node the next MATCH does not find drops its row")
		}
	}
	for _, node := range carried.Nodes {
		if !used[node] {
			return nil, unsupportedf(with, "a node a WITH passes on and the next MATCH leaves out",
				"use %s in the next MATCH, as in MATCH (%s)-[:R]->(x), or leave it out of the WITH", node, node)
		}
	}
	if err := analyzeProjectionBody(query, returning.OC_ProjectionBody(), nil, values); err != nil {
		return nil, err
	}
	query.Carried = carried
	return query, nil
}

// patternVariables lists the variables a query's pattern names.
func patternVariables(query *Query) map[string]bool {
	variables := map[string]bool{}
	for _, node := range query.Pattern.Nodes {
		if node.Variable != "" && !node.Anonymous {
			variables[node.Variable] = true
		}
	}
	return variables
}

// carriedProjection reads a RETURN item that uses a value a carried stage
// named: the value itself, or a value computed with it. ok is false when the
// item names none, and RETURN reads it as it reads any other.
func carriedProjection(expression parsing.IOC_ExpressionContext, carried map[string]bool) (*Projection, bool, error) {
	if !mentionsAny(expression, carried) {
		return nil, false, nil
	}
	value, err := carriedValue(expression, carried)
	if err != nil {
		return nil, true, err
	}
	switch value := value.(type) {
	case Aggregate:
		return &Projection{Aggregate: &value, Alias: sourceText(expression), Pos: value.Pos}, true, nil
	case PropertyRef:
		return &Projection{Property: &value, Alias: sourceText(expression), Pos: value.Pos}, true, nil
	}
	return &Projection{Expression: value, Alias: sourceText(expression), Pos: positionOf(expression)}, true, nil
}

// carriedSortKey reads an ORDER BY item that uses a value a carried stage
// named. A name RETURN returned under that alias is left to sort by the
// returned column.
func carriedSortKey(expression parsing.IOC_ExpressionContext, carried map[string]bool, query *Query) (*SortKey, bool, error) {
	if name, ok := bareVariable(expression); ok && returnsAlias(query, name) {
		return nil, false, nil
	}
	if !mentionsAny(expression, carried) {
		return nil, false, nil
	}
	value, err := carriedValue(expression, carried)
	if err != nil {
		return nil, true, err
	}
	return &SortKey{Expression: value, Pos: positionOf(expression)}, true, nil
}

func carriedValue(expression parsing.IOC_ExpressionContext, carried map[string]bool) (Expression, error) {
	sum, err := valueChain(expression)
	if err != nil {
		return nil, unsupportedf(expression, "a condition over a carried value",
			"test it in the WHERE of the WITH that named it")
	}
	var term termReader
	term = func(ctx parsing.IOC_PropertyOrLabelsExpressionContext, atom parsing.IOC_AtomContext) (Expression, error) {
		if len(ctx.AllOC_PropertyLookup()) == 0 {
			if variable := atom.OC_Variable(); variable != nil {
				if name := identifier(variable.GetText()); carried[name] {
					return CarriedValue{Name: name, Pos: positionOf(atom)}, nil
				}
			}
			if parenthesized := atom.OC_ParenthesizedExpression(); parenthesized != nil {
				inner, err := valueChain(parenthesized.OC_Expression())
				if err != nil {
					return nil, err
				}
				return readSum(inner, term)
			}
		}
		return analyzeTerm(ctx, atom)
	}
	return readSum(sum, term)
}

// mentionsAny reports whether a variable of one of the names appears under
// the tree.
func mentionsAny(tree antlr.Tree, names map[string]bool) bool {
	if variable, ok := tree.(parsing.IOC_VariableContext); ok && names[identifier(variable.GetText())] {
		return true
	}
	for _, child := range tree.GetChildren() {
		if mentionsAny(child, names) {
			return true
		}
	}
	return false
}
