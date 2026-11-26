// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// bindEventInputs gives the inputs of a handling action the values its event class binds them
// to, for this execution only: a constant, or a property of the object the action runs on, which
// the parameters then read as any other property. A binding must name an input of the action.
func bindEventInputs(actionType *interfaces.ActionType, bindings map[string]interfaces.ActionInputBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	// The definition may be shared; the bound parameters are this execution's own.
	parameters := make([]interfaces.Parameter, len(actionType.Parameters))
	copy(parameters, actionType.Parameters)
	used := make(map[string]bool, len(bindings))
	for i := range parameters {
		binding, ok := bindings[parameters[i].Name]
		if !ok {
			continue
		}
		name := parameters[i].Name
		if parameters[i].ValueFrom != interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT {
			return fmt.Errorf("parameter %q of action type %s is not an input; only an input can be bound", name, actionType.ATID)
		}
		switch binding.ValueFrom {
		case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
			if binding.Value == nil {
				return fmt.Errorf("input %q is bound to a constant without a value", name)
			}
			parameters[i].ValueFrom = interfaces.LOGIC_PARAMS_VALUE_FROM_CONST
			parameters[i].Value = binding.Value
		case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
			property, _ := binding.Value.(string)
			if strings.TrimSpace(property) == "" {
				return fmt.Errorf("input %q is bound to a property without its name", name)
			}
			parameters[i].ValueFrom = interfaces.LOGIC_PARAMS_VALUE_FROM_PROP
			parameters[i].Value = strings.TrimSpace(property)
		default:
			return fmt.Errorf("input %q: value_from of a binding must be %s or %s", name,
				interfaces.LOGIC_PARAMS_VALUE_FROM_CONST, interfaces.LOGIC_PARAMS_VALUE_FROM_PROP)
		}
		used[name] = true
	}
	var unknown []string
	for name := range bindings {
		if !used[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("action type %s has no input %s", actionType.ATID, strings.Join(unknown, ", "))
	}
	actionType.Parameters = parameters
	return nil
}
