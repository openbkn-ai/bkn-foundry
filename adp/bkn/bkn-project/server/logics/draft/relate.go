// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// proposeRelations drafts the relations of the other object types to target, which took a
// single-field identity after drafting: drafting proposes no relation to an object type
// without one. The candidates and the counts that gate them are drafting's; the tables are read
// again, as the draft keeps no field types. Relations the draft has are not drafted again. A
// count cut off by ctx drafts nothing for its pair, and a failed read drafts nothing at all:
// the identity is the person's choice, and is kept either way.
func proposeRelations(ctx context.Context, d *drafter, va interfaces.VegaAccess, caller interfaces.AccountInfo,
	items *interfaces.DraftItems, target *interfaces.DraftObjectType) {
	if ctx.Err() != nil || len(target.PrimaryKeys) != 1 {
		return
	}
	ids := []string{}
	for _, ot := range items.ObjectTypes {
		ids = append(ids, ot.Table.ResourceID)
	}
	tables, err := va.GetTables(ctx, caller, ids)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read tables to relate %s failed: %v", target.ID, err))
		return
	}
	byID := map[string]*interfaces.VegaTable{}
	for _, table := range tables {
		byID[table.ID] = table
	}
	targetTable := byID[target.Table.ResourceID]
	if targetTable == nil {
		return
	}

	// The draft's object types as drafting holds them. Only the target is a table to refer to,
	// and its foreign keys resolve to it alone.
	for _, ot := range items.ObjectTypes {
		table := byID[ot.Table.ResourceID]
		if table == nil {
			continue
		}
		d.kinds[table.ID] = tableKindEntity
		d.objectTypes[table.ID] = ot
		d.fields[table.ID] = map[string]string{}
		for _, p := range ot.Properties {
			d.fields[table.ID][p.MappedField] = p.Name
		}
		if !ot.NeedsIdentity {
			d.keys[table.ID] = []string{}
			for _, key := range ot.PrimaryKeys {
				d.keys[table.ID] = append(d.keys[table.ID], mappedField(ot, key))
			}
		}
	}
	d.bySourceName[sourceName(targetTable.CatalogID, targetTable.SourceSchema, targetTable.SourceName)] = targetTable
	// A field the draft already relates to the target is not counted again: refer takes no field
	// the source does not map.
	for _, r := range items.RelationTypes {
		if r.Target != target.ID || r.LinkTable != nil || len(r.Mapping) != 1 {
			continue
		}
		if source := findObjectType(items, r.Source); source != nil {
			delete(d.fields[source.Table.ResourceID], mappedField(source, r.Mapping[0].SourceProperty))
		}
	}
	relationIDs := map[string]bool{}
	for _, r := range items.RelationTypes {
		relationIDs[r.ID] = true
	}

	for _, ot := range items.ObjectTypes {
		table := byID[ot.Table.ResourceID]
		if table == nil || table.ID == targetTable.ID {
			continue
		}
		if err := d.addRelations(ctx, table, []*interfaces.VegaTable{targetTable}, relationIDs); err != nil {
			break
		}
	}

	adopted := map[string]bool{}
	for _, ot := range items.ObjectTypes {
		adopted[ot.ID] = ot.Adopted
	}
	for _, relation := range d.items.RelationTypes {
		if relation.Target != target.ID || hasRelation(items, relation) {
			continue
		}
		// As drafting: adopted only when both its object types are.
		if relation.Adopted && !(adopted[relation.Source] && adopted[relation.Target]) {
			relation.Adopted, relation.ProposedBecause = false, proposedEndNotAdopted
		}
		items.RelationTypes = append(items.RelationTypes, relation)
	}
}

// hasRelation reports whether items has a relation between the same object types by the same fields.
func hasRelation(items *interfaces.DraftItems, relation *interfaces.DraftRelationType) bool {
	return slices.ContainsFunc(items.RelationTypes, func(r *interfaces.DraftRelationType) bool {
		return r.LinkTable == nil && r.Source == relation.Source && r.Target == relation.Target &&
			slices.Equal(r.Mapping, relation.Mapping)
	})
}
