// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"sort"
	"strings"
)

// MisplacedParameter is a stored parameter whose value source sits in Source, the way the legacy
// parameter table (Parameter | Type | Source | Binding) wrote it. The 2.x reader stored Binding as
// ValueFrom, so execution drops the parameter. Suggested is the rewrite the 3.0 refusal shows for
// the same row (legacyBindingParameter). Conflict means the parameter already has a Value that the
// rewrite would replace, so a person has to choose.
type MisplacedParameter struct {
	// Exactly one of ActionTypeID and ObjectTypeID is set; LogicProperty goes with ObjectTypeID.
	ActionTypeID  string
	ObjectTypeID  string
	LogicProperty string

	Parameter string
	Current   Parameter
	Suggested Parameter
	Conflict  bool
}

// InvalidParameter is a stored parameter whose ValueFrom is not property, input or const and whose
// Source does not hold one either, so there is no rewrite to suggest. Execution drops it, and a 3.0
// package refuses it.
type InvalidParameter struct {
	ActionTypeID  string
	ObjectTypeID  string
	LogicProperty string

	Parameter string
	Current   Parameter
}

// FindMisplacedParameters checks the parameters of every action type and every logic property of
// a network, of any bkn_spec, and returns the misplaced ones: action types first, then object types,
// each by id, parameters in their order.
func FindMisplacedParameters(doc *BknNetwork) []MisplacedParameter {
	var found []MisplacedParameter
	eachParameter(doc, func(at, ot, lp string, p Parameter) {
		if m, ok := misplacedParameter(p); ok {
			m.ActionTypeID, m.ObjectTypeID, m.LogicProperty = at, ot, lp
			found = append(found, m)
		}
	})
	return found
}

// FindInvalidParameters returns the parameters a 3.0 package refuses that FindMisplacedParameters
// has no rewrite for, in the same order. Together the two are every parameter a 3.0 import refuses.
func FindInvalidParameters(doc *BknNetwork) []InvalidParameter {
	var found []InvalidParameter
	eachParameter(doc, func(at, ot, lp string, p Parameter) {
		if hasValueSource(p.ValueFrom) || hasValueSource(p.Source) {
			return
		}
		found = append(found, InvalidParameter{ActionTypeID: at, ObjectTypeID: ot, LogicProperty: lp, Parameter: p.Name, Current: p})
	})
	return found
}

// eachParameter visits the parameters of the action types, then of the logic properties of the
// object types, each by id.
func eachParameter(doc *BknNetwork, visit func(actionTypeID, objectTypeID, logicProperty string, p Parameter)) {
	actions := append([]*BknActionType(nil), doc.ActionTypes...)
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].ID < actions[j].ID })
	for _, at := range actions {
		for _, p := range at.Parameters {
			visit(at.ID, "", "", p)
		}
	}
	objects := append([]*BknObjectType(nil), doc.ObjectTypes...)
	sort.SliceStable(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })
	for _, ot := range objects {
		for _, lp := range ot.LogicProperties {
			if lp == nil {
				continue
			}
			for _, p := range lp.Parameters {
				visit("", ot.ID, lp.Name, p)
			}
		}
	}
}

func hasValueSource(s string) bool {
	return parameterValueSources[strings.ToLower(strings.TrimSpace(s))]
}

// misplacedParameter rebuilds the legacy row a stored parameter came from and rewrites it the way
// the 3.0 refusal does. A parameter whose ValueFrom already holds a value source is accepted by 3.0
// and is not misplaced, whatever its Source says.
func misplacedParameter(p Parameter) (MisplacedParameter, bool) {
	if hasValueSource(p.ValueFrom) {
		return MisplacedParameter{}, false
	}
	row := map[string]string{
		"Source":      p.Source,
		"Binding":     p.ValueFrom,
		"Type":        p.Type,
		"Operation":   p.Operation,
		"Description": p.Description,
	}
	suggested, ok := legacyBindingParameter(row, p.Name)
	if !ok {
		return MisplacedParameter{}, false
	}
	return MisplacedParameter{
		Parameter: p.Name,
		Current:   p,
		Suggested: suggested,
		Conflict:  p.Value != nil && strings.TrimSpace(fmt.Sprint(p.Value)) != "",
	}, true
}

// HasV3Declarations reports whether a network carries a declaration that only a 3.0 package can
// hold: a property declaration, an alias, a lifecycle, a relation declaration or a value type. A
// 2.x export would drop it, so such a network is exported under 3.0. Required on a primary key is
// implied and does not count.
func HasV3Declarations(doc *BknNetwork) bool {
	if len(doc.ValueTypes) > 0 {
		return true
	}
	for _, ot := range doc.ObjectTypes {
		if len(ot.Aliases) > 0 || ot.Lifecycle != nil || hasPropertyDeclarations(ot.DataProperties, ot.PrimaryKeys) {
			return true
		}
	}
	for _, at := range doc.ActionTypes {
		if len(at.Aliases) > 0 || at.Lifecycle != nil {
			return true
		}
	}
	for _, rt := range doc.RelationTypes {
		if len(rt.Aliases) > 0 || rt.Lifecycle != nil || rt.ReverseName != "" || rt.Importance != "" || rt.FallbackFor != "" ||
			rt.Verification != "" || rt.Endpoint.Cardinality != "" || len(rt.RequiredEnds) > 0 || len(rt.Characteristics) > 0 ||
			len(rt.DataProperties) > 0 {
			return true
		}
	}
	return false
}

func hasPropertyDeclarations(props []*DataProperty, primaryKeys []string) bool {
	for _, p := range props {
		if p == nil {
			continue
		}
		if p.Semantic != "" || p.Constraints != nil || len(p.Aliases) > 0 || (p.Required && !containsString(primaryKeys, p.Name)) {
			return true
		}
	}
	return false
}
