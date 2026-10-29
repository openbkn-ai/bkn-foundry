// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// shortName is the form of a usable short name from semantic understanding: a BKN id as it is,
// within the property name limit.
var shortName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// idBasis is what a registered id was made from.
type idBasis struct {
	PhysicalName string `json:"physical_name"`
	APIName      string `json:"api_name,omitempty"`
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

// reserve takes the ids registered for kind into taken before any item is named, so an item
// drafted later, such as a new table that sorts first, cannot take a registered id. For
// properties, sourcePrefix keeps it to one table's.
func (d *drafter) reserve(kind string, sourcePrefix string, taken map[string]bool) {
	for key, id := range d.registered {
		if key.Kind == kind && strings.HasPrefix(key.SourceKey, sourcePrefix) {
			taken[id.ID] = true
		}
	}
}

// itemID names an item drafted from physical, a table or field name, and takes the id. An ASCII
// name gives the id itself. Otherwise the id is the registered one; or, when there is none or it
// is an unlocked placeholder, the short name semantic understanding gave, else a placeholder,
// recorded so that later drafts keep it.
func (d *drafter) itemID(kind string, sourceKey string, physical string, apiName string, prefix string,
	taken map[string]bool) string {
	if isASCII(physical) {
		return uniqueName(idFrom(physical, prefix), taken)
	}
	semantic := shortName.MatchString(apiName)
	key := interfaces.IDKey{Kind: kind, SourceKey: sourceKey}
	if d.named == nil {
		d.named = map[interfaces.IDKey]bool{}
	}
	d.named[key] = true
	if registered, ok := d.registered[key]; ok &&
		(registered.Locked || registered.Origin != interfaces.ID_ORIGIN_FALLBACK || !semantic) {
		return registered.ID
	}
	origin, candidate := interfaces.ID_ORIGIN_FALLBACK, idFrom(physical, prefix)
	basis := idBasis{PhysicalName: physical}
	if semantic {
		origin, candidate, basis.APIName = interfaces.ID_ORIGIN_SEMANTIC, apiName, apiName
	}
	id := uniqueName(candidate, taken)
	basisJSON, _ := json.Marshal(basis)
	d.recorded = append(d.recorded, &interfaces.RegisteredID{
		ProjectID: d.projectID, IDKey: key, ID: id, Origin: origin, Basis: basisJSON,
	})
	return id
}

// asciiFirst lists the tables named in ASCII before the others, each in the order given. The ids of
// the first are as they always were; short names of the others take only what is left.
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
