// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// analyzePipeline preserves clause order and delays binding unlabelled nodes
// until the incoming scope is known. It never turns later MATCHes into joins
// across a WITH boundary.
func analyzePipeline(single parsing.IOC_SingleQueryContext) (*Query, error) {
	query := &Query{ValueEvaluation: true}
	addReading := func(clause parsing.IOC_ReadingClauseContext) error {
		part := &Query{ValueEvaluation: true}
		stage := QueryStage{Query: part}
		if match := clause.OC_Match(); match != nil {
			stage.Kind, stage.Optional = "match", match.OPTIONAL() != nil
			builder := newPatternBuilder()
			builder.graph, builder.allowUnlabeled = true, true
			if err := builder.addPattern(match.OC_Pattern()); err != nil {
				return err
			}
			part.Pattern = builder.pattern
			part.Where = combine("AND", builder.inline, positionOf(match))
			if where := match.OC_Where(); where != nil {
				value, err := readValueExpression(where.OC_Expression(), 0)
				if err != nil {
					return err
				}
				part.ResidualWhere = value
			}
		} else if unwind := clause.OC_Unwind(); unwind != nil {
			stage.Kind = "unwind"
			value, err := readValueExpression(unwind.OC_Expression(), 0)
			if err != nil {
				return err
			}
			name, err := namedIdentifier(unwind.OC_Variable(), "variable")
			if err != nil {
				return err
			}
			part.Unwinds = []Unwind{{Expression: value, Variable: name, Pos: positionOf(unwind)}}
		} else {
			return unsupported(clause, "procedure calls")
		}
		query.Stages = append(query.Stages, stage)
		return nil
	}
	addProjection := func(body parsing.IOC_ProjectionBodyContext, where parsing.IOC_WhereContext, kind string) error {
		part := &Query{ValueEvaluation: true}
		if err := analyzeProjectionBody(part, body); err != nil {
			return err
		}
		if order := body.OC_Order(); order != nil {
			for i, item := range order.AllOC_SortItem() {
				value, err := readValueExpression(item.OC_Expression(), 0)
				if err != nil {
					return err
				}
				part.OrderBy[i].Value = value
				part.OrderBy[i].Alias = ""
				part.OrderBy[i].Property = nil
				part.OrderBy[i].Aggregate = nil
			}
		}
		if kind == "with" {
			for _, item := range body.OC_ProjectionItems().AllOC_ProjectionItem() {
				if item.OC_Variable() == nil {
					if _, ok := bareVariable(item.OC_Expression()); !ok {
						return unsupportedf(item, "an unaliased WITH expression", "use AS to name the new variable")
					}
				}
			}
		}
		if where != nil {
			value, err := readValueExpression(where.OC_Expression(), 0)
			if err != nil {
				return err
			}
			part.PostWhere = value
		}
		query.Stages = append(query.Stages, QueryStage{Kind: kind, Query: part})
		return nil
	}
	var final parsing.IOC_SinglePartQueryContext
	if multi := single.OC_MultiPartQuery(); multi != nil {
		for _, child := range multi.GetChildren() {
			switch node := child.(type) {
			case parsing.IOC_UpdatingClauseContext:
				return nil, unsupportedf(node, "writing", "this interface only runs read-only queries")
			case parsing.IOC_ReadingClauseContext:
				if err := addReading(node); err != nil {
					return nil, err
				}
			case parsing.IOC_WithContext:
				if err := addProjection(node.OC_ProjectionBody(), node.OC_Where(), "with"); err != nil {
					return nil, err
				}
			case parsing.IOC_SinglePartQueryContext:
				final = node
			}
		}
	} else {
		final = single.OC_SinglePartQuery()
	}
	if final == nil {
		return nil, unsupported(single, "a query without RETURN")
	}
	if writes := final.AllOC_UpdatingClause(); len(writes) > 0 {
		return nil, unsupportedf(writes[0], "writing", "this interface only runs read-only queries")
	}
	for _, reading := range final.AllOC_ReadingClause() {
		if err := addReading(reading); err != nil {
			return nil, err
		}
	}
	if final.OC_Return() == nil {
		return nil, unsupported(final, "a query without RETURN")
	}
	if err := addProjection(final.OC_Return().OC_ProjectionBody(), nil, "return"); err != nil {
		return nil, err
	}
	unwinds := 0
	for _, stage := range query.Stages {
		if stage.Kind == "unwind" {
			unwinds++
		}
	}
	if unwinds > 32 {
		return nil, &Unsupported{Feature: "UNWIND clause budget", Detail: "at most 32 expansions are allowed per branch"}
	}
	if len(query.Stages) > 64 {
		return nil, &Unsupported{Feature: "query stage budget", Detail: "at most 64 stages are allowed"}
	}
	return query, nil
}

func projectionHasStar(body parsing.IOC_ProjectionBodyContext) bool {
	return strings.HasPrefix(strings.TrimSpace(body.OC_ProjectionItems().GetText()), "*")
}

func projectionUsesAliasExpression(body parsing.IOC_ProjectionBodyContext) bool {
	if body.OC_Order() == nil {
		return false
	}
	aliases := map[string]bool{}
	for _, item := range body.OC_ProjectionItems().AllOC_ProjectionItem() {
		if name := item.OC_Variable(); name != nil {
			aliases[identifier(name.GetText())] = true
		}
	}
	var referencesAlias func(ValueExpression) bool
	referencesAlias = func(expression ValueExpression) bool {
		switch value := expression.(type) {
		case VariableExpression:
			return aliases[value.Name]
		case PropertyExpression:
			return aliases[value.Ref.Variable]
		case OperationExpression:
			for _, argument := range value.Arguments {
				if referencesAlias(argument) {
					return true
				}
			}
		}
		return false
	}
	for _, item := range body.OC_Order().AllOC_SortItem() {
		if _, bare := bareVariable(item.OC_Expression()); bare {
			continue
		}
		value, err := readValueExpression(item.OC_Expression(), 0)
		if err == nil && referencesAlias(value) {
			return true
		}
	}
	return false
}
