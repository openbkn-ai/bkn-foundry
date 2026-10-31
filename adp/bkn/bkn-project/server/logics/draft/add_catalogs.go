// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// errNotCounted is the count of a table the draft has already: drafting the added catalogs does not
// count it again.
var errNotCounted = errors.New("not counted: the table is drafted already")

// draftedCounter counts the tables of added catalogs, and none of those the draft has already. Of
// those, only the field the object type's single-field identity maps is answered, as unique, so
// that the tables added can relate to it.
type draftedCounter struct {
	counter
	// keys are the identity fields of the tables drafted already, by table id.
	keys map[string]string
}

// draftedKeys are the tables of the draft's object types, each with the field its single-field
// identity maps, empty for another identity.
func draftedKeys(items *interfaces.DraftItems) map[string]string {
	keys := map[string]string{}
	for _, ot := range items.ObjectTypes {
		if ot.Table.ResourceID == "" {
			continue
		}
		keys[ot.Table.ResourceID] = ""
		if len(ot.PrimaryKeys) == 1 {
			if p := findProperty(ot, ot.PrimaryKeys[0]); p != nil {
				keys[ot.Table.ResourceID] = p.MappedField
			}
		}
	}
	return keys
}

func (c *draftedCounter) keyStats(ctx context.Context, table *interfaces.VegaTable, field string) (int64, int64, int64, error) {
	if key, drafted := c.keys[table.ID]; drafted {
		if field == key {
			return 1, 1, 0, nil
		}
		return 0, 0, 0, errNotCounted
	}
	return c.counter.keyStats(ctx, table, field)
}

func (c *draftedCounter) keyTupleStats(ctx context.Context, table *interfaces.VegaTable,
	fields []string) (int64, int64, int64, error) {
	if _, drafted := c.keys[table.ID]; drafted {
		return 0, 0, 0, errNotCounted
	}
	return c.counter.keyTupleStats(ctx, table, fields)
}

func (c *draftedCounter) joinStats(ctx context.Context, source *interfaces.VegaTable, field string,
	target *interfaces.VegaTable, key string) (int64, int64, int64, int64, error) {
	if _, drafted := c.keys[source.ID]; drafted {
		return 0, 0, 0, 0, errNotCounted
	}
	return c.counter.joinStats(ctx, source, field, target, key)
}

// checkAddCatalogs trims the catalogs to add and refuses none, an empty or repeated one, and one
// the draft was drafted from already.
func checkAddCatalogs(ctx context.Context, ids []string, drafted []string) ([]string, error) {
	invalid := httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidCatalogIDs)
	if len(ids) == 0 {
		return nil, invalid
	}
	trimmed := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(trimmed, id) {
			return nil, invalid
		}
		if slices.Contains(drafted, id) {
			return nil, invalid.WithErrorDetails(map[string]any{"drafted": id})
		}
		trimmed = append(trimmed, id)
	}
	return trimmed, nil
}

func (s *draftService) AddCatalogs(ctx context.Context, token string, projectID string,
	req interfaces.DraftAddCatalogs, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AddCatalogs")
	defer span.End()

	entry, err := s.unarchivedProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if entry.Owner.ID != caller.ID {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Draft_NotOwner)
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist || draft.Status != interfaces.DRAFT_STATUS_READY || draft.Items.Inputs == nil {
		// Before a draft there is nothing to add to: drafting reads every catalog of the project.
		span.SetStatus(codes.Error, "Draft not ready")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	added, err := checkAddCatalogs(ctx, req.CatalogIDs, draft.Items.Inputs.CatalogIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid catalogs")
		return nil, err
	}
	missing, err := s.va.MissingCatalogs(ctx, caller, added)
	if err != nil {
		span.SetStatus(codes.Error, "Check catalogs failed")
		return nil, internalError(ctx, "Check catalogs failed", err)
	}
	if len(missing) > 0 {
		span.SetStatus(codes.Error, "Catalog missing")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_CatalogMissing).
			WithErrorDetails(strings.Join(missing, ", "))
	}
	// A catalog the project has but the draft was not drafted from, such as one whose drafting
	// failed, is drafted again; the project keeps it once.
	catalogs := append([]string{}, entry.CatalogIDs...)
	for _, id := range added {
		if !slices.Contains(catalogs, id) {
			catalogs = append(catalogs, id)
		}
	}
	if !slices.Equal(catalogs, entry.CatalogIDs) {
		if _, err := s.ps.SetProjectCatalogs(ctx, token, projectID, catalogs, caller); err != nil {
			span.SetStatus(codes.Error, "Set catalogs failed")
			return nil, err
		}
	}

	ready := *draft
	drafting := *draft
	drafting.Status, drafting.UpdateTime = interfaces.DRAFT_STATUS_DRAFTING, max(time.Now().UnixMilli(), draft.UpdateTime+1)
	// The draft's items stay while the catalogs are drafted: nothing of them is discarded.
	staleBefore := time.Now().Add(-(s.setting.Timeout + time.Minute)).UnixMilli()
	started, err := s.da.StartDraft(ctx, &drafting, staleBefore, true)
	if err != nil {
		span.SetStatus(codes.Error, "Start draft failed")
		return nil, internalError(ctx, "Start draft failed", err)
	}
	if !started {
		span.SetStatus(codes.Error, "Drafting")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Drafting)
	}
	background := context.WithoutCancel(ctx)
	s.run(func() { s.draftAdded(background, &ready, added, caller) })
	span.SetStatus(codes.Ok, "")
	return &drafting, nil
}

