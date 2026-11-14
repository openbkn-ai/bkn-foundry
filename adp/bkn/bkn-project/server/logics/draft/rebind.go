// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A catalog and its tables deleted and created again under the same names, as reinstalling a sample
// does, come back with new resource ids. The draft's items still bind the old ids, which are gone:
// each such table is bound again to the table of the same catalog name and table name in the
// project's catalogs, keeping every decision on the item. Columns the item maps that the table no
// longer has are source changes, as for any table. A table with no such match, or with more than
// one, stays gone.

// draftTableRef is a table the draft binds, with the item binding it.
type draftTableRef struct {
	kind, itemID string
	table        *interfaces.DraftTable
}

// draftTableRefs are the tables the draft's own items bind: object types, link tables and the code
// tables of value types. What the network already has is bound as the network binds it.
func draftTableRefs(items *interfaces.DraftItems) []draftTableRef {
	refs := []draftTableRef{}
	for _, ot := range items.ObjectTypes {
		if !ot.Existing && ot.Table.ResourceID != "" {
			refs = append(refs, draftTableRef{interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID, &ot.Table})
		}
	}
	for _, rt := range items.RelationTypes {
		if !rt.Existing && rt.LinkTable != nil && rt.LinkTable.ResourceID != "" {
			refs = append(refs, draftTableRef{interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID, rt.LinkTable})
		}
	}
	for _, vt := range items.ValueTypes {
		if vt.CodeTable.ResourceID != "" {
			refs = append(refs, draftTableRef{interfaces.DRAFT_KIND_VALUE_TYPES, vt.ID, &vt.CodeTable})
		}
	}
	return refs
}

// sameTable tells whether table is the one gone, by the physical table's name where both record
// it, else by the name the draft recorded.
func sameTable(gone *interfaces.DraftTable, table *interfaces.VegaTable) bool {
	if gone.SourceName != "" && table.SourceName != "" {
		return gone.SourceName == table.SourceName
	}
	return gone.Name != "" && (table.Name == gone.Name || table.SourceName == gone.Name)
}

// rebindScope are the catalogs a gone table is looked for in: its own catalog while it is there;
// else those of the catalog name it recorded; else, for a draft that did not record it, all.
func rebindScope(gone *interfaces.DraftTable, catalogIDs []string, names map[string]string) []string {
	if slices.Contains(catalogIDs, gone.CatalogID) {
		return []string{gone.CatalogID}
	}
	if gone.CatalogName == "" {
		return catalogIDs
	}
	scope := []string{}
	for _, id := range catalogIDs {
		if names[id] == gone.CatalogName {
			scope = append(scope, id)
		}
	}
	return scope
}

// rebindMatches finds, for each gone table, the one table of the same catalog name and table name
// in catalogs, by the gone table's resource id; a table matched by none, or by more than one, is
// left out. listed are the catalogs' tables by catalog.
func rebindMatches(gone []*interfaces.DraftTable, catalogIDs []string, names map[string]string,
	listed map[string][]*interfaces.VegaTable) map[string]*interfaces.VegaTable {
	matches := map[string]*interfaces.VegaTable{}
	for _, t := range gone {
		if _, done := matches[t.ResourceID]; done {
			continue
		}
		var found []*interfaces.VegaTable
		for _, catalogID := range rebindScope(t, catalogIDs, names) {
			for _, table := range listed[catalogID] {
				if sameTable(t, table) {
					found = append(found, table)
				}
			}
		}
		if len(found) == 1 {
			matches[t.ResourceID] = found[0]
		}
	}
	return matches
}

// boundTo is the draft's reference to table, in the catalog names names.
func boundTo(table *interfaces.VegaTable, names map[string]string) interfaces.DraftTable {
	name := names[table.CatalogID]
	if name == "" {
		name = table.CatalogName
	}
	return interfaces.DraftTable{ResourceID: table.ID, Name: table.Name, CatalogID: table.CatalogID,
		CatalogName: name, SourceName: table.SourceName}
}

