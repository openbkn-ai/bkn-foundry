// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// GetExistingIndex returns the readiness of the local indexes of the tables the network's existing
// object types read, to whoever can see the project, read as the caller. The network keeps those
// indexes: the project shows them beside its own and never builds them. A table the caller cannot
// read, one by one or all at once when vega-backend refuses the batch, reads as unreadable.
func (s *draftService) GetExistingIndex(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectIndex, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetExistingIndex")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	var owned []string
	if draft, exist, err := s.da.GetDraft(ctx, projectID); err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	} else if exist {
		owned = ownedPaths(draft.Items.Confirmation)
	}
	existing, err := s.readExisting(ctx, entry.KNID, entry.Branch, owned)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return nil, internalError(ctx, "Read branch failed", err)
	}
	items := existingIndexItems(existing)
	tables := s.existingIndexTables(ctx, items, caller)
	tasks, visible := s.existingIndexTasks(ctx, tables, caller)
	for _, ot := range items.ObjectTypes {
		if table := tables[ot.Table.ResourceID]; table != nil {
			ot.Table.Name, ot.Table.CatalogID = table.Name, table.CatalogID
		}
	}
	index := indexReadiness(items, tables, tasks, visible)
	// Shown only: how the network refreshes them is not the project's to choose.
	for i := range index.ObjectTypes {
		index.ObjectTypes[i].FullRebuildOnly = false
		index.ObjectTypes[i].UpdateField = interfaces.ProjectIndexUpdateField{Candidates: []string{}}
	}
	span.SetStatus(codes.Ok, "")
	return index, nil
}

// existingIndexItems are the existing object types that read a table, as adopted items of their
// own, in the branch's order.
func existingIndexItems(existing *interfaces.DraftExisting) *interfaces.DraftItems {
	items := &interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{}}
	for _, ot := range existing.ObjectTypes {
		if ot.TableID == "" {
			continue
		}
		items.ObjectTypes = append(items.ObjectTypes, &interfaces.DraftObjectType{
			ID: ot.ID, Name: ot.Name, Adopted: true, Table: interfaces.DraftTable{ResourceID: ot.TableID},
		})
	}
	return items
}

// existingIndexTables reads the tables of items as the account, by resource id. A refused or
// failed read leaves them out: they read as unreadable.
func (s *draftService) existingIndexTables(ctx context.Context, items *interfaces.DraftItems,
	account interfaces.AccountInfo) map[string]*interfaces.VegaTable {
	tables := map[string]*interfaces.VegaTable{}
	ids := []string{}
	for _, ot := range items.ObjectTypes {
		if !slices.Contains(ids, ot.Table.ResourceID) {
			ids = append(ids, ot.Table.ResourceID)
		}
	}
	if len(ids) == 0 {
		return tables
	}
	read, err := s.va.GetTables(ctx, account, ids)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read the tables of existing object types: %v", err))
		return tables
	}
	for _, table := range read {
		tables[table.ID] = table
	}
	return tables
}

// existingIndexTasks lists the build tasks under way in the catalogs of tables, by resource id,
// and whether the account could list them all.
func (s *draftService) existingIndexTasks(ctx context.Context, tables map[string]*interfaces.VegaTable,
	account interfaces.AccountInfo) (map[string]*interfaces.VegaBuildTask, bool) {
	catalogIDs := []string{}
	for _, table := range tables {
		if table.CatalogID != "" && !slices.Contains(catalogIDs, table.CatalogID) {
			catalogIDs = append(catalogIDs, table.CatalogID)
		}
	}
	slices.Sort(catalogIDs)
	tasks, visible := map[string]*interfaces.VegaBuildTask{}, true
	for _, catalogID := range catalogIDs {
		listed, ok, err := s.va.ListActiveBuildTasks(ctx, account, catalogID)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("List build tasks of catalog %s: %v", catalogID, err))
		}
		if err != nil || !ok {
			visible = false
			continue
		}
		for _, task := range listed {
			tasks[task.ResourceID] = task
		}
	}
	return tasks, visible
}
