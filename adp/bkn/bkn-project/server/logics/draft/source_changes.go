// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// mainRef is the branch that holds the published definitions.
const mainRef = "refs/heads/" + interfaces.GIT_MAIN_BRANCH

// objectTypesDir holds the object types in a lineage's definitions.
const objectTypesDir = "object_types/"

// sourceTables are the tables a project binds as vega-backend now records them: those read, and
// those gone. A table the caller may not see is in neither.
type sourceTables struct {
	tables  map[string]*interfaces.VegaTable
	missing map[string]bool
}

// readSources reads the tables ids as the caller: which are gone, then the structure of the rest.
func (s *draftService) readSources(ctx context.Context, caller interfaces.AccountInfo, ids []string) (*sourceTables, error) {
	read := &sourceTables{tables: map[string]*interfaces.VegaTable{}, missing: map[string]bool{}}
	if len(ids) == 0 {
		return read, nil
	}
	missing, err := s.va.MissingTables(ctx, caller, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range missing {
		read.missing[id] = true
	}
	there := []string{}
	for _, id := range ids {
		if !read.missing[id] {
			there = append(there, id)
		}
	}
	if len(there) == 0 {
		return read, nil
	}
	tables, err := s.va.GetTables(ctx, caller, there)
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		read.tables[t.ID] = t
	}
	return read, nil
}

// draftSourceIDs are the tables of a ready draft's object types and link tables, each once, in the
// draft's order. Object types not adopted are included: adopting one later takes its table as it is.
func draftSourceIDs(draft *interfaces.Draft) []string {
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil
	}
	ids, seen := []string{}, map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, ot := range draft.Items.ObjectTypes {
		add(ot.Table.ResourceID)
	}
	for _, rt := range draft.Items.RelationTypes {
		if rt.LinkTable != nil {
			add(rt.LinkTable.ResourceID)
		}
	}
	return ids
}

