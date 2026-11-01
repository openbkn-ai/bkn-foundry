// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// reasonNotIdentity is a relation not counted: its target property does not identify the target.
const reasonNotIdentity = "not_identity"

// Why a relation a person defines is refused, in the error's details.
const (
	relationUnknownEnd      = "unknown_end"
	relationUnknownProperty = "unknown_property"
	relationUnknownField    = "unknown_field"
	relationTypeMismatch    = "type_mismatch"
	relationNoMapping       = "no_mapping"
	relationInvalidType     = "invalid_type"
	relationInvalidID       = "invalid_id"
	relationDuplicate       = "duplicate"
	relationNotManual       = "not_manual"
)

func invalidRelation(ctx context.Context, details map[string]any) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRelation).
		WithErrorDetails(details)
}

// joinable tells whether values of two BKN types join: the same type, integers with integers, or
// strings with text.
func joinable(a string, b string) bool {
	family := func(t string) string {
		switch t {
		case "integer", "unsigned integer":
			return "integer"
		case "string", "text":
			return "string"
		}
		return t
	}
	return a != "" && family(a) == family(b)
}

func relationSnapshot(rt *interfaces.DraftRelationType) interfaces.DraftRelationTypeSnapshot {
	s := interfaces.DraftRelationTypeSnapshot{Name: rt.Name, Source: rt.Source, Target: rt.Target, Mapping: rt.Mapping,
		SourceMapping: rt.SourceMapping, TargetMapping: rt.TargetMapping}
	if rt.Document != nil {
		s.DocumentMapping = rt.DocumentMapping
		s.DocumentCardinality = rt.Cardinality
	}
	if rt.LinkTable != nil {
		s.LinkTable = rt.LinkTable.ResourceID
	}
	return s
}

func sameJoin(a interfaces.DraftRelationTypeSnapshot, b interfaces.DraftRelationTypeSnapshot) bool {
	return a.Source == b.Source && a.Target == b.Target && a.LinkTable == b.LinkTable &&
		slices.Equal(a.Mapping, b.Mapping) && slices.Equal(a.SourceMapping, b.SourceMapping) &&
		slices.Equal(a.TargetMapping, b.TargetMapping) && a.DocumentCardinality == b.DocumentCardinality && reflect.DeepEqual(a.DocumentMapping, b.DocumentMapping)
}

