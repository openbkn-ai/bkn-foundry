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

func (c *draftedCounter) codeValues(ctx context.Context, table *interfaces.VegaTable, code string, name string,
	limit int64) ([]interfaces.DraftEnumValue, error) {
	if _, drafted := c.keys[table.ID]; drafted {
		return nil, errNotCounted
	}
	return c.counter.codeValues(ctx, table, code, name, limit)
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
	now := time.Now()
	if a := draft.Items.Adding; a != nil && a.Since >= now.Add(-(s.setting.Timeout+time.Minute)).UnixMilli() {
		span.SetStatus(codes.Error, "Adding")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Drafting)
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

	// The draft stays ready while the catalogs are drafted; it only says they are being added, written
	// on the draft as it was read, so that no edit made since is lost.
	read := draft.UpdateTime
	adding := &interfaces.DraftAdding{CatalogIDs: added, By: caller, Since: now.UnixMilli()}
	draft.Items.Adding = adding
	draft.UpdateTime = max(now.UnixMilli(), read+1)
	written, err := s.da.UpdateDraft(ctx, draft, read)
	if err != nil {
		span.SetStatus(codes.Error, "Update draft failed")
		return nil, internalError(ctx, "Update draft failed", err)
	}
	if !written {
		span.SetStatus(codes.Error, "Draft stale")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Stale)
	}
	snapshot := *draft
	background := context.WithoutCancel(ctx)
	s.run(func() { s.draftAdded(background, &snapshot, adding, caller) })
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

// maxAddAttempts bounds the writes of catalogs added that edits made meanwhile refuse.
const maxAddAttempts = 3

// draftAdded drafts the tables of the catalogs added, then adds what they drafted to the draft as it
// is by then, edits made meanwhile included, and records it: a decision of what was added, or why
// drafting failed, the draft being left as it is. It gives up when the draft was drafted again, or
// another adding took over, meanwhile.
func (s *draftService) draftAdded(ctx context.Context, snapshot *interfaces.Draft, adding *interfaces.DraftAdding,
	caller interfaces.AccountInfo) {
	timed, cancel := context.WithTimeout(ctx, s.setting.Timeout)
	defer cancel()
	timed, span := oteltrace.StartNamedInternalSpan(timed, "DraftAddedCatalogs")
	defer span.End()
	projectID := snapshot.ProjectID

	result, err := s.draftCatalogs(timed, snapshot, adding.CatalogIDs, caller)
	if err == nil {
		err = s.recordIDs(timed, result.recorded)
	}
	reason := ""
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Draft catalogs added to project %s failed: %v", projectID, err))
		span.SetStatus(codes.Error, "Draft added catalogs failed")
		reason = err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			reason = FailureTimeout
		}
	}
	// The outcome is recorded even when drafting ran out of time.
	for range maxAddAttempts {
		latest, exist, err := s.da.GetDraft(ctx, projectID)
		if err != nil || !exist || latest.Status != interfaces.DRAFT_STATUS_READY || latest.Items.Adding == nil ||
			latest.Items.Adding.Since != adding.Since {
			otellog.LogWarn(ctx, fmt.Sprintf("Catalogs added to project %s not recorded: the draft changed (%v)", projectID, err))
			return
		}
		record := interfaces.DraftCatalogsAdded{CatalogIDs: adding.CatalogIDs, ObjectTypes: []string{},
			RelationTypes: []string{}, ValueTypes: []string{}, Error: reason}
		items := latest.Items
		if reason == "" {
			items = s.applyAdded(items, result, adding.CatalogIDs, &record)
		}
		items.Adding = nil
		read := latest.UpdateTime
		latest.UpdateTime = max(time.Now().UnixMilli(), read+1)
		items.Decisions = append(slices.Clone(items.Decisions), &interfaces.DraftDecision{
			Kind: interfaces.DRAFT_KIND_DRAFT, Action: interfaces.DRAFT_DECISION_ADD_CATALOGS, After: record,
			By: caller, Time: latest.UpdateTime,
		})
		latest.Items = items
		written, err := s.da.UpdateDraft(ctx, latest, read)
		if err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Record catalogs added to project %s failed", projectID), err)
			return
		}
		if written {
			return
		}
	}
	otellog.LogWarn(ctx, fmt.Sprintf("Catalogs added to project %s not recorded: the draft kept changing", projectID))
}

