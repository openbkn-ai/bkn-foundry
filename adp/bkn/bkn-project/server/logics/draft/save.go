// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// excludedByPerson is a field a person took out of the object type: saving it as a property again
// restores it.
const excludedByPerson = "removed"

// snapshot is what saving records of an object type.
func snapshot(ot *interfaces.DraftObjectType) interfaces.DraftObjectTypeSnapshot {
	s := interfaces.DraftObjectTypeSnapshot{
		Properties:  []interfaces.DraftPropertySnapshot{},
		PrimaryKeys: append([]string{}, ot.PrimaryKeys...),
		DisplayKey:  ot.DisplayKey,
	}
	for _, p := range ot.Properties {
		s.Properties = append(s.Properties, interfaces.DraftPropertySnapshot{
			Name: p.Name, DisplayName: p.DisplayName, Description: p.Description, Type: p.Type, MappedField: p.MappedField,
		})
	}
	return s
}

// SaveObjectType saves an object type of a ready draft as a whole. The properties kept by name keep
// their field; new ones map fields of the table no property maps, named as drafting names them when
// no name is given; a field the privacy or type rules excluded is restored by mapping it. Properties
// left out are removed, unless a relation joins by them. The identity, title and update field follow
// the rules of choosing them one by one.
func (s *draftService) SaveObjectType(ctx context.Context, token string, projectID string, itemID string,
	save interfaces.DraftObjectTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SaveObjectType")
	defer span.End()

	name, comment, incremental := save.Name, save.Comment, save.IncrementalKey
	edit := interfaces.DraftItemEdit{UpdateTime: save.UpdateTime, Name: &name, Description: &comment,
		IncrementalKey: &incremental}
	if err := checkEdit(ctx, &edit); err != nil {
		span.SetStatus(codes.Error, "Invalid save")
		return nil, err
	}
	draft, err := s.readyDraft(ctx, token, projectID, save.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	ot := findObjectType(&draft.Items, itemID)
	if ot == nil {
		span.SetStatus(codes.Error, "Object type not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	tables, err := s.va.GetTables(ctx, caller, []string{ot.Table.ResourceID})
	if err != nil {
		span.SetStatus(codes.Error, "Read table failed")
		return nil, internalError(ctx, "Read table failed", err)
	}
	if len(tables) == 0 {
		span.SetStatus(codes.Error, "Table not readable")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_FieldNotFound)
	}
	registered, err := s.ira.ListIDs(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Read registered ids failed")
		return nil, internalError(ctx, "Read registered ids failed", err)
	}
	d := newDrafter(s.setting, nil).withRegisteredIDs(projectID, registered).withNamingLanguage(draftLanguage(draft))

	before := snapshot(ot)
	if err := saveProperties(ctx, &draft.Items, ot, tables[0], save.DataProperties, d); err != nil {
		span.SetStatus(codes.Error, "Invalid properties")
		return nil, err
	}
	keys := save.PrimaryKeys
	if len(keys) > 0 || !ot.NeedsIdentity {
		properties, err := identityProperties(ctx, ot, keys)
		if err != nil {
			span.SetStatus(codes.Error, "Invalid identity")
			return nil, err
		}
		if ot.NeedsIdentity || !slices.Equal(ot.PrimaryKeys, keys) {
			s.setIdentity(ctx, draft, ot, properties, caller)
		}
	}
	if save.DisplayKey != "" && findProperty(ot, save.DisplayKey) == nil {
		span.SetStatus(codes.Error, "Invalid title")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidProperty).
			WithErrorDetails(map[string]any{"display_key": save.DisplayKey})
	}
	ot.DisplayKey = save.DisplayKey

	decisions, err := applyEdit(ctx, &draft.Items, interfaces.DRAFT_KIND_OBJECT_TYPES, itemID, edit)
	if err != nil {
		span.SetStatus(codes.Error, "Edit refused")
		return nil, err
	}
	if ot.IncrementalKey != "" && slices.ContainsFunc(decisions, func(d *interfaces.DraftDecision) bool {
		return d.Action == interfaces.DRAFT_DECISION_INCREMENTAL
	}) {
		if err := s.checkIncrementalField(ctx, ot, caller); err != nil {
			span.SetStatus(codes.Error, "Update field refused")
			return nil, err
		}
	}
	if after := snapshot(ot); !snapshotsEqual(before, after) {
		decisions = append(decisions, &interfaces.DraftDecision{
			Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: itemID, Action: interfaces.DRAFT_DECISION_SAVE,
			Before: before, After: after,
		})
	}
	// Ids are registered before the draft is written, as drafting does: later drafts give them again.
	now := time.Now().UnixMilli()
	for _, id := range d.recorded {
		id.CreateTime, id.UpdateTime = now, now
	}
	if err := s.ira.RecordIDs(ctx, d.recorded); err != nil {
		span.SetStatus(codes.Error, "Record ids failed")
		return nil, internalError(ctx, "Record ids failed", err)
	}
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

func snapshotsEqual(a interfaces.DraftObjectTypeSnapshot, b interfaces.DraftObjectTypeSnapshot) bool {
	return slices.Equal(a.Properties, b.Properties) && slices.Equal(a.PrimaryKeys, b.PrimaryKeys) &&
		a.DisplayKey == b.DisplayKey
}

// saveProperties makes the object type's properties those saved, refusing a property that maps a
// field the table does not have, a field another property maps, a type the field does not map to, or
// an invalid name, and a removal a relation would lose its join by.
func saveProperties(ctx context.Context, items *interfaces.DraftItems, ot *interfaces.DraftObjectType,
	table *interfaces.VegaTable, saved []interfaces.DraftDataPropertySave, d *drafter) error {
	invalid := func(details map[string]any) error {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidProperty).
			WithErrorDetails(details)
	}
	language := itemsLanguage(items)
	byName := map[string]*interfaces.DraftProperty{}
	for _, p := range ot.Properties {
		byName[p.Name] = p
	}
	kept, fieldsUsed, names := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range saved {
		if p.MappedField == nil || p.MappedField.Name == "" {
			return invalid(map[string]any{"property": p.Name, "reason": "no_mapped_field"})
		}
		if fieldsUsed[p.MappedField.Name] {
			return invalid(map[string]any{"field": p.MappedField.Name, "reason": "field_mapped_twice"})
		}
		fieldsUsed[p.MappedField.Name] = true
		if existing, ok := byName[p.Name]; ok {
			if existing.MappedField != p.MappedField.Name || (p.Type != "" && p.Type != existing.Type) {
				return invalid(map[string]any{"property": p.Name, "reason": "mapping_changed"})
			}
			kept[p.Name] = true
		}
	}
	// What is left out is removed; a relation joining by it keeps it.
	joins := joinsOf(items, ot.ID)
	uses := []interfaces.DraftPropertyUse{}
	for _, p := range ot.Properties {
		if kept[p.Name] {
			continue
		}
		for _, r := range joins[p.Name] {
			rt := findRelationType(items, r.ID)
			uses = append(uses, interfaces.DraftPropertyUse{Property: p.Name, Relation: r,
				Source: objectTypeItem(items, rt.Source), Target: objectTypeItem(items, rt.Target)})
		}
	}
	if len(uses) > 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_PropertyInUse).
			WithErrorDetails(uses)
	}

	taken := map[string]bool{}
	for name := range kept {
		taken[name] = true
	}
	d.reserve(interfaces.ID_KIND_PROPERTIES, propertySourceKey(table.ID, ""), taken)
	properties := []*interfaces.DraftProperty{}
	for _, p := range saved {
		displayName := strings.TrimSpace(p.DisplayName)
		description := strings.TrimSpace(p.Comment)
		if utf8.RuneCountInString(displayName) > interfaces.DRAFT_NAME_MAX_LENGTH ||
			utf8.RuneCountInString(description) > interfaces.DRAFT_DESCRIPTION_MAX_LENGTH {
			return invalid(map[string]any{"property": p.Name, "reason": "too_long"})
		}
		property, ok := byName[p.Name]
		if !ok {
			c, found := column(table, p.MappedField.Name)
			typ := bknTypes[c.Type]
			if !found || typ == "" || (p.Type != "" && p.Type != typ) {
				return invalid(map[string]any{"field": p.MappedField.Name, "reason": "field_not_mappable"})
			}
			name := p.Name
			switch {
			case name == "":
				name = d.itemID(interfaces.ID_KIND_PROPERTIES, propertySourceKey(table.ID, c.Name), c.Name, c.APIName,
					idPrefixProperty, taken)
			case apiName(name) != name || taken[name] || names[name]:
				return invalid(map[string]any{"property": name, "reason": "invalid_name"})
			}
			names[name] = true
			property = newProperty(c, name, language)
			items.Excluded = slices.DeleteFunc(items.Excluded, func(e *interfaces.DraftExcludedField) bool {
				return e.ObjectType == ot.ID && e.Field == c.Name
			})
		}
		if displayName != "" {
			property.DisplayName = displayName
		}
		property.Description, property.MissingDescription = description, description == ""
		properties = append(properties, property)
	}
	for _, p := range ot.Properties {
		if kept[p.Name] {
			continue
		}
		// A field taken out stays restorable by saving it again.
		items.Excluded = append(items.Excluded, &interfaces.DraftExcludedField{
			ObjectType: ot.ID, Field: p.MappedField, Reason: excludedByPerson,
		})
		for _, vt := range items.ValueTypes {
			vt.UsedBy = slices.DeleteFunc(vt.UsedBy, func(ref interfaces.DraftPropertyRef) bool {
				return ref.ObjectType == ot.ID && ref.Property == p.Name
			})
		}
	}
	ot.Properties = properties
	dropAliasesOfOtherProperties(ot.Properties)
	return nil
}

// objectTypeItem names an object type of the draft for people to read.
func objectTypeItem(items *interfaces.DraftItems, id string) interfaces.ReleaseItem {
	item := interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: id, Name: id}
	if ot := findObjectType(items, id); ot != nil {
		item.Name = ot.Name
	}
	return item
}