// defineRelation sets on rt the ends and mappings of save, refusing ends that are not object types of
// the draft, properties they do not have, fields the backing table does not have, types that do not
// join, no mapping, and a join another relation has already.
func (s *draftService) defineRelation(ctx context.Context, items *interfaces.DraftItems, rt *interfaces.DraftRelationType,
	save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) error {
	source := findObjectType(items, save.SourceObjectTypeID)
	target := findObjectType(items, save.TargetObjectTypeID)
	switch {
	case source == nil:
		return invalidRelation(ctx, map[string]any{"reason": relationUnknownEnd, "object_type": save.SourceObjectTypeID})
	case target == nil:
		return invalidRelation(ctx, map[string]any{"reason": relationUnknownEnd, "object_type": save.TargetObjectTypeID})
	}
	property := func(ot *interfaces.DraftObjectType, name string) (*interfaces.DraftProperty, error) {
		if p := findProperty(ot, name); p != nil {
			return p, nil
		}
		return nil, invalidRelation(ctx, map[string]any{"reason": relationUnknownProperty, "object_type": ot.ID, "property": name})
	}
	mismatch := func(a string, b string) error {
		return invalidRelation(ctx, map[string]any{"reason": relationTypeMismatch, "source": a, "target": b})
	}
	rt.Source, rt.Target = source.ID, target.ID
	rt.Mapping, rt.LinkTable, rt.SourceMapping, rt.TargetMapping = nil, nil, nil, nil

	switch save.Type {
	case interfaces.DRAFT_RELATION_DIRECT:
		var rules []interfaces.DraftRelationMapping
		if err := json.Unmarshal(save.MappingRules, &rules); err != nil || len(rules) == 0 {
			return invalidRelation(ctx, map[string]any{"reason": relationNoMapping})
		}
		for _, r := range rules {
			sp, err := property(source, r.SourceProperty.Name)
			if err != nil {
				return err
			}
			tp, err := property(target, r.TargetProperty.Name)
			if err != nil {
				return err
			}
			if !joinable(sp.Type, tp.Type) {
				return mismatch(sp.Name, tp.Name)
			}
			rt.Mapping = append(rt.Mapping, interfaces.DraftFieldMapping{SourceProperty: sp.Name, TargetProperty: tp.Name})
		}
		rt.Cardinality = interfaces.DRAFT_CARDINALITY_MANY_TO_ONE
		if len(rt.Mapping) == 1 && slices.Equal(source.PrimaryKeys, []string{rt.Mapping[0].SourceProperty}) {
			rt.Cardinality = interfaces.DRAFT_CARDINALITY_ONE_TO_ONE
		}
	case interfaces.DRAFT_RELATION_INDIRECT:
		var rules interfaces.DraftIndirectMapping
		if err := json.Unmarshal(save.MappingRules, &rules); err != nil || rules.BackingDataSource == nil ||
			rules.BackingDataSource.ID == "" || len(rules.SourceMappingRules) == 0 || len(rules.TargetMappingRules) == 0 {
			return invalidRelation(ctx, map[string]any{"reason": relationNoMapping})
		}
		tables, err := s.va.GetTables(ctx, caller, []string{rules.BackingDataSource.ID})
		if err != nil {
			return internalError(ctx, "Read table failed", err)
		}
		if len(tables) == 0 {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_FieldNotFound).
				WithErrorDetails(map[string]any{"resource_id": rules.BackingDataSource.ID})
		}
		link := tables[0]
		field := func(name string) (string, error) {
			if c, ok := column(link, name); ok && bknTypes[c.Type] != "" {
				return bknTypes[c.Type], nil
			}
			return "", invalidRelation(ctx, map[string]any{"reason": relationUnknownField, "field": name})
		}
		for _, r := range rules.SourceMappingRules {
			sp, err := property(source, r.SourceProperty.Name)
			if err != nil {
				return err
			}
			typ, err := field(r.TargetProperty.Name)
			if err != nil {
				return err
			}
			if !joinable(sp.Type, typ) {
				return mismatch(sp.Name, r.TargetProperty.Name)
			}
			rt.SourceMapping = append(rt.SourceMapping, interfaces.DraftFieldMapping{SourceProperty: sp.Name, TargetProperty: r.TargetProperty.Name})
		}
		for _, r := range rules.TargetMappingRules {
			typ, err := field(r.SourceProperty.Name)
			if err != nil {
				return err
			}
			tp, err := property(target, r.TargetProperty.Name)
			if err != nil {
				return err
			}
			if !joinable(typ, tp.Type) {
				return mismatch(r.SourceProperty.Name, tp.Name)
			}
			rt.TargetMapping = append(rt.TargetMapping, interfaces.DraftFieldMapping{SourceProperty: r.SourceProperty.Name, TargetProperty: tp.Name})
		}
		table := draftTable(link)
		rt.LinkTable = &table
		rt.Cardinality = interfaces.DRAFT_CARDINALITY_MANY_TO_MANY
	default:
		return invalidRelation(ctx, map[string]any{"reason": relationInvalidType, "type": save.Type})
	}

	joined := relationSnapshot(rt)
	for _, other := range items.RelationTypes {
		if other.ID != rt.ID && sameJoin(relationSnapshot(other), joined) {
			return invalidRelation(ctx, map[string]any{"reason": relationDuplicate,
				"relation": interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: other.ID, Name: other.Name}})
		}
	}
	return nil
}