// addedDraft is what drafting the tables of catalogs added gave: the items drafted with the draft's
// own tables, the new tables, those of them drafting could not read, and the ids to register.
type addedDraft struct {
	drafted    interfaces.DraftItems
	tables     []*interfaces.VegaTable
	unreadable []*interfaces.DraftTable
	recorded   []*interfaces.RegisteredID
}

// draftCatalogs drafts the tables of the catalogs added with the tables of the draft's object types,
// which the new tables may relate to and which are not counted again.
func (s *draftService) draftCatalogs(ctx context.Context, draft *interfaces.Draft, added []string,
	caller interfaces.AccountInfo) (*addedDraft, error) {
	tables, unreadable, err := s.readCatalogs(ctx, added, caller)
	if err != nil {
		return nil, err
	}
	keys := draftedKeys(&draft.Items)
	isNew := map[string]bool{}
	for _, t := range tables {
		isNew[t.ID] = true
	}
	for _, t := range unreadable {
		isNew[t.ResourceID] = true
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
			return nil, fmt.Errorf("read tables drafted: %w", err)
		}
		all = append(all, old...)
	}
	registered, err := s.ira.ListIDs(ctx, draft.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("read registered ids: %w", err)
	}
	c := &draftedCounter{counter: &vegaCounter{va: s.va, account: caller, timeoutSeconds: s.setting.QueryTimeout}, keys: keys}
	d := newDrafter(s.setting, c).withRegisteredIDs(draft.ProjectID, registered).withNamingLanguage(draftLanguage(draft))
	drafted, err := d.draft(ctx, all, unreadable)
	if err != nil {
		return nil, err
	}
	result := &addedDraft{drafted: drafted, tables: tables}
	// Tables drafting itself could not read, such as a code table whose values it could not query,
	// are unreadable as those the catalogs listed but vega did not give.
	for _, t := range drafted.Unreadable {
		if isNew[t.ResourceID] {
			result.unreadable = append(result.unreadable, t)
		}
	}
	// Only the ids of what the new tables drafted are registered: those the draft has stand as given.
	for _, id := range d.recorded {
		for _, t := range tables {
			if id.SourceKey == t.ID || strings.HasPrefix(id.SourceKey, t.ID+"/") {
				result.recorded = append(result.recorded, id)
				break
			}
		}
	}
	return result, nil
}

// recordIDs registers ids drafted, as drafting does before its draft is recorded.
func (s *draftService) recordIDs(ctx context.Context, ids []*interfaces.RegisteredID) error {
	now := time.Now().UnixMilli()
	for _, id := range ids {
		id.CreateTime, id.UpdateTime = now, now
	}
	if err := s.ira.RecordIDs(ctx, ids); err != nil {
		return fmt.Errorf("record ids: %w", err)
	}
	return nil
}

// applyAdded adds what the catalogs added drafted to the draft's items as they are now, and their
// tables to what the draft was made from.
func (s *draftService) applyAdded(items interfaces.DraftItems, result *addedDraft, added []string,
	record *interfaces.DraftCatalogsAdded) interfaces.DraftItems {
	merged := mergeDrafted(items, result.drafted, result.tables, result.unreadable, record)
	inputs := interfaces.DraftInputs{}
	if items.Inputs != nil {
		inputs = *items.Inputs
	}
	addedInputs := draftInputsOf(s.setting, added, result.tables, result.unreadable)
	inputs.CatalogIDs = append(slices.Clone(inputs.CatalogIDs), added...)
	inputs.Tables = append(slices.Clone(inputs.Tables), addedInputs.Tables...)
	slices.SortFunc(inputs.Tables, func(a, b interfaces.DraftInputTable) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	inputs.Partial = inputs.Partial || addedInputs.Partial
	merged.Inputs = &inputs
	return merged
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
