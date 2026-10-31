// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// GetIndex returns the readiness of the local indexes of the draft's adopted object types to whoever
// can see the project. Their tables and build tasks are read as the caller: a table the caller
// cannot see reads as unreadable, and tasks the caller may not list leave building unknown.
func (s *draftService) GetIndex(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectIndex, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetIndex")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Draft not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
	}
	index, _, err := s.readIndex(ctx, &draft.Items, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Read tables failed")
		return nil, internalError(ctx, "Read tables failed", err)
	}
	if c := draft.Items.Confirmation; c != nil && c.Commit != "" {
		s.attachBuilds(ctx, index, projectID, c.Commit)
	}
	span.SetStatus(codes.Ok, "")
	return index, nil
}

// attachBuilds gives each object type the build that the confirmation of commit started for its
// table. Without them the readiness still stands.
func (s *draftService) attachBuilds(ctx context.Context, index *interfaces.ProjectIndex, projectID string,
	commit string) {
	builds, err := s.iba.ListBuilds(ctx, projectID, commit)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("List index builds of project %s: %v", projectID, err))
		return
	}
	byResource := map[string]*interfaces.IndexBuild{}
	for _, b := range builds {
		byResource[b.ResourceID] = b
	}
	for i := range index.ObjectTypes {
		item := &index.ObjectTypes[i]
		if b, ok := byResource[item.Table.ResourceID]; ok {
			item.Build = &interfaces.ProjectIndexBuild{Outcome: b.Outcome, TaskID: b.TaskID, Reason: b.Reason,
				Time: b.CreateTime}
		}
	}
}

// readIndex reads the readiness of the local indexes of the adopted object types as the account,
// and the tables it read them from, by resource id.
func (s *draftService) readIndex(ctx context.Context, items *interfaces.DraftItems,
	account interfaces.AccountInfo) (*interfaces.ProjectIndex, map[string]*interfaces.VegaTable, error) {
	var ids, catalogIDs []string
	for _, ot := range items.ObjectTypes {
		if ot.Adopted {
			ids = append(ids, ot.Table.ResourceID)
			if !slices.Contains(catalogIDs, ot.Table.CatalogID) {
				catalogIDs = append(catalogIDs, ot.Table.CatalogID)
			}
		}
	}
	tables := map[string]*interfaces.VegaTable{}
	if len(ids) > 0 {
		read, err := s.va.GetTables(ctx, account, ids)
		if err != nil {
			return nil, nil, err
		}
		for _, table := range read {
			tables[table.ID] = table
		}
	}
	tasks, tasksVisible := map[string]*interfaces.VegaBuildTask{}, true
	for _, catalogID := range catalogIDs {
		// Listing needs the catalog's task_manage permission, which a reader may not have.
		listed, visible, err := s.va.ListActiveBuildTasks(ctx, account, catalogID)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("List build tasks of catalog %s: %v", catalogID, err))
		}
		if err != nil || !visible {
			tasksVisible = false
			continue
		}
		for _, task := range listed {
			tasks[task.ResourceID] = task
		}
	}
	return indexReadiness(items, tables, tasks, tasksVisible), tables, nil
}

// indexReadiness is the readiness of the local indexes of the adopted object types, in the draft's
// order, from their tables by resource id and the build tasks under way by resource id.
func indexReadiness(items *interfaces.DraftItems, tables map[string]*interfaces.VegaTable,
	tasks map[string]*interfaces.VegaBuildTask, tasksVisible bool) *interfaces.ProjectIndex {
	index := &interfaces.ProjectIndex{
		ObjectTypes: []interfaces.ProjectIndexItem{}, Counts: map[string]int{}, TasksVisible: tasksVisible,
	}
	for _, ot := range items.ObjectTypes {
		if !ot.Adopted {
			continue
		}
		item := interfaces.ProjectIndexItem{ID: ot.ID, Name: ot.Name, Table: ot.Table, UpdateField: updateField(ot)}
		item.State, item.Missing = indexState(tables[ot.Table.ResourceID])
		item.FullRebuildOnly = fullRebuildOnly(tables[ot.Table.ResourceID])
		// A task under way builds the index; an index already ready stays in use until it is done.
		if task, ok := tasks[ot.Table.ResourceID]; ok && item.State != interfaces.INDEX_STATE_UNREADABLE {
			item.Task = &interfaces.ProjectIndexTask{ID: task.ID, Status: task.Status}
			if item.State != interfaces.INDEX_STATE_READY {
				item.State, item.Missing = interfaces.INDEX_STATE_BUILDING, nil
			}
		}
		index.Counts[item.State]++
		index.ObjectTypes = append(index.ObjectTypes, item)
	}
	return index
}

// indexState is the state of a table's local index, and the configuration fields it lacks. A
// missing configuration comes before a stale index: changing the configuration is what made it
// stale, and building needs the configuration first.
func indexState(table *interfaces.VegaTable) (string, []string) {
	if table == nil {
		return interfaces.INDEX_STATE_UNREADABLE, nil
	}
	index := table.Index
	if index.LocalStatus == interfaces.VEGA_LOCAL_STATUS_AVAILABLE && index.IndexName != "" {
		return interfaces.INDEX_STATE_READY, nil
	}
	var missing []string
	if len(index.PrimaryKeyFields) == 0 {
		missing = append(missing, interfaces.INDEX_FIELD_PRIMARY_KEY)
	}
	if len(index.IncrementalFields) == 0 {
		missing = append(missing, interfaces.INDEX_FIELD_INCREMENTAL)
	}
	switch {
	case len(missing) > 0:
		return interfaces.INDEX_STATE_NOT_CONFIGURED, missing
	case index.LocalStatus == interfaces.VEGA_LOCAL_STATUS_STALE:
		return interfaces.INDEX_STATE_STALE, nil
	}
	return interfaces.INDEX_STATE_NOT_BUILT, nil
}

// fullRebuildOnly tells whether a table's index refreshes by its identity's fields alone, the
// object type having no update field: its incremental fields are its primary key fields.
func fullRebuildOnly(table *interfaces.VegaTable) bool {
	return table != nil && len(table.Index.IncrementalFields) > 0 &&
		slices.Equal(table.Index.IncrementalFields, table.Index.PrimaryKeyFields)
}

// updateField is the object type's update field and the properties that may be one, as the pending
// item asking for one lists them.
func updateField(ot *interfaces.DraftObjectType) interfaces.ProjectIndexUpdateField {
	field := interfaces.ProjectIndexUpdateField{Property: ot.IncrementalKey, Candidates: updateFieldCandidates(ot)}
	field.Pending = field.Property == "" && len(field.Candidates) > 0
	return field
}
