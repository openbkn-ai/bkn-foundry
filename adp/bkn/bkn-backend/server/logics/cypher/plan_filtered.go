// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A filtered_cross_join relation has no keys: each end is narrowed by a
// condition of its own, and every source row that passes is linked to every
// target row that passes. It is planned as a join whose condition is the two
// side conditions together.
//
// The stored conditions use the platform's filter operations. Each one is
// translated to the predicate vega-backend writes for it on a SQL source, so
// the rows Cypher links are the rows the subgraph query links: == is =, != is
// <>, like is a substring LIKE, prefix a leading one, range and between are
// inclusive on both bounds, out_range is outside both. An operation with no
// such translation is refused by name rather than approximated.

// filteredCrossJoinMapping reads a filtered_cross_join relation's conditions.
func filteredCrossJoinMapping(relationType *interfaces.RelationType) (*interfaces.FilteredCrossJoinMapping, error) {
	var rules *interfaces.FilteredCrossJoinMapping
	switch value := relationType.MappingRules.(type) {
	case *interfaces.FilteredCrossJoinMapping:
		rules = value
	case interfaces.FilteredCrossJoinMapping:
		rules = &value
	}
	if rules == nil {
		return nil, fmt.Errorf("relation type %q has no conditions to join on", relationType.RTName)
	}
	// Either condition may be left out: that end is not narrowed, as in the
	// subgraph query.
	return rules, nil
}

// addFilteredCrossJoin plans one relationship over a filtered_cross_join
// relation type.
func (p *planner) addFilteredCrossJoin(edge EdgeRef, relationType *interfaces.RelationType, left, right int) error {
	rules, err := filteredCrossJoinMapping(relationType)
	if err != nil {
		return &PlanError{Pos: edge.Pos, Err: err}
	}

	var readForwards []bool
	for _, forwards := range p.readings(edge.Direction) {
		source, target := left, right
		if !forwards {
			source, target = right, left
		}
		if p.objectType[source].OTID == relationType.SourceObjectTypeID &&
			p.objectType[target].OTID == relationType.TargetObjectTypeID {
			readForwards = append(readForwards, forwards)
		}
	}
	switch len(readForwards) {
	case 0:
		return planErrorf(edge.Pos,
			"relation type %q goes from %q to %q, which does not connect %q and %q the way this pattern reads it",
			relationType.RTName, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID,
			p.objectType[left].OTID, p.objectType[right].OTID)
	case 1:
	default:
		// Each reading puts the source condition on a different node, and a
		// row would match if either held; the author knows which way it runs.
		return planErrorf(edge.Pos,
			"relation type %q links %q to itself by conditions, so an undirected pattern over it is ambiguous; write its direction",
			relationType.RTName, relationType.SourceObjectTypeID)
	}
	source, target := left, right
	if !readForwards[0] {
		source, target = right, left
	}

	sourceFilter, err := p.relationCondition(relationType, rules.SourceCondition, source)
	if err != nil {
		return &PlanError{Pos: edge.Pos, Err: err}
	}
	targetFilter, err := p.relationCondition(relationType, rules.TargetCondition, target)
	if err != nil {
		return &PlanError{Pos: edge.Pos, Err: err}
	}

	join := PlanJoin{
		Left: left, Right: right, RelationTypeID: relationType.RTID, Direction: edge.Direction,
		Optional: edge.Optional, OptionalTable: edge.OptionalNode, Variable: edge.Variable,
		Filter: combineRowFilterAnd([]PlanPredicate{sourceFilter, targetFilter}),
		// A join with no condition at all still has to say so.
		Filtered: true,
	}
	if edge.Direction != Undirected {
		p.hops = append(p.hops, plannedHop{
			relationType: relationType.RTID,
			source:       source,
			target:       target,
			clause:       edge.Clause,
			optional:     edge.Optional,
			pos:          edge.Pos,
		})
	}
	if edge.Variable != "" {
		if err := p.bindRelationship(edge, relationType, nil, readForwards, left, right); err != nil {
			return err
		}
		variable := p.relationshipOf[edge.Variable]
		variable.digest = filteredRelationDigest(relationType, rules)
		p.relationshipOf[edge.Variable] = variable
	}
	for _, condition := range edge.Conditions {
		planned, err := p.planPredicate(condition)
		if err != nil {
			return err
		}
		join.Conditions = append(join.Conditions, planned)
	}
	p.plan.Joins = append(p.plan.Joins, join)
	// The conditions are written as SQL; the bind-join executor takes no new
	// capabilities.
	p.plan.SQLOnly = true
	return nil
}

