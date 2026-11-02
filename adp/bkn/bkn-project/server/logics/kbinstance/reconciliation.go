// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"slices"
	"strings"
)

type ReconcileRecord struct {
	Identity any
	Values   map[string]any
}
type ReconcileConflict struct {
	Identity               any
	Property               string
	SystemValue, TextValue any
}
type ReconcileResult struct {
	Both, SystemOnly, TextOnly int
	Conflicts                  []ReconcileConflict
}

func businessJoinKey(identity any) (string, error) {
	if identity == nil {
		return "", ErrInvalidBuild
	}
	if text, ok := identity.(string); ok && strings.TrimSpace(text) == "" {
		return "", ErrInvalidBuild
	}
	if _, ok := identity.(bool); ok {
		return "", ErrInvalidBuild
	}
	encoded, err := canonicalBusinessValue(identity)
	if err != nil {
		return "", err
	}
	var node []any
	if json.Unmarshal(encoded, &node) != nil || len(node) != 2 {
		return "", ErrInvalidBuild
	}
	kind, _ := node[0].(string)
	if kind != "string" && kind != "number" {
		return "", ErrInvalidBuild
	}
	if kind == "number" {
		number, ok := node[1].(string)
		if !ok || strings.Contains(number, "/") {
			return "", ErrInvalidBuild
		}
	}
	return string(encoded), nil
}

// ReconcileSources compares complete, permission-checked source snapshots.
// The three counts are distinct business identities; duplicate keys reject the
// batch. Repeated field differences are review data, never updates to the table.
func ReconcileSources(system, text []ReconcileRecord, properties []string) (ReconcileResult, error) {
	result := ReconcileResult{Conflicts: []ReconcileConflict{}}
	index := func(records []ReconcileRecord) (map[string]ReconcileRecord, error) {
		keyed := map[string]ReconcileRecord{}
		for _, record := range records {
			key, err := businessJoinKey(record.Identity)
			if err != nil {
				return nil, err
			}
			if _, duplicate := keyed[key]; duplicate {
				return nil, ErrInvalidBuild
			}
			keyed[key] = record
		}
		return keyed, nil
	}
	left, err := index(system)
	if err != nil {
		return result, err
	}
	right, err := index(text)
	if err != nil {
		return result, err
	}
	keys := make([]string, 0, len(left))
	for key := range left {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		a := left[key]
		b, matched := right[key]
		if !matched {
			result.SystemOnly++
			continue
		}
		result.Both++
		fields := slices.Clone(properties)
		slices.Sort(fields)
		fields = slices.Compact(fields)
		for _, name := range fields {
			if name == "" {
				return ReconcileResult{}, ErrInvalidBuild
			}
			if b.Values[name] == nil {
				continue
			}
			x, err := canonicalBusinessValue(a.Values[name])
			if err != nil {
				return ReconcileResult{}, err
			}
			y, err := canonicalBusinessValue(b.Values[name])
			if err != nil {
				return ReconcileResult{}, err
			}
			if string(x) != string(y) {
				result.Conflicts = append(result.Conflicts, ReconcileConflict{Identity: a.Identity, Property: name, SystemValue: a.Values[name], TextValue: b.Values[name]})
			}
		}
	}
	for key := range right {
		if _, found := left[key]; !found {
			result.TextOnly++
		}
	}
	return result, nil
}
