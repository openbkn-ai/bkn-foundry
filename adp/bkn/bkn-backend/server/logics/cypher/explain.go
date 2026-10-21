// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// explain describes a compiled query without running it. It is built from the
// semantic descriptor, which already records the query in the model's terms
// and never its SQL, plus what only the plan knows: the type of each returned
// column, and what the caller should know before running it.
func explain(compiled *compiledQuery) (*interfaces.CypherPlan, error) {
	var descriptor SemanticQueryDescriptor
	if err := json.Unmarshal(compiled.traceDescriptor, &descriptor); err != nil {
		return nil, err
	}
	plan := compiled.plan
	out := &interfaces.CypherPlan{
		Shape:       planShape(plan),
		Graph:       plan.HasGraphValues(),
		Objects:     []interfaces.CypherPlanObject{},
		Columns:     []interfaces.CypherPlanColumn{},
		Limit:       descriptor.EffectiveLimit,
		LimitSource: descriptor.LimitSource,
		Reads:       planReads(plan),
	}
	for _, object := range descriptor.Objects {
		out.Objects = append(out.Objects, interfaces.CypherPlanObject{
			Alias: object.Alias, ObjectTypeID: refName(object.ObjectRef),
		})
	}
	for _, relation := range descriptor.Relations {
		out.Relations = append(out.Relations, interfaces.CypherPlanRelation{
			RelationTypeID: refName(relation.RelationRef),
			Source:         relation.SourceAlias, Target: relation.TargetAlias, Direction: relation.Direction,
		})
	}
	for _, predicate := range descriptor.Predicates {
		properties := predicate.PropertyRefs
		if predicate.PropertyRef != "" {
			properties = append([]string{predicate.PropertyRef}, properties...)
		}
		out.Conditions = append(out.Conditions, interfaces.CypherPlanCondition{
			Operator: predicate.Operator, Expression: predicate.Expression,
			Properties: propertyNames(properties), Where: conditionPlace(predicate.LogicalPath),
		})
	}
	out.Grouping = propertyNames(descriptor.Grouping)
	types := columnTypes(compiled)
	for _, projection := range descriptor.Projections {
		out.Columns = append(out.Columns, interfaces.CypherPlanColumn{
			Name: projection.Alias, Kind: projectionKind(projection), Type: types[projection.Alias],
		})
	}
	for _, order := range descriptor.Ordering {
		out.Ordering = append(out.Ordering, interfaces.CypherPlanOrder{
			Column: order.Alias, Property: propertyName(order.PropertyRef), Descending: order.Descending,
		})
	}
	for _, impact := range compiled.permissionImpacts {
		if impact.RowFilterApplied {
			out.RowFiltered = append(out.RowFiltered, impact.ObjectTypeID)
		}
	}
	out.Warnings = planWarnings(plan, out)
	return out, nil
}

// planShape names the form of a query, most specific first.
func planShape(plan *Plan) string {
	switch {
	case plan.Intersection != nil:
		return "list_comparison"
	case plan.Carried != nil:
		return "carried"
	case len(plan.Totals) > 0:
		return "totals"
	case hasSemiJoin(plan.Where):
		return "semi_join"
	case plan.Grouped || len(plan.GroupBy) > 0 || anyAggregates(plan):
		return "aggregate"
	default:
		return "match"
	}
}

func hasSemiJoin(predicate PlanPredicate) bool {
	switch node := predicate.(type) {
	case PlanSemiJoin:
		return true
	case PlanLogical:
		for _, operand := range node.Operands {
			if hasSemiJoin(operand) {
				return true
			}
		}
	case PlanNegation:
		return hasSemiJoin(node.Operand)
	}
	return false
}

func anyAggregates(plan *Plan) bool {
	for _, column := range plan.Select {
		if column.aggregates() {
			return true
		}
	}
	return false
}

// columnTypes gives each returned column's type. Totals and the sizes of two
// lists are computed over rows the plan's own columns describe, so they are
// typed from what they compute.
func columnTypes(compiled *compiledQuery) map[string]*interfaces.CypherValueType {
	plan := compiled.plan
	integer := &interfaces.CypherValueType{Kind: "integer"}
	types := map[string]*interfaces.CypherValueType{}
	if plan.Intersection != nil {
		for _, item := range plan.Intersection.Items {
			types[item.Alias] = integer
		}
		return types
	}
	selected := graphValueTypes(plan, compiled.schema)
	if len(plan.Totals) == 0 {
		return selected
	}
	for _, total := range plan.Totals {
		switch {
		case total.Function == "COUNT":
			types[total.Alias] = integer
		case total.Function == "AVG":
			types[total.Alias] = &interfaces.CypherValueType{Kind: "float"}
		default:
			types[total.Alias] = selected[total.Of]
		}
	}
	return types
}

