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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// People edit a ready draft item by item. Each change is recorded as a decision with what it
// replaced, and a draft is written only if no one changed it since it was read.

// draftItem is the part of an object, relation or value type people edit.
type draftItem struct {
	kind        string
	id          string
	name        *string
	description *string
	adopted     *bool
}

func findItem(items *interfaces.DraftItems, kind string, id string) (draftItem, bool) {
	switch kind {
	case interfaces.DRAFT_KIND_OBJECT_TYPES:
		for _, o := range items.ObjectTypes {
			if o.ID == id {
				return draftItem{kind, id, &o.Name, &o.Description, &o.Adopted}, true
			}
		}
	case interfaces.DRAFT_KIND_RELATION_TYPES:
		for _, r := range items.RelationTypes {
			if r.ID == id {
				return draftItem{kind, id, &r.Name, &r.Description, &r.Adopted}, true
			}
		}
	case interfaces.DRAFT_KIND_VALUE_TYPES:
		for _, v := range items.ValueTypes {
			if v.ID == id {
				return draftItem{kind, id, &v.Name, &v.Description, &v.Adopted}, true
			}
		}
	}
	return draftItem{}, false
}

func objectTypeAdopted(items *interfaces.DraftItems, id string) bool {
	for _, o := range items.ObjectTypes {
		if o.ID == id {
			return o.Adopted
		}
	}
	return false
}

// checkEdit refuses an edit that changes nothing or writes an invalid name or description,
// and trims what it writes.
func checkEdit(ctx context.Context, edit *interfaces.DraftItemEdit) error {
	if edit.Adopted == nil && edit.Name == nil && edit.Description == nil && edit.IncrementalKey == nil {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_EmptyEdit)
	}
	if edit.Name != nil {
		name := strings.TrimSpace(*edit.Name)
		if name == "" || utf8.RuneCountInString(name) > interfaces.DRAFT_NAME_MAX_LENGTH {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidName)
		}
		edit.Name = &name
	}
	if edit.Description != nil {
		description := strings.TrimSpace(*edit.Description)
		if utf8.RuneCountInString(description) > interfaces.DRAFT_DESCRIPTION_MAX_LENGTH {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidDescription)
		}
		edit.Description = &description
	}
	if edit.IncrementalKey != nil {
		key := strings.TrimSpace(*edit.IncrementalKey)
		edit.IncrementalKey = &key
	}
	return nil
}

// checkIncrementalField reads, as the caller, the field the object type's update field maps to,
// refusing one vega-backend would not refresh the table's local index by. The draft keeps BKN
// types only, which merge some vega types (large_integer drafts as integer).
func (s *draftService) checkIncrementalField(ctx context.Context, objectType *interfaces.DraftObjectType,
	caller interfaces.AccountInfo) error {
	property := findProperty(objectType, objectType.IncrementalKey)
	tables, err := s.va.GetTables(ctx, caller, []string{objectType.Table.ResourceID})
	if err != nil {
		return internalError(ctx, "Read table failed", err)
	}
	for _, table := range tables {
		if c, ok := column(table, property.MappedField); ok {
			if !incrementalTypes[c.Type] {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIncrementalKey)
			}
			return nil
		}
	}
	return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_FieldNotFound)
}

// incrementalTypes are the types vega-backend refreshes a table's local index by
// (INCREMENTAL_FIELD_TYPES); a property's BKN type has the same name.
var incrementalTypes = map[string]bool{
	"integer": true, "unsigned integer": true, "string": true, "date": true, "time": true, "datetime": true, "timestamp": true,
}

