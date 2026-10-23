// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"fmt"
	"sort"
)

// NamedDefinition is the name and the aliases of one object type or action type, as the alias
// check sees it.
type NamedDefinition struct {
	ID      string
	Name    string
	Aliases []string
}

// AliasConflict describes the network a save would produce, where an alias of one definition is the
// name of another of the same kind. kind names the kind, such as "object type".
//
// The network is incoming saved over stored: storedNamed holds the stored definitions whose names
// incoming aliases could hit, storedAliased the stored definitions with aliases. A definition of
// incoming replaces the stored one of its ID, so a rename or an alias change within the same save
// counts as saved. Only a conflict that involves an incoming definition is reported: one between
// two stored definitions is not this save's doing. The answer is "" when there is none.
func AliasConflict(kind string, incoming, storedNamed, storedAliased []NamedDefinition) string {
	saved := make(map[string]bool, len(incoming))
	for _, d := range incoming {
		saved[d.ID] = true
	}
	ownerOfName := map[string]string{}
	for _, d := range storedNamed {
		if !saved[d.ID] {
			ownerOfName[d.Name] = d.ID
		}
	}
	for _, d := range incoming {
		ownerOfName[d.Name] = d.ID
	}

	aliased := append([]NamedDefinition{}, incoming...)
	stored := make([]NamedDefinition, 0, len(storedAliased))
	for _, d := range storedAliased {
		if !saved[d.ID] {
			stored = append(stored, d)
		}
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].ID < stored[j].ID })
	aliased = append(aliased, stored...)

	for _, d := range aliased {
		for _, alias := range d.Aliases {
			owner, named := ownerOfName[alias]
			if !named || owner == d.ID {
				continue
			}
			if saved[d.ID] {
				return fmt.Sprintf("%s %q alias %q is the name of %s %q", kind, d.ID, alias, kind, owner)
			}
			if saved[owner] {
				return fmt.Sprintf("%s %q is named %q, which is an alias of %s %q", kind, owner, alias, kind, d.ID)
			}
		}
	}
	return ""
}
