// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"
	"strings"
)

// PlanCarried is a carried stage: Stage selects, per row the WITH passed on,
// the primary key of each node it passed on and the values it named. The
// statement joins it to the tables of the outer pattern on Keys.
type PlanCarried struct {
	Stage *Plan
	Keys  []PlanCarriedKey
}

// PlanCarriedKey is one primary key column of a passed-on node: the column of
// the outer table, and the alias the stage selects it under.
type PlanCarriedKey struct {
	Table  int
	Column string
	Alias  string
}

// PlanCarriedValue reads a value the carried stage named.
type PlanCarriedValue struct {
	Alias string
}

func (PlanCarriedValue) planExpression() {}

// carriedAlias names the derived table a carried stage becomes.
const carriedAlias = "w"

// planCarried plans the carried stage as a plan of its own, as a semi-join's
// stage is, so what it reads is authorized and filtered like any other part
// of the query. Each passed-on node is selected by its primary key, which is
// what joins the stage's rows to the rows of the outer pattern.
func (p *planner) planCarried(node *CarriedStage) error {
	stage := *node.Stage
	stage.Return = append([]Projection(nil), node.Stage.Return...)
	var keys []PlanCarriedKey
	for i, variable := range node.Nodes {
		table, known := p.tableOf[variable]
		if !known {
			return planErrorf(Position{}, "%s is passed on but not matched again", variable)
		}
		objectType := p.objectType[table]
		if len(objectType.PrimaryKeys) == 0 {
			return planErrorf(Position{}, "object type %q has no primary key, so %s cannot be passed on to the next MATCH",
				objectType.OTID, variable)
		}
		for j, key := range objectType.PrimaryKeys {
			column, err := p.resolvePropertyColumn(table, key, Position{})
			if err != nil {
				return err
			}
			alias := fmt.Sprintf("_key_%d_%d", i, j)
			stage.Return = append(stage.Return, Projection{
				Property: &PropertyRef{Variable: variable, Property: key}, Alias: alias,
			})
			keys = append(keys, PlanCarriedKey{Table: table, Column: column, Alias: alias})
		}
	}
	sub, err := Compile(&stage, p.schema, CompileOptions{Parameters: p.parameters})
	if err != nil {
		return err
	}
	p.plan.Properties = append(p.plan.Properties, sub.Properties...)
	p.plan.Carried = &PlanCarried{Stage: sub, Keys: keys}
	p.plan.SQLOnly = true
	p.carriedKinds = map[string]numberKind{}
	for _, column := range sub.Select {
		p.carriedKinds[column.Alias] = p.carriedKind(sub, column)
	}
	return nil
}

// carriedKind is what kind of number a value the stage named is, as far as
// arithmetic after it needs to know.
func (p *planner) carriedKind(sub *Plan, column PlanColumn) numberKind {
	propertyKind := func(table int, property string) numberKind {
		objectType := p.schema.objectTypesByID[sub.Tables[table].ObjectTypeID]
		if objectType == nil {
			return kindUnknown
		}
		for _, dp := range objectType.DataProperties {
			if dp == nil || dp.Name != property || dp.Type == "" {
				continue
			}
			lowered := strings.ToLower(dp.Type)
			switch {
			case isIn(integerTypes, lowered):
				return kindInteger
			case isIn(floatTypes, lowered):
				return kindFloat
			default:
				return kindNotNumber
			}
		}
		return kindUnknown
	}
	switch {
	case column.Aggregate != nil && column.Aggregate.Function == "COUNT":
		return kindInteger
	case column.Aggregate != nil && column.Aggregate.Function == "AVG":
		return kindFloat
	case column.Aggregate != nil && column.Aggregate.Argument == nil && !column.Aggregate.Star:
		return propertyKind(column.Aggregate.Table, column.Aggregate.Property)
	case column.Aggregate == nil && column.Expression == nil && column.Graph == nil && column.Property != "":
		return propertyKind(column.Table, column.Property)
	default:
		return kindUnknown
	}
}

// ungroupedCarried lists the carried values a computed value reads outside
// any aggregate. Like a property there, each must be returned on its own to
// be a grouping key.
func ungroupedCarried(value PlanExpression) []string {
	switch node := value.(type) {
	case PlanCarriedValue:
		return []string{node.Alias}
	case PlanArithmetic:
		return append(ungroupedCarried(node.Left), ungroupedCarried(node.Right)...)
	case PlanNegative:
		return ungroupedCarried(node.Operand)
	case PlanCase, PlanCoalesce:
		var names []string
		for _, part := range caseValues(value) {
			names = append(names, ungroupedCarried(part)...)
		}
		return names
	default:
		return nil
	}
}

// carriedProjected reports whether RETURN returns a carried value on its own.
func (p *planner) carriedProjected(name string) bool {
	for _, column := range p.plan.Select {
		if carried, ok := column.Expression.(PlanCarriedValue); ok && carried.Alias == name {
			return true
		}
	}
	return false
}