// projectionKind names what computes a returned column.
func projectionKind(projection SemanticQueryProjection) string {
	switch {
	case projection.Value != "":
		return projection.Value
	case projection.Aggregate != "" && projection.Expression == semanticIntersection:
		return "list_comparison"
	case projection.Aggregate != "":
		return projection.Aggregate
	case projection.Expression != "":
		return projection.Expression
	default:
		return "property"
	}
}

// planReads lists every property the query reads, its earlier stages
// included, as object_type_id.property.
func planReads(plan *Plan) []string {
	seen := map[string]bool{}
	reads := []string{}
	for _, property := range plan.Properties {
		name := property.ObjectTypeID + "." + property.Property
		if !seen[name] {
			seen[name] = true
			reads = append(reads, name)
		}
	}
	sort.Strings(reads)
	return reads
}

// planWarnings says what the caller should know before running the query.
func planWarnings(plan *Plan, out *interfaces.CypherPlan) []string {
	var warnings []string
	if parts := patternParts(plan); parts > 1 {
		warnings = append(warnings, fmt.Sprintf(
			"the pattern has %d parts that share no node, so every row of one combines with every row of the other; "+
				"connect them with a relationship or a shared node", parts))
	}
	oneRow := plan.Intersection != nil || len(plan.Totals) > 0 ||
		(anyAggregates(plan) && len(plan.GroupBy) == 0 && !plan.Grouped)
	if out.LimitSource == "default" && !oneRow {
		warnings = append(warnings, fmt.Sprintf(
			"no LIMIT: at most %d rows come back; write LIMIT to ask for more (up to %d) or fewer",
			interfaces.CYPHER_DEFAULT_LIMIT, interfaces.CYPHER_MAX_LIMIT))
	}
	if len(out.RowFiltered) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"your access policy narrows the rows of %s, so counts and results cover only the rows you may read",
			strings.Join(out.RowFiltered, ", ")))
	}
	for _, column := range plan.Select {
		if column.Graph != nil && planGraphCollects(column.Graph) {
			warnings = append(warnings, fmt.Sprintf(
				"collect reads its evidence in full; a query whose evidence reaches %d rows is refused, so narrow it with conditions",
				graphRowCap))
			break
		}
	}
	return warnings
}

// patternParts counts the parts of a pattern no relationship connects.
func patternParts(plan *Plan) int {
	if len(plan.Tables) == 0 {
		return 0
	}
	parent := make([]int, len(plan.Tables))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for _, join := range plan.Joins {
		parent[find(join.Left)] = find(join.Right)
	}
	parts := 0
	for i := range parent {
		if find(i) == i {
			parts++
		}
	}
	return parts
}

// refName is the last part of a descriptor reference, which is the id the
// model gives the concept: object:kn:ot_order is ot_order.
func refName(ref string) string {
	if at := strings.LastIndex(ref, ":"); at >= 0 {
		return ref[at+1:]
	}
	return ref
}

// propertyName turns property:kn:ot_order:amount into ot_order.amount.
func propertyName(ref string) string {
	parts := strings.Split(ref, ":")
	if len(parts) < 2 {
		return ref
	}
	return parts[len(parts)-2] + "." + parts[len(parts)-1]
}

func propertyNames(refs []string) []string {
	var names []string
	for _, ref := range refs {
		if ref != "" {
			names = append(names, propertyName(ref))
		}
	}
	return names
}

// conditionPlace keeps where a condition applies and drops its position
// inside AND, OR and NOT: where (the rows), having (the groups),
// optional_match (what an OPTIONAL MATCH matched), each under the prefix of
// the stage it belongs to, as in carried.having.
func conditionPlace(path string) string {
	var kept []string
	for _, segment := range strings.Split(path, ".") {
		name := segment
		if at := strings.Index(name, "["); at >= 0 {
			name = name[:at]
		}
		switch strings.ToUpper(name) {
		case "", "AND", "OR", "NOT":
			continue
		case "OPTIONAL":
			name = "optional_match"
		}
		kept = append(kept, name)
	}
	if len(kept) == 0 || isStagePrefix(kept[len(kept)-1]) {
		kept = append(kept, "where")
	}
	return strings.Join(kept, ".")
}

// isStagePrefix reports a descriptor prefix naming an earlier stage.
func isStagePrefix(segment string) bool {
	return segment == "carried" || strings.HasPrefix(segment, "stage") || strings.HasPrefix(segment, "list")
}
