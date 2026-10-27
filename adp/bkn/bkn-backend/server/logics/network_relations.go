// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// NetworkRelations checks the BKN spec 3.0 declarations that span definitions (SPECIFICATION.md
// §4.3, §7.6): lifecycle replaced_by references, relation types whose endpoint is deprecated, and
// fallback paths. A save checks them on the network it would produce, whichever side it writes; a
// delete is refused while another definition still refers to what it removes.
//
// A nil *NetworkRelations checks nothing.
type NetworkRelations struct {
	OTA interfaces.ObjectTypeAccess
	RTA interfaces.RelationTypeAccess
	ATA interfaces.ActionTypeAccess
}

// NewNetworkRelations reads the stored definitions through the registered accessors.
func NewNetworkRelations() *NetworkRelations {
	return &NetworkRelations{OTA: OTA, RTA: RTA, ATA: ATA}
}

// SavedDefinitions are the definitions one save writes.
type SavedDefinitions struct {
	ObjectTypes   []*interfaces.ObjectType
	RelationTypes []*interfaces.RelationType
	ActionTypes   []*interfaces.ActionType
}

func (s *SavedDefinitions) empty() bool {
	return len(s.ObjectTypes) == 0 && len(s.RelationTypes) == 0 && len(s.ActionTypes) == 0
}

// add records more, one entry per ID: a write may pass the same definition more than once, as a
// concept group update validates its nested definitions before writing them, and the later entry
// replaces the earlier one.
func (s *SavedDefinitions) add(more SavedDefinitions) {
	s.ObjectTypes = upsertByID(s.ObjectTypes, more.ObjectTypes, func(ot *interfaces.ObjectType) string { return ot.OTID })
	s.RelationTypes = upsertByID(s.RelationTypes, more.RelationTypes, func(rt *interfaces.RelationType) string { return rt.RTID })
	s.ActionTypes = upsertByID(s.ActionTypes, more.ActionTypes, func(at *interfaces.ActionType) string { return at.ATID })
}

func upsertByID[T any](have, more []T, id func(T) string) []T {
	for _, def := range more {
		if i := slices.IndexFunc(have, func(h T) bool { return id(h) == id(def) }); i >= 0 {
			have[i] = def
			continue
		}
		have = append(have, def)
	}
	return have
}

// involvedIn reports whether the issue concerns a definition of the save.
func (s *SavedDefinitions) involvedIn(issue bknsdk.DeclarationIssue) bool {
	for _, ot := range s.ObjectTypes {
		if issue.Involves(interfaces.RESOURCE_TYPE_OBJECT_TYPE, ot.OTID) {
			return true
		}
	}
	for _, rt := range s.RelationTypes {
		if issue.Involves(interfaces.RESOURCE_TYPE_RELATION_TYPE, rt.RTID) {
			return true
		}
	}
	for _, at := range s.ActionTypes {
		if issue.Involves(interfaces.RESOURCE_TYPE_ACTION_TYPE, at.ATID) {
			return true
		}
	}
	return false
}

type deferredSaveKey struct{}

// WithDeferredNetworkRelations makes the saves under ctx record the definitions they write instead
// of checking them. An import writes object types, relation types and action types one kind after
// another; checked one kind at a time, the network would mix the new definitions of one kind with
// the stored ones of the next. The import checks what was recorded once every kind is in.
func WithDeferredNetworkRelations(ctx context.Context) (context.Context, *SavedDefinitions) {
	saved := &SavedDefinitions{}
	return context.WithValue(ctx, deferredSaveKey{}, saved), saved
}

// DeferNetworkRelations is WithDeferredNetworkRelations for a write that may run inside another
// deferred one, such as the nested definitions of a concept group in an import. Inside one it
// records into the enclosing collector and owner is false: only the outermost write checks.
func DeferNetworkRelations(ctx context.Context) (deferredCtx context.Context, saved *SavedDefinitions, owner bool) {
	if enclosing, ok := ctx.Value(deferredSaveKey{}).(*SavedDefinitions); ok {
		return ctx, enclosing, false
	}
	deferredCtx, saved = WithDeferredNetworkRelations(ctx)
	return deferredCtx, saved, true
}