// sourceSignature stands for the draft's update time and the structure of the draft's tables read.
func sourceSignature(draft *interfaces.Draft, read *sourceTables) string {
	hash := sha256.New()
	if draft != nil {
		fmt.Fprintf(hash, "draft:%d\n", draft.UpdateTime)
	}
	for _, id := range draftSourceIDs(draft) {
		if t, ok := read.tables[id]; ok {
			fmt.Fprintf(hash, "%s:%s\n", id, fingerprint(t))
		} else {
			fmt.Fprintf(hash, "%s:-\n", id)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// columnName is a column's business name in language.
func columnName(c interfaces.VegaColumn, language string) string {
	name, _ := fieldNames(c, language)
	return name
}

// keyFields are the fields of a table that carry identity and relations: privacy rules never drop
// them, as drafting does not.
func keyFields(table *interfaces.VegaTable) map[string]bool {
	keys := map[string]bool{}
	key, _ := tableKey(table)
	for _, f := range key {
		keys[f] = true
	}
	for _, fk := range table.ForeignKeys {
		for _, f := range fk.Columns {
			keys[f] = true
		}
	}
	return keys
}

// excludedBy is the rule drafting leaves a column out by, or "".
func excludedBy(c interfaces.VegaColumn, keys map[string]bool, privacyKeywords []string) (string, string) {
	if bknTypes[c.Type] == "" {
		return excludedUnsupported, c.Type
	}
	if keyword := privacyKeyword(c, privacyKeywords); keyword != "" && !keys[c.Name] {
		return excludedPrivacy, keyword
	}
	return "", ""
}

// mappedProperty is a property as the comparison reads it, of a draft or of published definitions.
type mappedProperty struct {
	name, displayName, typ, field string
}

// compareColumns lists how table differs from properties: columns no property maps and not listed
// in excluded, properties whose column is gone, and properties whose column's type maps to another.
// keyed are the properties the object type is identified or displayed by; joins names the relation
// types joining by each property.
func compareColumns(properties []mappedProperty, excluded map[string]bool, keyed map[string]bool,
	joins map[string][]interfaces.ReleaseItem, table *interfaces.VegaTable, privacyKeywords []string,
	language string) []interfaces.SourceColumnChange {
	changes := []interfaces.SourceColumnChange{}
	columns := map[string]interfaces.VegaColumn{}
	for _, c := range table.Columns {
		columns[c.Name] = c
	}
	mapped := map[string]bool{}
	for _, p := range properties {
		if p.field == "" {
			continue
		}
		mapped[p.field] = true
		c, ok := columns[p.field]
		switch {
		case !ok:
			changes = append(changes, interfaces.SourceColumnChange{
				Change: interfaces.SOURCE_CHANGE_REMOVED, Field: p.field, FieldName: p.displayName,
				Property: p.name, PropertyName: p.displayName, FromType: p.typ,
				Key: keyed[p.name], Relations: joins[p.name],
			})
		case bknTypes[c.Type] != p.typ:
			to := bknTypes[c.Type]
			changes = append(changes, interfaces.SourceColumnChange{
				Change: interfaces.SOURCE_CHANGE_RETYPED, Field: p.field, FieldName: columnName(c, language),
				Property: p.name, PropertyName: p.displayName, FromType: p.typ, ToType: to,
				Key: keyed[p.name], Unsupported: to == "", Relations: joins[p.name],
			})
		}
	}
	keys := keyFields(table)
	for _, c := range table.Columns {
		if mapped[c.Name] || excluded[c.Name] {
			continue
		}
		rule, _ := excludedBy(c, keys, privacyKeywords)
		changes = append(changes, interfaces.SourceColumnChange{
			Change: interfaces.SOURCE_CHANGE_ADDED, Field: c.Name, FieldName: columnName(c, language),
			ToType: bknTypes[c.Type], Excluded: rule,
		})
	}
	return changes
}

// joinsOf names, for each property of the object type id, the draft relation types joining by it.
func joinsOf(items *interfaces.DraftItems, id string) map[string][]interfaces.ReleaseItem {
	joins := map[string][]interfaces.ReleaseItem{}
	for _, r := range items.RelationTypes {
		item := interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: r.ID, Name: r.Name}
		used := map[string]bool{}
		for _, m := range r.Mapping {
			if r.Source == id {
				used[m.SourceProperty] = true
			}
			if r.Target == id {
				used[m.TargetProperty] = true
			}
		}
		for _, m := range r.SourceMapping {
			if r.Source == id {
				used[m.SourceProperty] = true
			}
		}
		for _, m := range r.TargetMapping {
			if r.Target == id {
				used[m.TargetProperty] = true
			}
		}
		for p := range used {
			joins[p] = append(joins[p], item)
		}
	}
	return joins
}

// draftObjectTypeChanges compares a draft object type with its table.
func draftObjectTypeChanges(items *interfaces.DraftItems, ot *interfaces.DraftObjectType, table *interfaces.VegaTable,
	privacyKeywords []string, language string) []interfaces.SourceColumnChange {
	properties := make([]mappedProperty, 0, len(ot.Properties))
	for _, p := range ot.Properties {
		properties = append(properties, mappedProperty{name: p.Name, displayName: p.DisplayName, typ: p.Type, field: p.MappedField})
	}
	excluded := map[string]bool{}
	for _, e := range items.Excluded {
		if e.ObjectType == ot.ID {
			excluded[e.Field] = true
		}
	}
	keyed := map[string]bool{ot.DisplayKey: true}
	for _, k := range ot.PrimaryKeys {
		keyed[k] = true
	}
	return compareColumns(properties, excluded, keyed, joinsOf(items, ot.ID), table, privacyKeywords, language)
}

// linkFieldsRemoved are the link table fields a relation type joins by that the table no longer has.
func linkFieldsRemoved(rt *interfaces.DraftRelationType, table *interfaces.VegaTable) []string {
	removed := []string{}
	for _, f := range linkFields(rt) {
		if _, ok := column(table, f); !ok {
			removed = append(removed, f)
		}
	}
	return removed
}

// linkFields are the link table fields a relation type joins its ends by.
func linkFields(rt *interfaces.DraftRelationType) []string {
	fields := []string{}
	for _, m := range rt.SourceMapping {
		fields = append(fields, m.TargetProperty)
	}
	for _, m := range rt.TargetMapping {
		fields = append(fields, m.SourceProperty)
	}
	return fields
}

// draftSourceChanges compares the draft's object types and link tables with the tables read. Tables
// gone are listed apart, by the draft items reading them.
func draftSourceChanges(draft *interfaces.Draft, read *sourceTables, privacyKeywords []string) *interfaces.SourceChanges {
	changes := &interfaces.SourceChanges{
		State:         interfaces.SOURCE_CHANGES_IN_STEP,
		ObjectTypes:   []interfaces.SourceObjectTypeChanges{},
		RelationTypes: []interfaces.SourceRelationTypeChange{},
		MissingTables: []interfaces.ProjectStatusAffected{},
	}
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		return changes
	}
	language := draftLanguage(draft)
	for _, ot := range draft.Items.ObjectTypes {
		id := ot.Table.ResourceID
		if read.missing[id] {
			changes.MissingTables = append(changes.MissingTables, interfaces.ProjectStatusAffected{
				Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: ot.ID, Name: ot.Name, Table: ot.Table.Name,
			})
			continue
		}
		table, ok := read.tables[id]
		if !ok {
			continue
		}
		if c := draftObjectTypeChanges(&draft.Items, ot, table, privacyKeywords, language); len(c) > 0 {
			changes.ObjectTypes = append(changes.ObjectTypes, interfaces.SourceObjectTypeChanges{
				ID: ot.ID, Name: ot.Name, Table: ot.Table.Name, InDraft: true, Changes: c,
			})
		}
	}
	for _, rt := range draft.Items.RelationTypes {
		if rt.LinkTable == nil {
			continue
		}
		if read.missing[rt.LinkTable.ResourceID] {
			changes.MissingTables = append(changes.MissingTables, interfaces.ProjectStatusAffected{
				Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ID: rt.ID, Name: rt.Name, Table: rt.LinkTable.Name,
			})
			continue
		}
		table, ok := read.tables[rt.LinkTable.ResourceID]
		if !ok {
			continue
		}
		if removed := linkFieldsRemoved(rt, table); len(removed) > 0 {
			changes.RelationTypes = append(changes.RelationTypes, interfaces.SourceRelationTypeChange{
				ID: rt.ID, Name: rt.Name, Table: rt.LinkTable.Name, Removed: removed,
			})
		}
	}
	changes.Signature = sourceSignature(draft, read)
	if len(changes.ObjectTypes) > 0 || len(changes.RelationTypes) > 0 {
		changes.State = interfaces.SOURCE_CHANGES_CHANGED
	}
	return changes
}

// publishedObjectTypes are the object types of the published definitions that read a table, in path
// order; one that does not parse is left out, as the release review leaves it.
func publishedObjectTypes(files map[string][]byte) []*bkn.BknObjectType {
	paths := []string{}
	for p := range files {
		if strings.HasPrefix(p, objectTypesDir) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	types := []*bkn.BknObjectType{}
	for _, p := range paths {
		if ot, err := bkn.ParseObjectTypeFile(string(files[p]), p); err == nil && ot.DataSource != nil && ot.DataSource.ID != "" {
			types = append(types, ot)
		}
	}
	return types
}

// withPublished adds the comparison of the published object types: one the draft has is marked
// published, as the draft is what drafting changes; one only published is compared as published.
func withPublished(changes *interfaces.SourceChanges, draft *interfaces.Draft, published []*bkn.BknObjectType,
	read *sourceTables, privacyKeywords []string, language string) {
	inDraft := map[string]bool{}
	if draft != nil && draft.Status == interfaces.DRAFT_STATUS_READY {
		for _, ot := range draft.Items.ObjectTypes {
			inDraft[ot.ID] = true
		}
	}
	for i := range changes.ObjectTypes {
		for _, ot := range published {
			if ot.ID == changes.ObjectTypes[i].ID {
				changes.ObjectTypes[i].Published = true
			}
		}
	}
	for _, ot := range published {
		if inDraft[ot.ID] {
			continue
		}
		if read.missing[ot.DataSource.ID] {
			changes.MissingTables = append(changes.MissingTables, interfaces.ProjectStatusAffected{
				Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: ot.ID, Name: ot.Name, Table: ot.DataSource.Name,
			})
			continue
		}
		table, ok := read.tables[ot.DataSource.ID]
		if !ok {
			continue
		}
		properties := make([]mappedProperty, 0, len(ot.DataProperties))
		for _, p := range ot.DataProperties {
			properties = append(properties, mappedProperty{name: p.Name, displayName: p.DisplayName, typ: p.Type, field: p.MappedField})
		}
		keyed := map[string]bool{ot.DisplayKey: true}
		for _, k := range ot.PrimaryKeys {
			keyed[k] = true
		}
		if c := compareColumns(properties, nil, keyed, nil, table, privacyKeywords, language); len(c) > 0 {
			changes.ObjectTypes = append(changes.ObjectTypes, interfaces.SourceObjectTypeChanges{
				ID: ot.ID, Name: ot.Name, Table: ot.DataSource.Name, Published: true, Changes: c,
			})
		}
	}
	if len(changes.ObjectTypes) > 0 || len(changes.RelationTypes) > 0 {
		changes.State = interfaces.SOURCE_CHANGES_CHANGED
	}
}

// GetSourceChanges reads the draft, the published definitions on main and their tables, as the caller.
func (s *draftService) GetSourceChanges(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.SourceChanges, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetSourceChanges")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		draft = nil
	}
	files, err := s.gs.ReadTree(ctx, entry.KNID, mainRef)
	if err != nil {
		span.SetStatus(codes.Error, "Read definitions failed")
		return nil, internalError(ctx, "Read definitions failed", err)
	}
	published := publishedObjectTypes(files)

	ids := draftSourceIDs(draft)
	for _, ot := range published {
		if !slices.Contains(ids, ot.DataSource.ID) {
			ids = append(ids, ot.DataSource.ID)
		}
	}
	language := entry.NamingLanguage
	if draft != nil {
		language = draftLanguage(draft)
	}
	read, err := s.readSources(ctx, caller, ids)
	if err != nil {
		// Nothing is known to be in step; the reply says so rather than failing the read.
		span.SetStatus(codes.Ok, "")
		return &interfaces.SourceChanges{
			ProjectID: projectID, State: interfaces.SOURCE_CHANGES_UNREACHABLE,
			ObjectTypes: []interfaces.SourceObjectTypeChanges{}, RelationTypes: []interfaces.SourceRelationTypeChange{},
			MissingTables: []interfaces.ProjectStatusAffected{},
		}, nil
	}
	changes := draftSourceChanges(draft, read, s.setting.PrivacyKeywords)
	changes.ProjectID = projectID
	withPublished(changes, draft, published, read, s.setting.PrivacyKeywords, language)
	span.SetStatus(codes.Ok, "")
	return changes, nil
}

// DraftSourceChanges applies the source changes of the draft's tables to the draft, as drafting would
// have from the tables as they are now, keeping every decision people made.
func (s *draftService) DraftSourceChanges(ctx context.Context, token string, projectID string,
	req interfaces.SourceChangesDraft, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DraftSourceChanges")
	defer span.End()

	entry, err := s.unarchivedProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Project not editable")
		return nil, err
	}
	if entry.Owner.ID != caller.ID {
		span.SetStatus(codes.Error, "Not owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Draft_NotOwner)
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "No draft")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
	}
	if draft.Status != interfaces.DRAFT_STATUS_READY {
		span.SetStatus(codes.Error, "Draft not ready")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	read, err := s.readSources(ctx, caller, draftSourceIDs(draft))
	if err != nil {
		span.SetStatus(codes.Error, "Read tables failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, perrors.BknProject_Draft_SourceUnreachable).
			WithErrorDetails(err.Error())
	}
	changes := draftSourceChanges(draft, read, s.setting.PrivacyKeywords)
	if req.Signature == "" || req.Signature != changes.Signature {
		span.SetStatus(codes.Error, "Source changes outdated")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_SourceChangesOutdated)
	}
	if changes.State != interfaces.SOURCE_CHANGES_CHANGED {
		span.SetStatus(codes.Error, "No source changes")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NoSourceChanges)
	}
	decisions := applySourceChanges(&draft.Items, changes, read, s.setting.PrivacyKeywords, s.setting.NameFieldKeywords)
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

// applySourceChanges changes the draft items as changes say, one decision per item changed: an added
// column becomes a property, or an excluded field by the rule that leaves it out; a removed column
// takes its property away; a retyped column retypes its property, or takes it away when BKN cannot
// map the type. An object type that loses what identifies it waits for a person to choose its
// identity again, and a relation that loses a field it joins by, to adopt it again. The fingerprints
// of the tables changed are those read.
func applySourceChanges(items *interfaces.DraftItems, changes *interfaces.SourceChanges, read *sourceTables,
	privacyKeywords []string, nameKeywords []string) []*interfaces.DraftDecision {
	decisions := []*interfaces.DraftDecision{}
	language := itemsLanguage(items)
	changedTables := map[string]bool{}
	unadopt := map[string]bool{}
	for _, otc := range changes.ObjectTypes {
		if !otc.InDraft {
			continue
		}
		ot := findObjectType(items, otc.ID)
		table := read.tables[ot.Table.ResourceID]
		changedTables[table.ID] = true
		taken := map[string]bool{}
		for _, p := range ot.Properties {
			taken[p.Name] = true
		}
		keys := keyFields(table)
		gone := map[string]bool{}
		lostKey := false
		for _, c := range otc.Changes {
			switch {
			case c.Change == interfaces.SOURCE_CHANGE_ADDED:
				col, _ := column(table, c.Field)
				if rule, keyword := excludedBy(col, keys, privacyKeywords); rule != "" {
					items.Excluded = append(items.Excluded, &interfaces.DraftExcludedField{
						ObjectType: ot.ID, Field: col.Name, Reason: rule, Keyword: keyword,
					})
					continue
				}
				ot.Properties = append(ot.Properties, newProperty(col, uniqueName(idFrom(col.Name, idPrefixProperty), taken), language))
			case c.Change == interfaces.SOURCE_CHANGE_REMOVED || c.Unsupported:
				gone[c.Property] = true
				if c.Unsupported {
					col, _ := column(table, c.Field)
					items.Excluded = append(items.Excluded, &interfaces.DraftExcludedField{
						ObjectType: ot.ID, Field: c.Field, Reason: excludedUnsupported, Keyword: col.Type,
					})
				}
				if slices.Contains(ot.PrimaryKeys, c.Property) {
					lostKey = true
				}
				for _, r := range c.Relations {
					unadopt[r.ID] = true
				}
			default:
				p := findProperty(ot, c.Property)
				p.Type = c.ToType
				if slices.Contains(ot.PrimaryKeys, c.Property) && !keyTypes[c.ToType] {
					lostKey = true
				}
			}
		}
		ot.Properties = slices.DeleteFunc(ot.Properties, func(p *interfaces.DraftProperty) bool { return gone[p.Name] })
		dropAliasesOfOtherProperties(ot.Properties)
		for _, vt := range items.ValueTypes {
			vt.UsedBy = slices.DeleteFunc(vt.UsedBy, func(ref interfaces.DraftPropertyRef) bool {
				return ref.ObjectType == ot.ID && gone[ref.Property]
			})
		}
		if gone[ot.IncrementalKey] {
			ot.IncrementalKey = ""
		}
		if lostKey {
			ot.PrimaryKeys, ot.NeedsIdentity = nil, true
			ot.Key = interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_NONE}
		}
		if gone[ot.DisplayKey] || lostKey {
			fields := map[string]string{}
			for _, p := range ot.Properties {
				fields[p.MappedField] = p.Name
			}
			ot.DisplayKey = displayKey(table, fields, ot.PrimaryKeys, nameKeywords)
		}
		decisions = append(decisions, &interfaces.DraftDecision{
			Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: ot.ID, Action: interfaces.DRAFT_DECISION_SYNC,
			Before: nil, After: otc.Changes,
		})
	}
	for _, rtc := range changes.RelationTypes {
		unadopt[rtc.ID] = true
		for _, rt := range items.RelationTypes {
			if rt.ID == rtc.ID {
				changedTables[rt.LinkTable.ResourceID] = true
			}
		}
	}
	for _, rt := range items.RelationTypes {
		if !unadopt[rt.ID] {
			continue
		}
		rt.Adopted, rt.ProposedBecause = false, interfaces.DRAFT_PROPOSED_FIELD_REMOVED
		decisions = append(decisions, &interfaces.DraftDecision{
			Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: rt.ID, Action: interfaces.DRAFT_DECISION_SYNC,
			Before: nil, After: interfaces.DRAFT_PROPOSED_FIELD_REMOVED,
		})
	}
	if items.Inputs != nil {
		for i, t := range items.Inputs.Tables {
			if table, ok := read.tables[t.ResourceID]; ok && changedTables[t.ResourceID] {
				items.Inputs.Tables[i].Fingerprint = fingerprint(table)
			}
		}
	}
	return decisions
}