// countJoin counts a relation of the draft over the full table as drafting does, when a single field
// maps to the target's single-field identity within one catalog; otherwise it says why it is not
// counted.
func (s *draftService) countJoin(ctx context.Context, items *interfaces.DraftItems, rt *interfaces.DraftRelationType,
	caller interfaces.AccountInfo) interfaces.DraftJoinEvidence {
	source, target := findObjectType(items, rt.Source), findObjectType(items, rt.Target)
	switch {
	case rt.LinkTable != nil:
		return interfaces.DraftJoinEvidence{Reason: reasonLinkTable}
	case len(rt.Mapping) != 1:
		return interfaces.DraftJoinEvidence{Reason: reasonCompositeKey}
	case !slices.Equal(target.PrimaryKeys, []string{rt.Mapping[0].TargetProperty}):
		return interfaces.DraftJoinEvidence{Reason: reasonNotIdentity}
	case source.Table.CatalogID != target.Table.CatalogID:
		return interfaces.DraftJoinEvidence{Reason: reasonCrossCatalog}
	}
	countCtx, cancel := context.WithTimeout(ctx, identityCountTimeout)
	defer cancel()
	c := &vegaCounter{va: s.va, account: caller, timeoutSeconds: min(s.setting.QueryTimeout, int(identityCountTimeout.Seconds()))}
	field := findProperty(source, rt.Mapping[0].SourceProperty).MappedField
	key := findProperty(target, rt.Mapping[0].TargetProperty).MappedField
	rows, nulls, orphans, distinct, err := c.joinStats(countCtx, &interfaces.VegaTable{ID: source.Table.ResourceID}, field,
		&interfaces.VegaTable{ID: target.Table.ResourceID}, key)
	if err != nil {
		return interfaces.DraftJoinEvidence{Reason: fmt.Sprintf("%s: %v", reasonQueryFailed, err)}
	}
	evidence := joinEvidence(rows, nulls, orphans)
	evidence.SourceDistinct = distinct
	return evidence
}

// checkRelationNames trims and refuses an empty or too long name, and a too long description.
func checkRelationNames(ctx context.Context, save *interfaces.DraftRelationTypeSave) error {
	save.Name, save.Comment = strings.TrimSpace(save.Name), strings.TrimSpace(save.Comment)
	if save.Name == "" || utf8.RuneCountInString(save.Name) > interfaces.DRAFT_NAME_MAX_LENGTH {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidName)
	}
	if utf8.RuneCountInString(save.Comment) > interfaces.DRAFT_DESCRIPTION_MAX_LENGTH {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidDescription)
	}
	return nil
}

func (s *draftService) CreateRelationType(ctx context.Context, token string, projectID string,
	save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateRelationType")
	defer span.End()

	if err := checkRelationNames(ctx, &save); err != nil {
		span.SetStatus(codes.Error, "Invalid names")
		return nil, err
	}
	draft, err := s.readyDraft(ctx, token, projectID, save.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	taken := map[string]bool{}
	for _, r := range draft.Items.RelationTypes {
		taken[r.ID] = true
	}
	id := save.ID
	switch {
	case id == "":
		id = uniqueName(idFrom(save.SourceObjectTypeID+"_to_"+save.TargetObjectTypeID, idPrefixRelationType), taken)
	case apiName(id) != id || taken[id]:
		span.SetStatus(codes.Error, "Invalid id")
		return nil, invalidRelation(ctx, map[string]any{"reason": relationInvalidID, "id": id})
	}
	rt := &interfaces.DraftRelationType{ID: id, Name: save.Name, Description: save.Comment,
		Origin: interfaces.DRAFT_ORIGIN_MANUAL, Verification: "confirmed"}
	if save.DocumentMapping != nil {
		return s.createDocumentRelation(ctx, token, draft, rt, save, caller)
	}
	for _, id := range []string{save.SourceObjectTypeID, save.TargetObjectTypeID} {
		if object := findObjectType(&draft.Items, id); object != nil && object.Document != nil {
			return nil, invalidRelation(ctx, map[string]any{"reason": relationNoMapping})
		}
	}

	if err := s.defineRelation(ctx, &draft.Items, rt, save, caller); err != nil {
		span.SetStatus(codes.Error, "Invalid relation")
		return nil, err
	}
	rt.Evidence = s.countJoin(ctx, &draft.Items, rt, caller)
	// A person's relation is in, as drafting's are, once both its ends are.
	rt.Adopted = objectTypeAdopted(&draft.Items, rt.Source) && objectTypeAdopted(&draft.Items, rt.Target)
	if !rt.Adopted {
		rt.ProposedBecause = proposedEndNotAdopted
	}
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, rt)
	decisions := []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: id,
		Action: interfaces.DRAFT_DECISION_CREATE, After: relationSnapshot(rt)}}
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

