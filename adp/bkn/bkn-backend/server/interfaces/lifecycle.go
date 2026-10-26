// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"bytes"
	"encoding/json"
)

// Lifecycle statuses (BKN spec 3.0, SPECIFICATION.md §4.3).
const (
	LIFECYCLE_ACTIVE     = "active"
	LIFECYCLE_DEPRECATED = "deprecated"
)

// DECLARATION_LIFECYCLE is the lifecycle field of an object type, a relation type or an action type.
const DECLARATION_LIFECYCLE = "lifecycle"

// Lifecycle says whether a definition is still in use (SPECIFICATION.md §4.3). It is named lifecycle,
// not status: the status of an object type is its index information. A definition without one is
// active. Reason and ReplacedBy go with deprecated only.
type Lifecycle struct {
	Status     string `json:"status" mapstructure:"status"`
	Reason     string `json:"reason,omitempty" mapstructure:"reason"`
	ReplacedBy string `json:"replaced_by,omitempty" mapstructure:"replaced_by"`
}

// NormalizeLifecycle stores an active lifecycle as nil: no lifecycle, null, {} and status active
// without reason or replaced_by all mean active, and active is written as NULL, the way rows saved
// before the column read. Anything else is kept for the save check to judge.
func NormalizeLifecycle(lc *Lifecycle) *Lifecycle {
	if lc == nil {
		return nil
	}
	if (lc.Status == "" || lc.Status == LIFECYCLE_ACTIVE) && lc.Reason == "" && lc.ReplacedBy == "" {
		return nil
	}
	return lc
}

// IsDeprecated reports whether a lifecycle is deprecated.
func (lc *Lifecycle) IsDeprecated() bool {
	return lc != nil && lc.Status == LIFECYCLE_DEPRECATED
}

// carriedKeys reports which of keys a JSON object has at its top level, null included.
func carriedKeys(data []byte, keys ...string) (map[string]bool, error) {
	present := false
	for _, key := range keys {
		if bytes.Contains(data, []byte(`"`+key+`"`)) {
			present = true
			break
		}
	}
	if !present {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	carried := map[string]bool{}
	for _, key := range keys {
		if _, ok := raw[key]; ok {
			carried[key] = true
		}
	}
	return carried, nil
}

// Columns returns the status, reason and replaced_by of a lifecycle; a nil lifecycle is all empty.
func (lc *Lifecycle) Columns() (string, string, string) {
	if lc == nil {
		return "", "", ""
	}
	return lc.Status, lc.Reason, lc.ReplacedBy
}

// LifecycleOf builds a lifecycle from its stored status, reason and replaced_by. An empty status is
// active, returned as nil.
func LifecycleOf(status, reason, replacedBy string) *Lifecycle {
	if status == "" {
		return nil
	}
	return NormalizeLifecycle(&Lifecycle{Status: status, Reason: reason, ReplacedBy: replacedBy})
}