// relationCondition translates one side's stored condition into a predicate
// on table. A nil predicate holds for every row.
func (p *planner) relationCondition(relationType *interfaces.RelationType, condition *cond.CondCfg,
	table int) (PlanPredicate, error) {
	translated, err := p.translateCondition(condition, table)
	if err != nil {
		return nil, fmt.Errorf("relation type %q: %w", relationType.RTName, err)
	}
	return translated, nil
}

func (p *planner) translateCondition(condition *cond.CondCfg, table int) (PlanPredicate, error) {
	if condition == nil {
		return nil, nil
	}
	operation := strings.ToLower(strings.TrimSpace(condition.Operation))
	switch operation {
	case cond.OperationAnd, cond.OperationOr:
		operands := make([]PlanPredicate, 0, len(condition.SubConds))
		for _, sub := range condition.SubConds {
			translated, err := p.translateCondition(sub, table)
			if err != nil {
				return nil, err
			}
			operands = append(operands, translated)
		}
		if operation == cond.OperationAnd {
			return combineRowFilterAnd(operands), nil
		}
		return combineConditionOr(operands), nil
	}

	objectType := p.objectType[table]
	property := strings.TrimSpace(condition.Field)
	if property == "" {
		return nil, fmt.Errorf("the %q condition names no property", operation)
	}
	column, err := p.schema.Column(objectType, property)
	if err != nil {
		return nil, err
	}
	switch operation {
	case cond.OperationNull, cond.OperationNotExist:
		// A null check reads no value, so it holds for a property of any type.
		return PlanNullCheck{Table: table, Column: column, Property: property}, nil
	case cond.OperationNotNull, cond.OperationExist:
		return PlanNullCheck{Table: table, Column: column, Property: property, Negated: true}, nil
	}
	declared, _ := p.dataPropertyType(table, property)
	family := valueFamily(declared)
	if family == "date" {
		// vega-backend normalizes date values per source before comparing;
		// that conversion is not one the generated SQL can reproduce.
		return nil, fmt.Errorf("the %q condition on %q compares a date or time property, which a Cypher pattern cannot apply yet",
			operation, property)
	}
	if from := strings.TrimSpace(condition.ValueFrom); from != "" && from != cond.ValueFrom_Const {
		return nil, fmt.Errorf("the %q condition on %q takes its value from %q; only constant values are supported",
			operation, property, from)
	}
	value := func() (Literal, error) { return conditionLiteral(condition.Value, family, property) }
	pair := func() (Literal, Literal, error) {
		values, err := conditionLiterals(condition.Value, family, property)
		if err != nil {
			return Literal{}, Literal{}, err
		}
		if len(values) != 2 {
			return Literal{}, Literal{}, fmt.Errorf("the %q condition on %q needs two values", operation, property)
		}
		return values[0], values[1], nil
	}
	text := func() (string, error) {
		if family != "string" {
			return "", fmt.Errorf("the %q condition on %q needs a string property", operation, property)
		}
		literal, err := value()
		if err != nil {
			return "", err
		}
		return literal.String, nil
	}
	compare := func(operator string) (PlanPredicate, error) {
		literal, err := value()
		if err != nil {
			return nil, err
		}
		return PlanCondition{Table: table, Column: column, Property: property, Operator: operator, Value: literal}, nil
	}
	match := func(operator StringMatchOperator, negated bool) (PlanPredicate, error) {
		needle, err := text()
		if err != nil {
			return nil, err
		}
		if operation == cond.OperationLike || operation == cond.OperationNotLike {
			// like is a literal substring with the platform's escapes: \% and
			// \_ stand for the characters, and a bare % is refused, as every
			// other path refuses it.
			if needle, err = cond.ParseLikeValue(operation, needle); err != nil {
				return nil, fmt.Errorf("property %q: %w", property, err)
			}
		}
		var predicate PlanPredicate = PlanStringMatch{
			Table: table, Column: column, Property: property, Operator: operator, Value: needle,
		}
		if negated {
			predicate = PlanNegation{Operand: predicate}
		}
		return predicate, nil
	}
	bounds := func(lower, upper, junction string) (PlanPredicate, error) {
		low, high, err := pair()
		if err != nil {
			return nil, err
		}
		return PlanLogical{Operator: junction, Operands: []PlanPredicate{
			PlanCondition{Table: table, Column: column, Property: property, Operator: lower, Value: low},
			PlanCondition{Table: table, Column: column, Property: property, Operator: upper, Value: high},
		}}, nil
	}
	empty := func(operator string) (PlanPredicate, error) {
		if family != "string" {
			return nil, fmt.Errorf("the %q condition on %q needs a string property", operation, property)
		}
		return PlanCondition{Table: table, Column: column, Property: property, Operator: operator,
			Value: Literal{Kind: LiteralString}}, nil
	}

	switch operation {
	case cond.OperationEq, "=":
		return compare("=")
	case cond.OperationNotEq:
		return compare("<>")
	case cond.OperationGt, cond.OperationGte, cond.OperationLt, cond.OperationLte:
		return compare(operation)
	case cond.OperationIn, cond.OperationNotIn:
		values, err := conditionLiterals(condition.Value, family, property)
		if err != nil {
			return nil, err
		}
		return PlanMembership{Table: table, Column: column, Property: property, Values: values,
			Negated: operation == cond.OperationNotIn}, nil
	case cond.OperationLike:
		return match(Contains, false)
	case cond.OperationNotLike:
		return match(Contains, true)
	case cond.OperationPrefix:
		return match(StartsWith, false)
	case cond.OperationNotPrefix:
		return match(StartsWith, true)
	case cond.OperationRange, cond.OperationBetween:
		return bounds(">=", "<=", "AND")
	case cond.OperationOutRange:
		return bounds("<", ">", "OR")
	case cond.OperationEmpty:
		return empty("=")
	case cond.OperationNotEmpty:
		return empty("<>")
	default:
		return nil, fmt.Errorf("the %q condition on %q is not supported in a Cypher pattern", operation, property)
	}
}