// draftAdded drafts the tables of the catalogs added into the draft, and records the result: the
// draft with the new items, or the draft as it was when drafting failed, with why.
func (s *draftService) draftAdded(ctx context.Context, draft *interfaces.Draft, added []string,
	caller interfaces.AccountInfo) {
	ctx, cancel := context.WithTimeout(ctx, s.setting.Timeout)
	defer cancel()
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DraftAddedCatalogs")
	defer span.End()

	result := *draft
	record := interfaces.DraftCatalogsAdded{CatalogIDs: added, ObjectTypes: []string{}, RelationTypes: []string{},
		ValueTypes: []string{}}
	items, err := s.mergeAdded(ctx, draft, added, caller, &record)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Draft catalogs added to project %s failed: %v", draft.ProjectID, err))
		span.SetStatus(codes.Error, "Draft added catalogs failed")
		record.Error = err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			record.Error = FailureTimeout
		}
	} else {
		result.Items = items
	}
	result.Status = interfaces.DRAFT_STATUS_READY
	result.UpdateTime = max(time.Now().UnixMilli(), draft.UpdateTime+1)
	result.Items.Decisions = append(slices.Clone(result.Items.Decisions), &interfaces.DraftDecision{
		Kind: interfaces.DRAFT_KIND_DRAFT, Action: interfaces.DRAFT_DECISION_ADD_CATALOGS, After: record,
		By: caller, Time: result.UpdateTime,
	})
	// The outcome is recorded even when drafting ran out of time.
	if err := s.da.FinishDraft(context.WithoutCancel(ctx), &result); err != nil {
		otellog.LogError(ctx, fmt.Sprintf("Record draft of project %s failed", draft.ProjectID), err)
	}
}