// existingTableRefs are the tables the network's existing items read, as the project branch
// defines them: object types and link tables.
func existingTableRefs(items *interfaces.DraftItems) []draftTableRef {
	refs := []draftTableRef{}
	for _, ot := range items.ObjectTypes {
		if ot.Existing && ot.Table.ResourceID != "" {
			refs = append(refs, draftTableRef{interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID, &ot.Table})
		}
	}
	for _, rt := range items.RelationTypes {
		if rt.Existing && rt.LinkTable != nil && rt.LinkTable.ResourceID != "" {
			refs = append(refs, draftTableRef{interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID, rt.LinkTable})
		}
	}
	return refs
}

// rebindExisting binds again, in place, the tables the network's existing items read that are gone
// to the table of the same catalog name and table name among tables, as the draft's own items are
// bound again: the project branch keeps the ids its definitions were written with, so after a
// catalog was created again they name tables that are gone, and drafting would model the network's
// tables a second time. A table not gone is the network's as it is, whatever its name.
func (s *draftService) rebindExisting(ctx context.Context, caller interfaces.AccountInfo,
	existing *interfaces.DraftItems, tables []*interfaces.VegaTable) error {
	refs := existingTableRefs(existing)
	listedIDs := map[string]bool{}
	listed := map[string][]*interfaces.VegaTable{}
	catalogIDs := []string{}
	names := map[string]string{}
	for _, t := range tables {
		listedIDs[t.ID] = true
		if _, ok := listed[t.CatalogID]; !ok {
			catalogIDs = append(catalogIDs, t.CatalogID)
		}
		listed[t.CatalogID] = append(listed[t.CatalogID], t)
		if t.CatalogName != "" {
			names[t.CatalogID] = t.CatalogName
		}
	}
	unlisted := []string{}
	for _, ref := range refs {
		if !listedIDs[ref.table.ResourceID] && !slices.Contains(unlisted, ref.table.ResourceID) {
			unlisted = append(unlisted, ref.table.ResourceID)
		}
	}
	if len(unlisted) == 0 || len(tables) == 0 {
		return nil
	}
	missing, err := s.va.MissingTables(ctx, caller, unlisted)
	if err != nil {
		return fmt.Errorf("read the tables the network reads: %w", err)
	}
	gone := []*interfaces.DraftTable{}
	for _, ref := range refs {
		if slices.Contains(missing, ref.table.ResourceID) {
			gone = append(gone, ref.table)
		}
	}
	matches := rebindMatches(gone, catalogIDs, names, listed)
	for _, ref := range refs {
		if table, ok := matches[ref.table.ResourceID]; ok {
			*ref.table = boundTo(table, names)
		}
	}
	return nil
}

