// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// PlanUnion is UNION or UNION ALL: each part a plan of its own, compiled,
// authorized and filtered like any query. vega-backend runs one SELECT at a
// time, so each part runs as the query it would be on its own -- on SQL, as
// graph values or through the data API -- and the rows are put together.
type PlanUnion struct {
	Parts []*Plan
	All   bool
}

// compileUnion plans each part on its own. The parts must return the same
// columns in the same order, as openCypher asks, and return values: a
// constant, such as 'open' AS kind, is one, while nodes, relationships and
// collect are not. Each part keeps its ORDER BY, SKIP and LIMIT, so the query
// returns at most what its parts do together.
func compileUnion(node *Union, schema *Schema, options CompileOptions) (*Plan, error) {
	if len(options.ObjectProperties) > 0 {
		return nil, planErrorf(Position{}, "object_properties names properties of returned nodes; a UNION returns values only")
	}
	union := &PlanUnion{All: node.All}
	plan := &Plan{NetworkID: schema.KNID, Branch: schema.Branch, Union: union}
	var rows int64
	for i, part := range node.Parts {
		sub, err := Compile(part, schema, CompileOptions{Parameters: options.Parameters})
		if err != nil {
			return nil, err
		}
		position := Position{}
		if len(part.Return) > 0 {
			position = part.Return[0].Pos
		}
		for _, column := range sub.Select {
			if _, constant := column.Graph.(PlanGraphConstant); column.Graph != nil && !constant {
				return nil, planErrorf(position, "a UNION of nodes, relationships or collect is not supported; "+
					"return their properties, as in RETURN n.name AS name")
			}
		}
		if i > 0 {
			if want, got := unionColumns(union.Parts[0]), unionColumns(sub); want != got {
				return nil, planErrorf(position, "every part of a UNION returns the same columns in the same order: "+
					"the first returns %s, this one %s; name them alike with AS", want, got)
			}
		}
		plan.Properties = append(plan.Properties, sub.Properties...)
		union.Parts = append(union.Parts, sub)
		rows += int64(pageSize(sub.Limit))
	}
	if rows > interfaces.CYPHER_MAX_LIMIT {
		rows = interfaces.CYPHER_MAX_LIMIT
	}
	plan.Limit = &rows
	return plan, nil
}

// unionColumns names the columns a part returns, in order.
func unionColumns(plan *Plan) string {
	names := make([]string, 0, len(plan.Select)+len(plan.Totals))
	for _, column := range plan.Select {
		names = append(names, column.Alias)
	}
	for _, total := range plan.Totals {
		names = append(names, total.Alias)
	}
	return "(" + strings.Join(names, ", ") + ")"
}

// semanticUnionDescriptor describes a UNION by its first part, which names
// the columns, with every further part recorded as a stage of its own.
func semanticUnionDescriptor(plan *Plan, query string) (*SemanticQueryDescriptor, error) {
	descriptor, err := BuildSemanticQueryDescriptor(plan.Union.Parts[0], query)
	if err != nil {
		return nil, err
	}
	explicit := true
	for i, part := range plan.Union.Parts {
		explicit = explicit && part.Limit != nil
		if i == 0 {
			continue
		}
		if err := appendSemanticStage(descriptor, part, fmt.Sprintf("union%d.", i)); err != nil {
			return nil, err
		}
	}
	descriptor.Limit = cloneInt64(plan.Limit)
	descriptor.EffectiveLimit = *plan.Limit
	descriptor.LimitSource = "default"
	if explicit {
		descriptor.LimitSource = "explicit"
	}
	return descriptor, nil
}