// mergeAdded drafts the tables of the catalogs added with the tables of the draft's object types,
// which the new tables may relate to, and returns the draft's items with what only the new tables
// drafted: their object types and value types, and the relations with an end among them. Items the
// draft has stay exactly as they are; a relation's end at one of them takes its id and property.
func (s *draftService) mergeAdded(ctx context.Context, draft *interfaces.Draft, added []string,
	caller interfaces.AccountInfo, record *interfaces.DraftCatalogsAdded) (interfaces.DraftItems, error) {
	tables, unreadable, err := s.readCatalogs(ctx, added, caller)
	if err != nil {
		return interfaces.DraftItems{}, err
	}
	items := draft.Items
	keys := draftedKeys(&items)
	isNew := map[string]bool{}
	for _, t := range tables {
		isNew[t.ID] = true
	}
	oldIDs := []string{}
	for id := range keys {
		if !isNew[id] {
			oldIDs = append(oldIDs, id)
		}
	}
	slices.Sort(oldIDs)
	all := slices.Clone(tables)
	if len(oldIDs) > 0 {
		old, err := s.va.GetTables(ctx, caller, oldIDs)
		if err != nil {
			return interfaces.DraftItems{}, fmt.Errorf("read tables drafted: %w", err)
		}
		all = append(all, old...)
	}
	registered, err := s.ira.ListIDs(ctx, draft.ProjectID)
	if err != nil {
		return interfaces.DraftItems{}, fmt.Errorf("read registered ids: %w", err)
	}
	c := &draftedCounter{counter: &vegaCounter{va: s.va, account: caller, timeoutSeconds: s.setting.QueryTimeout}, keys: keys}
	d := newDrafter(s.setting, c).withRegisteredIDs(draft.ProjectID, registered).withNamingLanguage(draftLanguage(draft))
	drafted, err := d.draft(ctx, all, unreadable)
	if err != nil {
		return interfaces.DraftItems{}, err
	}

	merged := mergeDrafted(items, drafted, tables, unreadable, record)
	// Only the ids of what the new tables drafted are registered: those the draft has stand as given.
	recorded := []*interfaces.RegisteredID{}
	for _, id := range d.recorded {
		for _, t := range tables {
			if id.SourceKey == t.ID || strings.HasPrefix(id.SourceKey, t.ID+"/") {
				recorded = append(recorded, id)
				break
			}
		}
	}
	now := time.Now().UnixMilli()
	for _, id := range recorded {
		id.CreateTime, id.UpdateTime = now, now
	}
	if err := s.ira.RecordIDs(ctx, recorded); err != nil {
		return interfaces.DraftItems{}, fmt.Errorf("record ids: %w", err)
	}
	inputs := *items.Inputs
	addedInputs := draftInputsOf(s.setting, added, tables, unreadable)
	inputs.CatalogIDs = append(slices.Clone(items.Inputs.CatalogIDs), added...)
	inputs.Tables = append(slices.Clone(items.Inputs.Tables), addedInputs.Tables...)
	slices.SortFunc(inputs.Tables, func(a, b interfaces.DraftInputTable) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	inputs.Partial = inputs.Partial || addedInputs.Partial
	merged.Inputs = &inputs
	return merged, nil
}

// mergeDrafted adds to the draft's items what the new tables drafted: their object types and value
// types, and the relations with an end among them, each marked with the catalog that added it. The
// draft's items stay exactly as they are; a relation's end at one of them takes its id and property,
// and a relation needing a property the draft's object type does not have is left out. New ids
// meeting the draft's take the first free suffix.
func mergeDrafted(items interfaces.DraftItems, drafted interfaces.DraftItems, tables []*interfaces.VegaTable,
	unreadable []*interfaces.DraftTable, record *interfaces.DraftCatalogsAdded) interfaces.DraftItems {
	isNew := map[string]bool{}
	for _, t := range tables {
		isNew[t.ID] = true
	}
	draftedBy := map[string]*interfaces.DraftObjectType{}
	for _, ot := range items.ObjectTypes {
		draftedBy[ot.Table.ResourceID] = ot
	}
	// The ids the draft holds are taken; a new item meeting one takes the first free suffix.
	taken := map[string]map[string]bool{
		interfaces.DRAFT_KIND_OBJECT_TYPES: {}, interfaces.DRAFT_KIND_RELATION_TYPES: {}, interfaces.DRAFT_KIND_VALUE_TYPES: {},
	}
	for _, ot := range items.ObjectTypes {
		taken[interfaces.DRAFT_KIND_OBJECT_TYPES][ot.ID] = true
	}
	for _, rt := range items.RelationTypes {
		taken[interfaces.DRAFT_KIND_RELATION_TYPES][rt.ID] = true
	}
	for _, vt := range items.ValueTypes {
		taken[interfaces.DRAFT_KIND_VALUE_TYPES][vt.ID] = true
	}
	free := func(kind string, id string) string {
		if !taken[kind][id] {
			taken[kind][id] = true
			return id
		}
		return uniqueName(id, taken[kind])
	}

	merged := items
	merged.ObjectTypes = slices.Clone(items.ObjectTypes)
	merged.RelationTypes = slices.Clone(items.RelationTypes)
	merged.ValueTypes = slices.Clone(items.ValueTypes)
	merged.Excluded = slices.Clone(items.Excluded)
	merged.Unreadable = append(slices.Clone(items.Unreadable), unreadable...)
	// The ids and properties of the object types, as the draft has them: the drafted id of a table
	// the draft has maps to the draft's object type, a property to the one mapping the same field.
	objectTypeID, catalogOf := map[string]string{}, map[string]string{}
	propertyOf := map[string]map[string]string{}
	for _, ot := range drafted.ObjectTypes {
		if old, ok := draftedBy[ot.Table.ResourceID]; ok && !isNew[ot.Table.ResourceID] {
			objectTypeID[ot.ID] = old.ID
			byField := map[string]string{}
			for _, p := range old.Properties {
				byField[p.MappedField] = p.Name
			}
			propertyOf[ot.ID] = map[string]string{}
			for _, p := range ot.Properties {
				propertyOf[ot.ID][p.Name] = byField[p.MappedField]
			}
			continue
		}
		id := free(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID)
		objectTypeID[ot.ID], catalogOf[id] = id, ot.Table.CatalogID
		for _, e := range drafted.Excluded {
			if e.ObjectType == ot.ID {
				excluded := *e
				excluded.ObjectType = id
				merged.Excluded = append(merged.Excluded, &excluded)
			}
		}
		ot.ID, ot.AddedByCatalog = id, ot.Table.CatalogID
		merged.ObjectTypes = append(merged.ObjectTypes, ot)
		record.ObjectTypes = append(record.ObjectTypes, id)
	}
	property := func(objectType string, name string) (string, bool) {
		names, old := propertyOf[objectType]
		if !old {
			return name, true
		}
		mapped, ok := names[name]
		return mapped, ok && mapped != ""
	}
	for _, vt := range drafted.ValueTypes {
		if !isNew[vt.CodeTable.ResourceID] {
			continue
		}
		vt.ID, vt.AddedByCatalog = free(interfaces.DRAFT_KIND_VALUE_TYPES, vt.ID), vt.CodeTable.CatalogID
		// Only new object types use it: those the draft has stay as they are.
		vt.UsedBy = slices.DeleteFunc(vt.UsedBy, func(ref interfaces.DraftPropertyRef) bool {
			_, old := propertyOf[ref.ObjectType]
			return old
		})
		for i := range vt.UsedBy {
			vt.UsedBy[i].ObjectType = objectTypeID[vt.UsedBy[i].ObjectType]
		}
		vt.Adopted = vt.Adopted && len(vt.UsedBy) > 0
		merged.ValueTypes = append(merged.ValueTypes, vt)
		record.ValueTypes = append(record.ValueTypes, vt.ID)
	}
relations:
	for _, rt := range drafted.RelationTypes {
		_, sourceOld := propertyOf[rt.Source]
		_, targetOld := propertyOf[rt.Target]
		linkNew := rt.LinkTable != nil && isNew[rt.LinkTable.ResourceID]
		if sourceOld && targetOld && !linkNew {
			continue
		}
		// A mapping names the ends' properties: both in a direct one; the source's in a link table's
		// source mapping and the target's in its target mapping, beside the link table's fields.
		var ok bool
		for i := range rt.Mapping {
			if rt.Mapping[i].SourceProperty, ok = property(rt.Source, rt.Mapping[i].SourceProperty); !ok {
				continue relations
			}
			if rt.Mapping[i].TargetProperty, ok = property(rt.Target, rt.Mapping[i].TargetProperty); !ok {
				continue relations
			}
		}
		for i := range rt.SourceMapping {
			if rt.SourceMapping[i].SourceProperty, ok = property(rt.Source, rt.SourceMapping[i].SourceProperty); !ok {
				continue relations
			}
		}
		for i := range rt.TargetMapping {
			if rt.TargetMapping[i].TargetProperty, ok = property(rt.Target, rt.TargetMapping[i].TargetProperty); !ok {
				continue relations
			}
		}
		catalog := catalogOf[objectTypeID[rt.Source]]
		if catalog == "" {
			catalog = catalogOf[objectTypeID[rt.Target]]
		}
		if linkNew {
			catalog = rt.LinkTable.CatalogID
		}
		rt.Source, rt.Target = objectTypeID[rt.Source], objectTypeID[rt.Target]
		rt.ID, rt.AddedByCatalog = free(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID), catalog
		merged.RelationTypes = append(merged.RelationTypes, rt)
		// An end the draft has is adopted as people left it, which drafting did not know.
		if rt.Adopted && !(objectTypeAdopted(&merged, rt.Source) && objectTypeAdopted(&merged, rt.Target)) {
			rt.Adopted, rt.ProposedBecause = false, proposedEndNotAdopted
		}
		record.RelationTypes = append(record.RelationTypes, rt.ID)
	}

	return merged
}
