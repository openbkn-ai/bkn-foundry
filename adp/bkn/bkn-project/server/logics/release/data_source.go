// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"sort"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// boundTable is a table a type in the definitions reads its records from.
type boundTable struct {
	item       interfaces.ReleaseItem
	resourceID string
}

// boundTables are the tables of the object types and relation types in files, in path order: an
// object type's data source and the link table of a relation joining through one. Items are
// named as the definitions name them.
func boundTables(files map[string][]byte) []boundTable {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	bound := []boundTable{}
	for _, p := range paths {
		id := ""
		switch kindOf(p) {
		case interfaces.RELEASE_ITEM_OBJECT_TYPE:
			if ot, err := bkn.ParseObjectTypeFile(string(files[p]), p); err == nil && ot.DataSource != nil {
				id = ot.DataSource.ID
			}
		case interfaces.RELEASE_ITEM_RELATION_TYPE:
			if rt, err := bkn.ParseRelationTypeFile(string(files[p]), p); err == nil {
				if rule, ok := rt.MappingRules.(*bkn.InDirectMappingRule); ok && rule.BackingDataSource != nil {
					id = rule.BackingDataSource.ID
				}
			}
		}
		if id != "" {
			bound = append(bound, boundTable{item: itemAt(p, files), resourceID: id})
		}
	}
	return bound
}

// tableIDs are the tables bound, each once.
func tableIDs(bound []boundTable) []string {
	ids, seen := []string{}, map[string]bool{}
	for _, b := range bound {
		if !seen[b.resourceID] {
			seen[b.resourceID] = true
			ids = append(ids, b.resourceID)
		}
	}
	return ids
}

// goneItems are the items bound to a table in missing, in their order.
func goneItems(bound []boundTable, missing []string) []interfaces.ReleaseItem {
	gone := map[string]bool{}
	for _, id := range missing {
		gone[id] = true
	}
	items := []interfaces.ReleaseItem{}
	for _, b := range bound {
		if gone[b.resourceID] {
			items = append(items, b.item)
		}
	}
	return items
}