func (s *draftService) SaveRelationType(ctx context.Context, token string, projectID string, itemID string,
	save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SaveRelationType")
	defer span.End()

	if err := checkRelationNames(ctx, &save); err != nil {
		span.SetStatus(codes.Error, "Invalid names")
		return nil, err
	}
	draft, err := s.readyDraft(ctx, token, projectID, save.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	rt := findRelationType(&draft.Items, itemID)
	if rt == nil {
		span.SetStatus(codes.Error, "Relation type not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	if rt.Document != nil {
		return s.saveDocumentRelation(ctx, token, draft, rt, save, caller)
	}
	if save.DocumentMapping != nil {
		return nil, invalidRelation(ctx, map[string]any{"reason": relationNoMapping})
	}
	for _, id := range []string{save.SourceObjectTypeID, save.TargetObjectTypeID} {
		if object := findObjectType(&draft.Items, id); object != nil && object.Document != nil {
			return nil, invalidRelation(ctx, map[string]any{"reason": relationNoMapping})
		}
	}
	before := relationSnapshot(rt)
	defined := *rt
	if err := s.defineRelation(ctx, &draft.Items, &defined, save, caller); err != nil {
		span.SetStatus(codes.Error, "Invalid relation")
		return nil, err
	}
	decisions := []*interfaces.DraftDecision{}
	if after := relationSnapshot(&defined); !sameJoin(before, after) {
		*rt = defined
		rt.Verification = "confirmed"
		rt.Evidence = s.countJoin(ctx, &draft.Items, rt, caller)
		// A relation stays in only while both its ends are.
		if rt.Adopted && !(objectTypeAdopted(&draft.Items, rt.Source) && objectTypeAdopted(&draft.Items, rt.Target)) {
			rt.Adopted, rt.ProposedBecause = false, proposedEndNotAdopted
		}
		decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: itemID,
			Action: interfaces.DRAFT_DECISION_SAVE, Before: before, After: relationSnapshot(rt)})
	}
	name, comment := save.Name, save.Comment
	edited, err := applyEdit(ctx, &draft.Items, interfaces.DRAFT_KIND_RELATION_TYPES, itemID,
		interfaces.DraftItemEdit{Name: &name, Description: &comment})
	if err != nil {
		span.SetStatus(codes.Error, "Edit refused")
		return nil, err
	}
	if err := s.commit(ctx, draft, append(edited, decisions...), caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

func (s *draftService) DeleteRelationType(ctx context.Context, token string, projectID string, itemID string,
	updateTime int64, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteRelationType")
	defer span.End()

	draft, err := s.readyDraft(ctx, token, projectID, updateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	at := slices.IndexFunc(draft.Items.RelationTypes, func(r *interfaces.DraftRelationType) bool { return r.ID == itemID })
	if at < 0 {
		span.SetStatus(codes.Error, "Relation type not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	rt := draft.Items.RelationTypes[at]
	if rt.Origin != interfaces.DRAFT_ORIGIN_MANUAL {
		// Drafting would draft it again: it is left out instead.
		span.SetStatus(codes.Error, "Not made by a person")
		return nil, invalidRelation(ctx, map[string]any{"reason": relationNotManual, "relation": itemID})
	}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.Document != nil && object.Document.Role == "event" && slices.Contains(object.Document.ParticipantRelations, itemID) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_EventParticipantInUse)
		}
	}
	draft.Items.RelationTypes = slices.Delete(slices.Clone(draft.Items.RelationTypes), at, at+1)
	decisions := []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: itemID,
		Action: interfaces.DRAFT_DECISION_DELETE, Before: relationSnapshot(rt)}}
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}
