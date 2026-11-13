// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"encoding/json"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// idBasis is what a registered id was made from.
type idBasis struct {
	PhysicalName string `json:"physical_name"`
}

// withRegisteredIDs gives the drafter the project's registered ids, so items drafted from names
// that are not ASCII keep the ids earlier drafts gave them.
func (d *drafter) withRegisteredIDs(projectID string, ids []*interfaces.RegisteredID) *drafter {
	d.projectID = projectID
	d.registered = map[interfaces.IDKey]*interfaces.RegisteredID{}
	for _, id := range ids {
		d.registered[id.IDKey] = id
	}
	return d
}

// withTaken keeps the ids of the object and relation types of items from what this draft names.
func (d *drafter) withTaken(items *interfaces.DraftItems) *drafter {
	d.taken = map[string]map[string]bool{interfaces.ID_KIND_OBJECT_TYPES: {}, interfaces.ID_KIND_RELATION_TYPES: {}}
	// A system object type is left out of the network's items, but its id stays taken.
	for _, id := range interfaces.SystemObjectTypeIDs() {
		d.taken[interfaces.ID_KIND_OBJECT_TYPES][id] = true
	}
	for _, ot := range items.ObjectTypes {
		d.taken[interfaces.ID_KIND_OBJECT_TYPES][ot.ID] = true
	}
	for _, rt := range items.RelationTypes {
		d.taken[interfaces.ID_KIND_RELATION_TYPES][rt.ID] = true
	}
	return d
}

// reserve takes the ids registered for kind into taken before any item is named, so an item
// drafted later, such as a new table that sorts first, cannot take a registered id. For
// properties, sourcePrefix keeps it to one table's.
func (d *drafter) reserve(kind string, sourcePrefix string, taken map[string]bool) {
	for id := range d.taken[kind] {
		taken[id] = true
	}
	for key, id := range d.registered {
		if key.Kind == kind && strings.HasPrefix(key.SourceKey, sourcePrefix) {
			taken[id.ID] = true
		}
	}
}

// itemID names an item drafted from physical, a table or field name, and takes the id. An ASCII
// name gives the id itself. Otherwise the id is the registered one, else a placeholder: the kind's
// prefix and a short hash of the name, recorded so that later drafts keep it.
func (d *drafter) itemID(kind string, sourceKey string, physical string, prefix string,
	taken map[string]bool) string {
	if isASCII(physical) {
		return uniqueName(idFrom(physical, prefix), taken)
	}
	key := interfaces.IDKey{Kind: kind, SourceKey: sourceKey}
	if d.named == nil {
		d.named = map[interfaces.IDKey]bool{}
	}
	d.named[key] = true
	if registered, ok := d.registered[key]; ok {
		return registered.ID
	}
	id := uniqueName(idFrom(physical, prefix), taken)
	basisJSON, _ := json.Marshal(idBasis{PhysicalName: physical})
	d.recorded = append(d.recorded, &interfaces.RegisteredID{
		ProjectID: d.projectID, IDKey: key, ID: id, Origin: interfaces.ID_ORIGIN_FALLBACK, Basis: basisJSON,
	})
	return id
}

// asciiFirst lists the tables named in ASCII before the others, each in the order given. The ids of
// the first are as they always were; those of the others take only what is left.
func asciiFirst(tables []*interfaces.VegaTable) []*interfaces.VegaTable {
	ordered := make([]*interfaces.VegaTable, 0, len(tables))
	for _, ascii := range []bool{true, false} {
		for _, table := range tables {
			if isASCII(physicalName(table)) == ascii {
				ordered = append(ordered, table)
			}
		}
	}
	return ordered
}

// propertySourceKey keys a property by its table and field.
func propertySourceKey(tableID string, field string) string {
	return tableID + "/" + field
}
