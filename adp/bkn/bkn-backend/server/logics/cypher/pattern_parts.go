// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

// patternComponents gives each table of a pattern the part it belongs to:
// tables a relationship or an equality between their properties connects
// share a part. An equality connects only when every row must meet it, so
// one under OR or NOT does not. Nodes a WITH passed on are joined to the
// same row of the carried stage, so they share a part too.
func patternComponents(plan *Plan) []int {
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
	for _, term := range conjuncts(plan.Where) {
		if node, ok := term.(PlanColumnComparison); ok && node.Operator == "=" && node.LeftTable != node.RightTable {
			parent[find(node.LeftTable)] = find(node.RightTable)
		}
	}
	if plan.Carried != nil {
		for _, key := range plan.Carried.Keys {
			parent[find(key.Table)] = find(plan.Carried.Keys[0].Table)
		}
	}
	for i := range parent {
		parent[i] = find(i)
	}
	return parent
}

// patternParts counts the parts of a pattern that neither a relationship nor
// an equality between their properties connects.
func patternParts(plan *Plan) int {
	parts := map[int]bool{}
	for _, part := range patternComponents(plan) {
		parts[part] = true
	}
	return len(parts)
}

// unnarrowedParts counts the parts of a pattern that no condition narrows. A
// part is narrowed by a condition every row must meet that reads its nodes
// and no others, such as an inline property or a WHERE term under AND.
//
// Rows of parts that nothing connects combine pairwise. One part read whole
// against narrowed ones is a listing the query asks for; two parts read whole
// are every row of one table against every row of another, which is refused.
func unnarrowedParts(plan *Plan) int {
	part := patternComponents(plan)
	narrowed := map[int]bool{}
	// The carried stage's rows bound the nodes passed on, but only when that
	// stage is itself narrowed: one read whole passes whole tables on.
	if plan.Carried != nil && stageNarrowed(plan.Carried.Stage) {
		for _, key := range plan.Carried.Keys {
			narrowed[part[key.Table]] = true
		}
	}
	for _, term := range conjuncts(plan.Where) {
		tables := predicateTables(term)
		if len(tables) == 0 {
			continue
		}
		own := part[tables[0]]
		for _, table := range tables[1:] {
			if part[table] != own {
				own = -1
				break
			}
		}
		if own >= 0 {
			narrowed[own] = true
		}
	}
	open := map[int]bool{}
	for _, p := range part {
		if !narrowed[p] {
			open[p] = true
		}
	}
	return len(open)
}

// stageNarrowed says an earlier stage yields fewer rows than its tables
// hold: every part of its pattern is narrowed, or its groups must meet a
// condition.
func stageNarrowed(stage *Plan) bool {
	return stage != nil && (unnarrowedParts(stage) == 0 || stage.Having != nil)
}

// crossProduct reports a plan, or a stage it reads first, whose pattern has
// two parts that nothing connects and no condition narrows.
func crossProduct(plan *Plan) bool {
	if plan == nil {
		return false
	}
	if patternParts(plan) > 1 && unnarrowedParts(plan) > 1 {
		return true
	}
	if plan.Carried != nil && crossProduct(plan.Carried.Stage) {
		return true
	}
	if plan.Intersection != nil && (crossProduct(plan.Intersection.Lists[0]) || crossProduct(plan.Intersection.Lists[1])) {
		return true
	}
	if plan.Union != nil {
		for _, part := range plan.Union.Parts {
			if crossProduct(part) {
				return true
			}
		}
	}
	found := false
	var walk func(PlanPredicate)
	walk = func(predicate PlanPredicate) {
		switch node := predicate.(type) {
		case PlanSemiJoin:
			found = found || crossProduct(node.Sub)
		case PlanLogical:
			for _, operand := range node.Operands {
				walk(operand)
			}
		}
	}
	walk(plan.Where)
	return found
}
