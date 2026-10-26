// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
)

// NormalizeAliases stores no aliases as nil: [] and null in a request clear them, and a cleared
// list is written as NULL, the same as a definition that never had aliases.
func NormalizeAliases(aliases []string) []string {
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

// UnmarshalJSON records which declaration fields the JSON carried, so an interactive update can keep
// the stored ones it left out.
func (ot *ObjectType) UnmarshalJSON(data []byte) error {
	type objectType ObjectType
	var value objectType
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	declared, err := carriedKeys(data, DECLARATION_ALIASES, DECLARATION_LIFECYCLE)
	if err != nil {
		return err
	}
	*ot = ObjectType(value)
	ot.declared = declared
	return nil
}

// DeclaresAliases reports whether the JSON this object type was read from carried aliases.
func (ot *ObjectType) DeclaresAliases() bool {
	return ot != nil && ot.declared[DECLARATION_ALIASES]
}

// DeclaresLifecycle reports whether the JSON this object type was read from carried lifecycle.
func (ot *ObjectType) DeclaresLifecycle() bool {
	return ot != nil && ot.declared[DECLARATION_LIFECYCLE]
}

// UnmarshalJSON records which declaration fields the JSON carried, as for an object type.
func (at *ActionType) UnmarshalJSON(data []byte) error {
	type actionType ActionType
	var value actionType
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	declared, err := carriedKeys(data, DECLARATION_ALIASES, DECLARATION_LIFECYCLE)
	if err != nil {
		return err
	}
	*at = ActionType(value)
	at.declared = declared
	return nil
}

// DeclaresAliases reports whether the JSON this action type was read from carried aliases.
func (at *ActionType) DeclaresAliases() bool {
	return at != nil && at.declared[DECLARATION_ALIASES]
}

// DeclaresLifecycle reports whether the JSON this action type was read from carried lifecycle.
func (at *ActionType) DeclaresLifecycle() bool {
	return at != nil && at.declared[DECLARATION_LIFECYCLE]
}
