// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"bytes"
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

// carriesAliases reports whether a JSON object has an aliases key at its top level, null included.
func carriesAliases(data []byte) (bool, error) {
	if !bytes.Contains(data, []byte(`"aliases"`)) {
		return false, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, err
	}
	_, ok := raw[DECLARATION_ALIASES]
	return ok, nil
}

// UnmarshalJSON records whether the JSON carried aliases, so an interactive update can keep the
// stored aliases when it did not.
func (ot *ObjectType) UnmarshalJSON(data []byte) error {
	type objectType ObjectType
	var value objectType
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	declared, err := carriesAliases(data)
	if err != nil {
		return err
	}
	*ot = ObjectType(value)
	ot.aliasesDeclared = declared
	return nil
}

// DeclaresAliases reports whether the JSON this object type was read from carried aliases.
func (ot *ObjectType) DeclaresAliases() bool {
	return ot != nil && ot.aliasesDeclared
}

// UnmarshalJSON records whether the JSON carried aliases, as for an object type.
func (at *ActionType) UnmarshalJSON(data []byte) error {
	type actionType ActionType
	var value actionType
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	declared, err := carriesAliases(data)
	if err != nil {
		return err
	}
	*at = ActionType(value)
	at.aliasesDeclared = declared
	return nil
}

// DeclaresAliases reports whether the JSON this action type was read from carried aliases.
func (at *ActionType) DeclaresAliases() bool {
	return at != nil && at.aliasesDeclared
}