// combineConditionOr joins alternatives; one that holds for every row makes
// the whole condition hold.
func combineConditionOr(operands []PlanPredicate) PlanPredicate {
	kept := make([]PlanPredicate, 0, len(operands))
	for _, operand := range operands {
		if operand == nil {
			return nil
		}
		if _, never := operand.(PlanNever); !never {
			kept = append(kept, operand)
		}
	}
	switch len(kept) {
	case 0:
		return PlanNever{}
	case 1:
		return kept[0]
	default:
		return PlanLogical{Operator: "OR", Operands: kept}
	}
}

// conditionLiterals reads a condition's value list.
func conditionLiterals(value any, family, property string) ([]Literal, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("the condition on %q needs a list of values", property)
	}
	literals := make([]Literal, 0, len(list))
	for _, item := range list {
		literal, err := conditionLiteral(item, family, property)
		if err != nil {
			return nil, err
		}
		literals = append(literals, literal)
	}
	return literals, nil
}

// conditionLiteral reads one condition value as the property's type, so the
// generated comparison is between like kinds.
func conditionLiteral(value any, family, property string) (Literal, error) {
	mismatch := fmt.Errorf("the condition on %q compares it with %v, which is not a %s", property, value, family)
	switch family {
	case "string":
		if text, ok := value.(string); ok {
			return Literal{Kind: LiteralString, String: text}, nil
		}
	case "boolean":
		if flag, ok := value.(bool); ok {
			return Literal{Kind: LiteralBoolean, Boolean: flag}, nil
		}
	case "number":
		number, ok := conditionNumber(value)
		if !ok {
			return Literal{}, mismatch
		}
		if number == math.Trunc(number) && math.Abs(number) < 1<<53 {
			return Literal{Kind: LiteralInteger, Integer: int64(number)}, nil
		}
		return Literal{Kind: LiteralFloat, Float: number}, nil
	default:
		return Literal{}, fmt.Errorf("the condition on %q is on a property whose type is not known", property)
	}
	return Literal{}, mismatch
}

func conditionNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		return parsed, err == nil
	}
	return 0, false
}

// filteredRelationDigest names a filtered_cross_join binding: its two
// conditions and when the model last changed it.
func filteredRelationDigest(relationType *interfaces.RelationType, rules *interfaces.FilteredCrossJoinMapping) string {
	source, _ := json.Marshal(rules.SourceCondition)
	target, _ := json.Marshal(rules.TargetCondition)
	var b strings.Builder
	b.WriteString(relationType.RTID)
	b.WriteString("|source=" + string(source))
	b.WriteString("|target=" + string(target))
	b.WriteString("|" + strconv.FormatInt(relationType.UpdateTime, 10))
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}
