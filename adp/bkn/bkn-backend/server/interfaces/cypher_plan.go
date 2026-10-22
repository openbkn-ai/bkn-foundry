// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

// CypherPlan says how a query would run, without running it: what it matches,
// what it tests, groups and returns, and what the caller should know before
// running it. It names object types, relation types and properties in the
// model's terms only; the SQL a query becomes is never part of it, for the
// same reason it is not part of a result.
type CypherPlan struct {
	// Shape is the form the query takes: match, aggregate, totals,
	// semi_join, list_comparison or carried.
	Shape string `json:"shape"`
	// Graph says the result holds nodes, relationships or other graph values.
	Graph      bool                  `json:"graph,omitempty"`
	Objects    []CypherPlanObject    `json:"objects"`
	Relations  []CypherPlanRelation  `json:"relations,omitempty"`
	Conditions []CypherPlanCondition `json:"conditions,omitempty"`
	// Grouping lists the properties the result is grouped by, as
	// object_type_id.property.
	Grouping []string           `json:"grouping,omitempty"`
	Columns  []CypherPlanColumn `json:"columns"`
	Ordering []CypherPlanOrder  `json:"ordering,omitempty"`
	// Limit is the most rows the query returns: its own LIMIT, or the
	// default when it wrote none (LimitSource says which).
	Limit       int64  `json:"limit"`
	LimitSource string `json:"limit_source"`
	// Reads lists every property the query reads, as object_type_id.property;
	// each one is authorized before the query runs.
	Reads []string `json:"reads"`
	// RowFiltered lists the object types whose rows the caller's access
	// policy narrows.
	RowFiltered []string `json:"row_filtered,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	// WarningCodes names each warning, in the same order, so a caller can
	// word it in its own language: disconnected_parts, default_limit,
	// row_filtered or collect_cap.
	WarningCodes []string `json:"warning_codes,omitempty"`
}

// CypherPlanObject is one node of a pattern. Alias is its variable; a node of
// an earlier stage carries that stage's prefix, as in stage0.o.
type CypherPlanObject struct {
	Alias        string `json:"alias"`
	ObjectTypeID string `json:"object_type_id"`
}

// CypherPlanRelation is one relationship a pattern walks.
type CypherPlanRelation struct {
	RelationTypeID string `json:"relation_type_id"`
	Source         string `json:"source"`
	Target         string `json:"target"`
	Direction      string `json:"direction"`
}

// CypherPlanCondition is one condition, by operator and the properties it
// reads. Where says where it applies: the rows (where), the groups
// (having), a relationship's match, or an earlier stage.
type CypherPlanCondition struct {
	Operator   string   `json:"operator,omitempty"`
	Expression string   `json:"expression,omitempty"`
	Properties []string `json:"properties,omitempty"`
	Where      string   `json:"where,omitempty"`
}

// CypherPlanColumn is one returned column: its name, what computes it, and
// the type of value it holds.
type CypherPlanColumn struct {
	Name string           `json:"name"`
	Kind string           `json:"kind"`
	Type *CypherValueType `json:"type,omitempty"`
}

// CypherPlanOrder is one sort key, by returned column or property.
type CypherPlanOrder struct {
	Column     string `json:"column,omitempty"`
	Property   string `json:"property,omitempty"`
	Descending bool   `json:"descending,omitempty"`
}
