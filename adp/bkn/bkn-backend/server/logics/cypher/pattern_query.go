// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

func readRelationshipRange(edge *EdgeRef, ctx parsing.IOC_RangeLiteralContext) error {
	text := strings.ReplaceAll(strings.TrimPrefix(ctx.GetText(), "*"), " ", "")
	edge.VariableLength = true
	edge.MinHops, edge.MaxHops = 1, -1
	parse := func(raw string) (int, error) {
		value, err := strconv.ParseInt(raw, 0, 64)
		if err != nil || value < 0 || value > interfaces.CYPHER_MAX_PATH_LENGTH {
			return 0, unsupportedf(ctx, "path depth budget", "length must be between 0 and %d", interfaces.CYPHER_MAX_PATH_LENGTH)
		}
		return int(value), nil
	}
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "..")
	var err error
	if len(parts) == 1 {
		edge.MinHops, err = parse(parts[0])
		edge.MaxHops = edge.MinHops
		return err
	}
	if parts[0] != "" {
		edge.MinHops, err = parse(parts[0])
		if err != nil {
			return err
		}
	}
	if parts[1] != "" {
		edge.MaxHops, err = parse(parts[1])
		if err != nil {
			return err
		}
	}
	if edge.MaxHops >= 0 && edge.MaxHops < edge.MinHops {
		return unsupportedf(ctx, "an invalid path range", "maximum length is below the minimum")
	}
	return nil
}

func patternNeedsRuntime(pattern Pattern) bool {
	for _, node := range pattern.Nodes {
		if node.Label == "" {
			return true
		}
	}
	seenRelationships := map[string]bool{}
	for _, edge := range pattern.Edges {
		if edge.Variable != "" {
			if seenRelationships[edge.Variable] {
				return true
			}
			seenRelationships[edge.Variable] = true
		}
		if edge.VariableLength || len(edge.Types) > 0 || edge.Type == "" {
			return true
		}
		if edge.Direction == Undirected && (edge.Variable != "" || len(pattern.Paths) > 0) {
			return true
		}
	}
	return false
}

func patternQueryStages(query *Query) *Query {
	result := &Query{ValueEvaluation: true}
	match := &Query{ValueEvaluation: true, Pattern: query.Pattern, Where: query.InlineWhere, ResidualWhere: query.FullWhere}
	if match.ResidualWhere == nil {
		match.ResidualWhere = query.ResidualWhere
	}
	result.Stages = append(result.Stages, QueryStage{Kind: "match", Query: match})
	for _, unwind := range query.Unwinds {
		result.Stages = append(result.Stages, QueryStage{Kind: "unwind", Query: &Query{ValueEvaluation: true, Unwinds: []Unwind{unwind}}})
	}
	projection := *query
	projection.Pattern = Pattern{}
	projection.Where, projection.InlineWhere = nil, nil
	projection.FullWhere, projection.ResidualWhere = nil, nil
	projection.Unwinds = nil
	result.Stages = append(result.Stages, QueryStage{Kind: "return", Query: &projection})
	return result
}

// Predicate conversion is only for generated inline maps. Full WHERE trees
// already use the shared value-expression reader.
func inlinePatternExpression(predicate Predicate) ValueExpression {
	if predicate == nil {
		return nil
	}
	switch value := predicate.(type) {
	case Comparison:
		var right ValueExpression
		if value.Right.Literal != nil {
			right = LiteralExpression{Value: *value.Right.Literal}
		}
		if value.Right.Parameter != nil {
			right = ParameterExpression{Ref: *value.Right.Parameter}
		}
		return OperationExpression{Operator: value.Operator, Arguments: []ValueExpression{PropertyExpression{Ref: value.Left}, right}, Pos: value.Pos}
	case LogicalOperator:
		var result ValueExpression
		for _, child := range value.Operands {
			expression := inlinePatternExpression(child)
			if result == nil {
				result = expression
			} else {
				result = OperationExpression{Operator: value.Operator, Arguments: []ValueExpression{result, expression}, Pos: value.Pos}
			}
		}
		return result
	}
	return nil
}