// CheckSave lists the issues the save leaves in the network it would produce that involve a
// definition it writes; one between two stored definitions is not this save's doing. Under
// WithDeferredNetworkRelations it records the definitions and lists nothing.
func (nr *NetworkRelations) CheckSave(ctx context.Context, knID, branch string, saved SavedDefinitions) ([]string, error) {
	if nr == nil || saved.empty() {
		return nil, nil
	}
	if deferred, ok := ctx.Value(deferredSaveKey{}).(*SavedDefinitions); ok {
		deferred.add(saved)
		return nil, nil
	}
	return nr.Check(ctx, knID, branch, saved)
}

// Check lists the issues involving a definition of saved in the network saved would produce over
// the stored definitions. It reads the stored definitions an issue could involve: the deprecated
// ones, the fallback paths, the ones saved names, and the relation types bound to an object type
// saved deprecates. A network without lifecycles or fallback paths answers the list reads with no
// rows.
func (nr *NetworkRelations) Check(ctx context.Context, knID, branch string, saved SavedDefinitions) ([]string, error) {
	if nr == nil || saved.empty() {
		return nil, nil
	}
	doc, err := nr.network(ctx, knID, branch, saved)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, issue := range bknsdk.NetworkRelationIssues(doc) {
		if saved.involvedIn(issue) {
			problems = append(problems, issue.Err.Error())
		}
	}
	return problems, nil
}

// SaveRefusal runs CheckSave and answers its issues as a 400 with invalidCode, joined in the
// details; a failed read answers 500 with internalCode.
func (nr *NetworkRelations) SaveRefusal(ctx context.Context, knID, branch string, saved SavedDefinitions,
	invalidCode, internalCode string) error {
	problems, err := nr.CheckSave(ctx, knID, branch, saved)
	return nr.saveRefusal(ctx, problems, err, invalidCode, internalCode)
}

// CheckRefusal is SaveRefusal for Check: it checks now, also under WithDeferredNetworkRelations.
func (nr *NetworkRelations) CheckRefusal(ctx context.Context, knID, branch string, saved SavedDefinitions,
	invalidCode, internalCode string) error {
	problems, err := nr.Check(ctx, knID, branch, saved)
	return nr.saveRefusal(ctx, problems, err, invalidCode, internalCode)
}

func (nr *NetworkRelations) saveRefusal(ctx context.Context, problems []string, err error, invalidCode, internalCode string) error {
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, internalCode).WithErrorDetails(err.Error())
	}
	if len(problems) > 0 {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, invalidCode).WithErrorDetails(strings.Join(problems, "; "))
	}
	return nil
}

// DeleteRefusal runs DeleteReferrers and answers the references as a 409 with referencedCode, listed
// in the details; a failed read answers 500 with internalCode.
func (nr *NetworkRelations) DeleteRefusal(ctx context.Context, knID, branch, kind string, ids []string,
	referencedCode, internalCode string) error {
	references, err := nr.DeleteReferrers(ctx, knID, branch, kind, ids)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, internalCode).WithErrorDetails(err.Error())
	}
	if len(references) > 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, referencedCode).
			WithErrorDetails(map[string]any{"references": references})
	}
	return nil
}