// applyEdit changes the item and returns the decisions it made. An object type is adopted only
// once it has an identity. Not adopting an object type stops adopting the relation types that
// connect it; a relation type is adopted only when both its object types are. Value types not adopted leave their properties to their base
// type when the draft is compiled.
func applyEdit(ctx context.Context, items *interfaces.DraftItems, kind string, id string,
	edit interfaces.DraftItemEdit) ([]*interfaces.DraftDecision, error) {
	item, ok := findItem(items, kind, id)
	if !ok {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	if edit.Adopted != nil && *edit.Adopted && !*item.adopted && kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
		// BKN object types need a primary key: one without an identity cannot be compiled.
		if findObjectType(items, id).NeedsIdentity {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_NeedsIdentity)
		}
	}
	if edit.Adopted != nil && *edit.Adopted && !*item.adopted && kind == interfaces.DRAFT_KIND_RELATION_TYPES {
		for _, r := range items.RelationTypes {
			if r.ID == id && (!objectTypeAdopted(items, r.Source) || !objectTypeAdopted(items, r.Target)) {
				return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_EndpointNotAdopted)
			}
		}
	}

	var objectType *interfaces.DraftObjectType
	if edit.IncrementalKey != nil {
		invalid := httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIncrementalKey)
		if kind != interfaces.DRAFT_KIND_OBJECT_TYPES {
			return nil, invalid
		}
		objectType = findObjectType(items, id)
		if key := *edit.IncrementalKey; key != "" {
			if property := findProperty(objectType, key); property == nil || !incrementalTypes[property.Type] {
				return nil, invalid
			}
		}
	}

	decisions := []*interfaces.DraftDecision{}
	decide := func(kind string, id string, action string, before any, after any) {
		decisions = append(decisions, &interfaces.DraftDecision{
			Kind: kind, ItemID: id, Action: action, Before: before, After: after,
		})
	}
	if edit.Adopted != nil && *edit.Adopted != *item.adopted {
		*item.adopted = *edit.Adopted
		action := interfaces.DRAFT_DECISION_ADOPT
		if !*edit.Adopted {
			action = interfaces.DRAFT_DECISION_UNADOPT
		}
		decide(kind, id, action, !*edit.Adopted, *edit.Adopted)
		if !*edit.Adopted && kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
			for _, r := range items.RelationTypes {
				if r.Adopted && (r.Source == id || r.Target == id) {
					r.Adopted = false
					decide(interfaces.DRAFT_KIND_RELATION_TYPES, r.ID, interfaces.DRAFT_DECISION_UNADOPT, true, false)
				}
			}
		}
	}
	if edit.Name != nil && *edit.Name != *item.name {
		decide(kind, id, interfaces.DRAFT_DECISION_RENAME, *item.name, *edit.Name)
		if kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
			// Relations named after the object type follow it, unless a person renamed them.
			renameRelations(items, findObjectType(items, id), *edit.Name, decide)
		}
		*item.name = *edit.Name
	}
	if edit.Description != nil && *edit.Description != *item.description {
		decide(kind, id, interfaces.DRAFT_DECISION_DESCRIBE, *item.description, *edit.Description)
		*item.description = *edit.Description
	}
	if edit.IncrementalKey != nil && *edit.IncrementalKey != objectType.IncrementalKey {
		decide(kind, id, interfaces.DRAFT_DECISION_INCREMENTAL, objectType.IncrementalKey, *edit.IncrementalKey)
		objectType.IncrementalKey = *edit.IncrementalKey
	}
	return decisions, nil
}

