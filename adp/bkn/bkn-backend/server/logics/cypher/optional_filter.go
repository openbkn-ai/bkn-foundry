// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

// An optional filter is a condition the caller may leave out, written
//
//	WHERE ($code IS NULL OR s.code = $code)
//
// so a statement saved as a function filters by a value when the caller passes
// one and reads every row when they pass null or nothing. Whether a parameter
// is null is known before the query is planned, so the test is decided then
// and the condition it sits in is simplified: the query that reaches a data
// source is either `s.code = 'S1'` or no condition at all, never an OR that
// defeats an index.

// ParameterNullCheck is $name IS NULL, or IS NOT NULL when negated. It is
// accepted in the WHERE of a MATCH or an OPTIONAL MATCH, and bindNullChecks
// replaces it before planning, so no later stage ever meets it.
type ParameterNullCheck struct {
	Parameter ParameterRef
	Negated   bool
	Pos       Position
}

func (n ParameterNullCheck) predicatePosition() Position { return n.Pos }

// NeverMatches is a condition decided false before planning, planned as
// PlanNever, which every executor knows.
type NeverMatches struct {
	Pos Position
}

func (n NeverMatches) predicatePosition() Position { return n.Pos }

// bindNullChecks decides every $name IS NULL in the query from the supplied
// parameters. A parameter that was not supplied counts as null, so a caller
// can leave an optional value out rather than send null for it.
func bindNullChecks(query *Query, parameters map[string]any) error {
	if query == nil {
		return nil
	}
	if query.Union != nil {
		for _, part := range query.Union.Parts {
			if err := bindNullChecks(part, parameters); err != nil {
				return err
			}
		}
	}
	if query.Intersection != nil {
		for _, list := range query.Intersection.Lists {
			if err := bindNullChecks(list, parameters); err != nil {
				return err
			}
		}
	}
	if query.Carried != nil {
		if err := bindNullChecks(query.Carried.Stage, parameters); err != nil {
			return err
		}
	}
	where, err := decideCondition(query.Where, parameters, false)
	if err != nil {
		return err
	}
	query.Where = where
	having, err := decideCondition(query.Having, parameters, true)
	if err != nil {
		return err
	}
	query.Having = having
	for i := range query.Pattern.Edges {
		edge := &query.Pattern.Edges[i]
		conditions := edge.Conditions[:0:0]
		for _, condition := range edge.Conditions {
			decided, err := decideCondition(condition, parameters, false)
			if err != nil {
				return err
			}
			if decided != nil {
				conditions = append(conditions, decided)
			}
		}
		edge.Conditions = conditions
	}
	return nil
}

// refuseBareNullChecks refuses a WHERE made only of parameter null checks. It
// reads no property, so it would keep every row or none depending on what the
// caller passed, which is a switch rather than a filter.
func refuseBareNullChecks(where Predicate) error {
	if where == nil || !hasParameterNullCheck(where) || len(predicateProperties(where)) > 0 {
		return nil
	}
	return unsupportedAt(where.predicatePosition(), "a WHERE that only tests whether parameters are null",
		"write the test beside the condition it makes optional, as in ($name IS NULL OR n.property = $name)")
}

// refuseParameterNullChecks refuses a parameter null check in a condition that
// is a value, as in RETURN or CASE, where nothing decides it before planning.
func refuseParameterNullChecks(predicate Predicate) error {
	switch node := predicate.(type) {
	case ParameterNullCheck:
		name := node.Parameter.Name
		return unsupportedAt(node.Pos, "testing a parameter for null outside WHERE",
			"$%s IS NULL makes a condition optional, so write it in a WHERE, as in ($%s IS NULL OR n.property = $%s)",
			name, name, name)
	case Negation:
		return refuseParameterNullChecks(node.Operand)
	case LogicalOperator:
		for _, operand := range node.Operands {
			if err := refuseParameterNullChecks(operand); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasParameterNullCheck(predicate Predicate) bool {
	switch node := predicate.(type) {
	case ParameterNullCheck:
		return true
	case Negation:
		return hasParameterNullCheck(node.Operand)
	case LogicalOperator:
		for _, operand := range node.Operands {
			if hasParameterNullCheck(operand) {
				return true
			}
		}
	}
	return false
}

// decideCondition returns the condition with its null checks decided: nil
// when it holds for every row, and a condition that matches nothing when it
// holds for none. grouped says the condition is a WITH's, tested on groups.
func decideCondition(predicate Predicate, parameters map[string]any, grouped bool) (Predicate, error) {
	if predicate == nil {
		return nil, nil
	}
	residual, constant := foldNullChecks(predicate, parameters)
	if constant == nil {
		return residual, nil
	}
	if *constant {
		return nil, nil
	}
	// A condition that can match no row keeps one of the properties it reads,
	// so it is still a condition on that node: an OPTIONAL MATCH then finds no
	// node rather than dropping the row, as the condition written in full
	// would.
	// A condition on groups reads what the WITH computed rather than one
	// node's property.
	pos := predicate.predicatePosition()
	properties := predicateProperties(predicate)
	if grouped || len(properties) == 0 {
		return NeverMatches{Pos: pos}, nil
	}
	return Membership{Property: properties[0], Pos: pos}, nil
}

// foldNullChecks simplifies a condition once its null checks are known. It
// returns the condition left over, or, when the condition no longer depends on
// the row, whether it holds.
func foldNullChecks(predicate Predicate, parameters map[string]any) (Predicate, *bool) {
	switch node := predicate.(type) {
	case ParameterNullCheck:
		value, supplied := parameters[node.Parameter.Name]
		isNull := !supplied || value == nil
		holds := isNull != node.Negated
		return nil, &holds
	case Negation:
		operand, constant := foldNullChecks(node.Operand, parameters)
		if constant != nil {
			holds := !*constant
			return nil, &holds
		}
		return Negation{Operand: operand, Pos: node.Pos}, nil
	case LogicalOperator:
		// AND is decided by a false operand, OR by a true one; an operand
		// with the other value drops out.
		decides := node.Operator == "OR"
		operands := make([]Predicate, 0, len(node.Operands))
		for _, operand := range node.Operands {
			residual, constant := foldNullChecks(operand, parameters)
			if constant == nil {
				operands = append(operands, residual)
				continue
			}
			if *constant == decides {
				return nil, &decides
			}
		}
		switch len(operands) {
		case 0:
			holds := !decides
			return nil, &holds
		case 1:
			return operands[0], nil
		}
		return LogicalOperator{Operator: node.Operator, Operands: operands, Pos: node.Pos}, nil
	default:
		return predicate, nil
	}
}