// applyRebind binds the draft's tables matched again, one decision per item, and moves what
// drafting recorded it read to them: the tables' ids, and the catalogs gone to those now read.
func applyRebind(items *interfaces.DraftItems, refs []draftTableRef, matches map[string]*interfaces.VegaTable,
	names map[string]string) []*interfaces.DraftDecision {
	decisions := []*interfaces.DraftDecision{}
	catalogs := map[string]string{}
	for _, ref := range refs {
		table, ok := matches[ref.table.ResourceID]
		if !ok {
			continue
		}
		before := *ref.table
		*ref.table = boundTo(table, names)
		if before.CatalogID != table.CatalogID {
			catalogs[before.CatalogID] = table.CatalogID
		}
		decisions = append(decisions, &interfaces.DraftDecision{
			Kind: ref.kind, ItemID: ref.itemID, Action: interfaces.DRAFT_DECISION_REBIND,
			Before: before, After: *ref.table,
		})
	}
	if items.Inputs == nil {
		return decisions
	}
	for i, t := range items.Inputs.Tables {
		if table, ok := matches[t.ResourceID]; ok {
			items.Inputs.Tables[i].ResourceID = table.ID
		}
	}
	ids := []string{}
	for _, id := range items.Inputs.CatalogIDs {
		if to, ok := catalogs[id]; ok {
			id = to
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	items.Inputs.CatalogIDs = ids
	return decisions
}

// rebindTables binds the draft's tables that are gone again by catalog name and table name in the
// catalogs, as the caller, and writes the draft; it returns how many tables it bound again. A
// draft written meanwhile is left as it is, and the next read tries again.
func (s *draftService) rebindTables(ctx context.Context, caller interfaces.AccountInfo, catalogIDs []string,
	draft *interfaces.Draft) (int, error) {
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY || len(catalogIDs) == 0 {
		return 0, nil
	}
	refs := draftTableRefs(&draft.Items)
	ids := []string{}
	for _, ref := range refs {
		if !slices.Contains(ids, ref.table.ResourceID) {
			ids = append(ids, ref.table.ResourceID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	missing, err := s.va.MissingTables(ctx, caller, ids)
	if err != nil {
		return 0, fmt.Errorf("read the tables: %w", err)
	}
	if len(missing) == 0 {
		return 0, nil
	}
	gone := []*interfaces.DraftTable{}
	for _, ref := range refs {
		if slices.Contains(missing, ref.table.ResourceID) {
			gone = append(gone, ref.table)
		}
	}
	names, err := s.va.CatalogNames(ctx, caller, catalogIDs)
	if err != nil {
		return 0, fmt.Errorf("read the names of the catalogs: %w", err)
	}
	// Only the catalogs some gone table is looked for in are read, each once.
	listed := map[string][]*interfaces.VegaTable{}
	for _, t := range gone {
		for _, catalogID := range rebindScope(t, catalogIDs, names) {
			if _, read := listed[catalogID]; read {
				continue
			}
			tables, err := s.va.ListTables(ctx, caller, catalogID)
			if err != nil {
				return 0, fmt.Errorf("list tables of catalog %s: %w", catalogID, err)
			}
			listed[catalogID] = tables
		}
	}
	// The physical names are in the tables' structure, which listing may leave out.
	tableIDs := []string{}
	for _, tables := range listed {
		for _, table := range tables {
			tableIDs = append(tableIDs, table.ID)
		}
	}
	if len(tableIDs) == 0 {
		return 0, nil
	}
	slices.Sort(tableIDs)
	read, err := s.va.GetTables(ctx, caller, tableIDs)
	if err != nil {
		return 0, fmt.Errorf("read tables: %w", err)
	}
	byID := map[string]*interfaces.VegaTable{}
	for _, table := range read {
		byID[table.ID] = table
	}
	for catalogID, tables := range listed {
		full := make([]*interfaces.VegaTable, 0, len(tables))
		for _, table := range tables {
			if t, ok := byID[table.ID]; ok {
				full = append(full, t)
			}
		}
		listed[catalogID] = full
	}
	matches := rebindMatches(gone, catalogIDs, names, listed)
	if len(matches) == 0 {
		return 0, nil
	}
	decisions := applyRebind(&draft.Items, refs, matches, names)
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		return 0, err
	}
	return len(matches), nil
}

// reconnectedTables counts the tables bound again by name since the draft was last confirmed.
func reconnectedTables(draft *interfaces.Draft) int {
	if draft == nil {
		return 0
	}
	var since int64
	if c := draft.Items.Confirmation; c != nil {
		since = c.Time
	}
	tables := map[string]bool{}
	for _, d := range draft.Items.Decisions {
		if d.Action == interfaces.DRAFT_DECISION_REBIND && d.Time > since {
			if id := tableIDOf(d.Before); id != "" {
				tables[id] = true
			}
		}
	}
	return len(tables)
}

// tableIDOf is the resource id of a decision's table, as written or as read back from the database.
func tableIDOf(v any) string {
	switch t := v.(type) {
	case interfaces.DraftTable:
		return t.ResourceID
	case *interfaces.DraftTable:
		if t != nil {
			return t.ResourceID
		}
	case map[string]any:
		id, _ := t["resource_id"].(string)
		return id
	}
	return ""
}