// regateRelations adopts the relations of an object type just adopted that drafting proposed
// only because it was not, as drafting would have with both ends adopted: on the counts it
// recorded, while they were counted against the key the target still has. One the counts now
// refute, or a person adopted or not, is left as it is.
func regateRelations(setting common.DraftSetting, items *interfaces.DraftItems, id string) []*interfaces.DraftDecision {
	// As undecidedItems counts them: only adopting or not is a decision on the relation itself.
	decided := map[string]bool{}
	for _, d := range items.Decisions {
		if d.Kind == interfaces.DRAFT_KIND_RELATION_TYPES &&
			(d.Action == interfaces.DRAFT_DECISION_ADOPT || d.Action == interfaces.DRAFT_DECISION_UNADOPT) {
			decided[d.ItemID] = true
		}
	}
	decisions := []*interfaces.DraftDecision{}
	for _, r := range items.RelationTypes {
		if r.ProposedBecause != proposedEndNotAdopted || decided[r.ID] || (r.Source != id && r.Target != id) ||
			!objectTypeAdopted(items, r.Source) || !objectTypeAdopted(items, r.Target) {
			continue
		}
		because := ""
		if r.LinkTable == nil {
			target := findObjectType(items, r.Target)
			if len(r.Mapping) != 1 || !slices.Equal(target.PrimaryKeys, []string{r.Mapping[0].TargetProperty}) {
				continue
			}
			var refuted bool
			if because, refuted = proposedBecause(setting, r.Origin, r.Evidence); refuted {
				// Refuted by the counts on the rates now set: it stays for a person to look at.
				continue
			}
		}
		r.ProposedBecause, r.Adopted = because, because == ""
		if r.Adopted {
			decisions = append(decisions, &interfaces.DraftDecision{
				Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: r.ID, Action: interfaces.DRAFT_DECISION_ADOPT,
				Before: false, After: true,
			})
		}
	}
	return decisions
}

// readyDraft returns the project's draft if it can be edited: the project is not archived,
// the draft is ready, and it is the draft the edit was made on.
func (s *draftService) readyDraft(ctx context.Context, token string, projectID string,
	updateTime int64) (*interfaces.Draft, error) {
	_, draft, err := s.readyDraftOf(ctx, token, projectID, updateTime)
	return draft, err
}

// readyDraftOf is readyDraft that also returns the project.
func (s *draftService) readyDraftOf(ctx context.Context, token string, projectID string,
	updateTime int64) (*interfaces.ProjectEntry, *interfaces.Draft, error) {
	if updateTime <= 0 {
		// Reloading would not help a change made on no draft, so it is not reported as stale.
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_MissingUpdateTime)
	}
	entry, err := s.unarchivedProject(ctx, token, projectID)
	if err != nil {
		return nil, nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		return nil, nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
	}
	if draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	if draft.UpdateTime != updateTime {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Stale)
	}
	return entry, draft, nil
}

// commit records the decisions on the draft and writes it, unless no one changed it since it
// was read. No decisions leave the draft as it is.
func (s *draftService) commit(ctx context.Context, draft *interfaces.Draft,
	decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo) error {
	if len(decisions) == 0 {
		return nil
	}
	readUpdateTime := draft.UpdateTime
	// The update time always moves on, so an edit made on the draft just read is refused.
	draft.UpdateTime = max(time.Now().UnixMilli(), readUpdateTime+1)
	for _, decision := range decisions {
		decision.By, decision.Time = caller, draft.UpdateTime
	}
	draft.Items.Decisions = append(draft.Items.Decisions, decisions...)
	written, err := s.da.UpdateDraft(ctx, draft, readUpdateTime)
	if err != nil {
		return internalError(ctx, "Update draft failed", err)
	}
	if !written {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Stale)
	}
	return nil
}

func (s *draftService) EditDraftItem(ctx context.Context, token string, projectID string, kind string,
	itemID string, edit interfaces.DraftItemEdit, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "EditDraftItem")
	defer span.End()

	if err := checkEdit(ctx, &edit); err != nil {
		span.SetStatus(codes.Error, "Invalid edit")
		return nil, err
	}
	draft, err := s.readyDraft(ctx, token, projectID, edit.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	decisions, err := applyEdit(ctx, &draft.Items, kind, itemID, edit)
	if err != nil {
		span.SetStatus(codes.Error, "Edit refused")
		return nil, err
	}
	if kind == interfaces.DRAFT_KIND_OBJECT_TYPES && edit.Adopted != nil && *edit.Adopted {
		decisions = append(decisions, regateRelations(s.setting, &draft.Items, itemID)...)
	}
	for _, d := range decisions {
		if d.Action == interfaces.DRAFT_DECISION_INCREMENTAL && d.After != "" {
			if err := s.checkIncrementalField(ctx, findObjectType(&draft.Items, itemID), caller); err != nil {
				span.SetStatus(codes.Error, "Update field refused")
				return nil, err
			}
		}
	}
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}