func (nr *NetworkRelations) network(ctx context.Context, knID, branch string, saved SavedDefinitions) (*bknsdk.BknNetwork, error) {
	objects := map[string]*bknsdk.BknObjectType{}
	relations := map[string]*bknsdk.BknRelationType{}
	actions := map[string]*bknsdk.BknActionType{}

	deprecatedObjects, err := nr.OTA.ListDeprecatedObjectTypes(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	for _, ot := range deprecatedObjects {
		objects[ot.OTID] = lifecycleObjectType(ot.OTID, ot.OTName, ot.Lifecycle)
	}
	deprecatedActions, err := nr.ATA.ListDeprecatedActionTypes(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	for _, at := range deprecatedActions {
		actions[at.ATID] = lifecycleActionType(at.ATID, at.ATName, at.Lifecycle)
	}

	// The definitions saved names: a replaced_by must exist, and a fallback path's primary relation
	// is read whole.
	savedIDs := func(kind string) []string {
		var ids []string
		switch kind {
		case interfaces.RESOURCE_TYPE_OBJECT_TYPE:
			for _, ot := range saved.ObjectTypes {
				ids = append(ids, ot.OTID)
			}
		case interfaces.RESOURCE_TYPE_RELATION_TYPE:
			for _, rt := range saved.RelationTypes {
				ids = append(ids, rt.RTID)
			}
		default:
			for _, at := range saved.ActionTypes {
				ids = append(ids, at.ATID)
			}
		}
		return ids
	}
	missing := func(id string, known func(string) bool, kind string, ids []string) []string {
		if id == "" || known(id) || slices.Contains(savedIDs(kind), id) || slices.Contains(ids, id) {
			return ids
		}
		return append(ids, id)
	}

	var objectIDs []string
	var deprecatedSaved []string
	for _, ot := range saved.ObjectTypes {
		if ot.Lifecycle != nil {
			objectIDs = missing(ot.Lifecycle.ReplacedBy, func(id string) bool { return objects[id] != nil },
				interfaces.RESOURCE_TYPE_OBJECT_TYPE, objectIDs)
			if ot.Lifecycle.Status == interfaces.LIFECYCLE_DEPRECATED {
				deprecatedSaved = append(deprecatedSaved, ot.OTID)
			}
		}
	}
	if len(objectIDs) > 0 {
		found, err := nr.OTA.GetObjectTypeIdentitiesByIDsOrNames(ctx, knID, branch, objectIDs, nil)
		if err != nil {
			return nil, err
		}
		for _, ot := range found {
			objects[ot.OTID] = lifecycleObjectType(ot.OTID, ot.OTName, nil)
		}
	}
	var actionIDs []string
	for _, at := range saved.ActionTypes {
		if at.Lifecycle != nil {
			actionIDs = missing(at.Lifecycle.ReplacedBy, func(id string) bool { return actions[id] != nil },
				interfaces.RESOURCE_TYPE_ACTION_TYPE, actionIDs)
		}
	}
	if len(actionIDs) > 0 {
		found, err := nr.ATA.GetActionTypeIdentitiesByIDsOrNames(ctx, knID, branch, actionIDs, nil)
		if err != nil {
			return nil, err
		}
		for _, at := range found {
			actions[at.ATID] = lifecycleActionType(at.ATID, at.ATName, nil)
		}
	}

	relationIDs, err := nr.RTA.ListDeprecatedOrFallbackRelationTypeIDs(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	relationIDs = slices.DeleteFunc(relationIDs, func(id string) bool {
		return slices.Contains(savedIDs(interfaces.RESOURCE_TYPE_RELATION_TYPE), id)
	})
	for _, rt := range saved.RelationTypes {
		known := func(string) bool { return false }
		relationIDs = missing(rt.FallbackFor, known, interfaces.RESOURCE_TYPE_RELATION_TYPE, relationIDs)
		if rt.Lifecycle != nil {
			relationIDs = missing(rt.Lifecycle.ReplacedBy, known, interfaces.RESOURCE_TYPE_RELATION_TYPE, relationIDs)
		}
	}
	if len(relationIDs) > 0 {
		found, err := nr.RTA.GetRelationTypesByIDs(ctx, knID, branch, relationIDs)
		if err != nil {
			return nil, err
		}
		for _, rt := range found {
			relations[rt.RTID] = ToBKNRelationType(rt)
		}
	}
	// An object type the save deprecates deprecates the relation types bound to it.
	if len(deprecatedSaved) > 0 {
		bound, err := nr.RTA.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
			KNID:                      knID,
			Branch:                    branch,
			BoundObjectTypeIDs:        deprecatedSaved,
		})
		if err != nil {
			return nil, err
		}
		for _, rt := range bound {
			if relations[rt.RTID] == nil {
				relations[rt.RTID] = ToBKNRelationType(rt)
			}
		}
	}

	// The save replaces the stored definitions of its IDs, after the stored ones in ID order.
	doc := &bknsdk.BknNetwork{}
	for _, id := range sortedKeys(objects) {
		if !slices.Contains(savedIDs(interfaces.RESOURCE_TYPE_OBJECT_TYPE), id) {
			doc.ObjectTypes = append(doc.ObjectTypes, objects[id])
		}
	}
	for _, ot := range saved.ObjectTypes {
		doc.ObjectTypes = append(doc.ObjectTypes, lifecycleObjectType(ot.OTID, ot.OTName, ot.Lifecycle))
	}
	for _, id := range sortedKeys(relations) {
		if !slices.Contains(savedIDs(interfaces.RESOURCE_TYPE_RELATION_TYPE), id) {
			doc.RelationTypes = append(doc.RelationTypes, relations[id])
		}
	}
	for _, rt := range saved.RelationTypes {
		doc.RelationTypes = append(doc.RelationTypes, ToBKNRelationType(rt))
	}
	for _, id := range sortedKeys(actions) {
		if !slices.Contains(savedIDs(interfaces.RESOURCE_TYPE_ACTION_TYPE), id) {
			doc.ActionTypes = append(doc.ActionTypes, actions[id])
		}
	}
	for _, at := range saved.ActionTypes {
		doc.ActionTypes = append(doc.ActionTypes, lifecycleActionType(at.ATID, at.ATName, at.Lifecycle))
	}
	return doc, nil
}

// DeleteReferrers lists the stored definitions that still refer to the definitions of kind a delete
// removes: a deprecated definition of the same kind whose replaced_by names one, or a relation type
// whose fallback_for names one. A referrer the same delete removes does not count.
func (nr *NetworkRelations) DeleteReferrers(ctx context.Context, knID, branch, kind string, ids []string) ([]string, error) {
	if nr == nil || len(ids) == 0 {
		return nil, nil
	}
	label := map[string]string{
		interfaces.RESOURCE_TYPE_OBJECT_TYPE:   "object type",
		interfaces.RESOURCE_TYPE_RELATION_TYPE: "relation type",
		interfaces.RESOURCE_TYPE_ACTION_TYPE:   "action type",
	}[kind]
	var referrers []string
	replacedBy := func(id string, lc *interfaces.Lifecycle) {
		if lc != nil && lc.ReplacedBy != "" && slices.Contains(ids, lc.ReplacedBy) && !slices.Contains(ids, id) {
			referrers = append(referrers, fmt.Sprintf("%s %q is the lifecycle replaced_by of %s %q", label, lc.ReplacedBy, label, id))
		}
	}
	switch kind {
	case interfaces.RESOURCE_TYPE_OBJECT_TYPE:
		stored, err := nr.OTA.ListDeprecatedObjectTypes(ctx, knID, branch)
		if err != nil {
			return nil, err
		}
		sort.Slice(stored, func(i, j int) bool { return stored[i].OTID < stored[j].OTID })
		for _, ot := range stored {
			replacedBy(ot.OTID, ot.Lifecycle)
		}
	case interfaces.RESOURCE_TYPE_ACTION_TYPE:
		stored, err := nr.ATA.ListDeprecatedActionTypes(ctx, knID, branch)
		if err != nil {
			return nil, err
		}
		sort.Slice(stored, func(i, j int) bool { return stored[i].ATID < stored[j].ATID })
		for _, at := range stored {
			replacedBy(at.ATID, at.Lifecycle)
		}
	case interfaces.RESOURCE_TYPE_RELATION_TYPE:
		relationIDs, err := nr.RTA.ListDeprecatedOrFallbackRelationTypeIDs(ctx, knID, branch)
		if err != nil || len(relationIDs) == 0 {
			return nil, err
		}
		stored, err := nr.RTA.GetRelationTypesByIDs(ctx, knID, branch, relationIDs)
		if err != nil {
			return nil, err
		}
		sort.Slice(stored, func(i, j int) bool { return stored[i].RTID < stored[j].RTID })
		for _, rt := range stored {
			replacedBy(rt.RTID, rt.Lifecycle)
			if rt.FallbackFor != "" && slices.Contains(ids, rt.FallbackFor) && !slices.Contains(ids, rt.RTID) {
				referrers = append(referrers, fmt.Sprintf("relation type %q is the fallback_for of relation type %q", rt.FallbackFor, rt.RTID))
			}
		}
	}
	return referrers, nil
}

func lifecycleObjectType(id, name string, lc *interfaces.Lifecycle) *bknsdk.BknObjectType {
	return &bknsdk.BknObjectType{BknObjectTypeFrontmatter: bknsdk.BknObjectTypeFrontmatter{ID: id, Name: name, Lifecycle: toBKNLifecycle(lc)}}
}

func lifecycleActionType(id, name string, lc *interfaces.Lifecycle) *bknsdk.BknActionType {
	return &bknsdk.BknActionType{BknActionTypeFrontmatter: bknsdk.BknActionTypeFrontmatter{ID: id, Name: name, Lifecycle: toBKNLifecycle(lc)}}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
