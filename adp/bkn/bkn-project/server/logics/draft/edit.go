// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

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
	if edit.Adopted == nil && edit.Name == nil && edit.Description == nil {
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
	return nil
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
		*item.name = *edit.Name
	}
	if edit.Description != nil && *edit.Description != *item.description {
		decide(kind, id, interfaces.DRAFT_DECISION_DESCRIBE, *item.description, *edit.Description)
		*item.description = *edit.Description
	}
	return decisions, nil
}

// readyDraft returns the project's draft if it can be edited: the project is not archived,
// the draft is ready, and it is the draft the edit was made on.
func (s *draftService) readyDraft(ctx context.Context, token string, projectID string,
	updateTime int64) (*interfaces.Draft, error) {
	if updateTime <= 0 {
		// Reloading would not help a change made on no draft, so it is not reported as stale.
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_MissingUpdateTime)
	}
	if _, err := s.unarchivedProject(ctx, token, projectID); err != nil {
		return nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
	}
	if draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	if draft.UpdateTime != updateTime {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Stale)
	}
	return draft, nil
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
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}
