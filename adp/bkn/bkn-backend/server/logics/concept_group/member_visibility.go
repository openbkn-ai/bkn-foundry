// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package concept_group

import (
	"context"
	"net/http"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

// memberScope is what the caller can see of the object types, relation types and action types a
// concept group names or counts. It is loaded once per request, however many groups it serves.
//
// A concept group lists its members and counts the relation and action types among them. Both
// used to include object types the caller holds nothing on, and relation or action types the
// caller may not read (#1532). Members now follow the reference rule every child resource uses;
// counts follow what the relation and action type lists would show the caller.
type memberScope struct {
	visibleObjectTypes map[string]struct{}
	relationTypes      []*interfaces.RelationType
	actionTypes        []*interfaces.ActionType
}

// loadMemberScope resolves, for the given members, which object types the caller holds at least
// one effective operation on, and which relation and action types among those the caller may read.
func (cgs *conceptGroupService) loadMemberScope(ctx context.Context, knID, branch string,
	memberIDs []string) (*memberScope, error) {

	scope := &memberScope{visibleObjectTypes: map[string]struct{}{}}
	if len(memberIDs) == 0 {
		return scope, nil
	}
	visible, err := permission.VisibleReferencedObjectTypes(ctx, cgs.ps, knID, memberIDs)
	if err != nil {
		return nil, err
	}
	scope.visibleObjectTypes = visible
	if len(visible) == 0 {
		return scope, nil
	}
	visibleIDs := make([]string, 0, len(visible))
	for memberID := range visible {
		visibleIDs = append(visibleIDs, memberID)
	}
	sort.Strings(visibleIDs)

	// Both ends among the visible members: a relation type with an end outside them is either
	// outside every group or hidden by the endpoint rule.
	relationTypes, err := cgs.rta.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      knID,
		Branch:                    branch,
		SourceObjectTypeIDs:       visibleIDs,
		TargetObjectTypeIDs:       visibleIDs,
	})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetRelationTypesTotalFailed).WithErrorDetails(err.Error())
	}
	relationTypeIDs := make([]string, 0, len(relationTypes))
	for _, relationType := range relationTypes {
		relationTypeIDs = append(relationTypeIDs, relationType.RTID)
	}
	readableRelationTypes, err := permission.FilterKNChildIDs(ctx, cgs.ps, interfaces.RESOURCE_TYPE_RELATION_TYPE,
		knID, relationTypeIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	scope.relationTypes = keepByID(relationTypes, readableRelationTypes,
		func(relationType *interfaces.RelationType) string { return relationType.RTID })

	actionTypes, err := cgs.ata.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      knID,
		Branch:                    branch,
		ObjectTypeIDs:             visibleIDs,
	})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetRelationTypesTotalFailed).WithErrorDetails(err.Error())
	}
	actionTypeIDs := make([]string, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		actionTypeIDs = append(actionTypeIDs, actionType.ATID)
	}
	readableActionTypes, err := permission.FilterKNChildIDs(ctx, cgs.ps, interfaces.RESOURCE_TYPE_ACTION_TYPE,
		knID, actionTypeIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	scope.actionTypes = keepByID(actionTypes, readableActionTypes,
		func(actionType *interfaces.ActionType) string { return actionType.ATID })
	return scope, nil
}

// apply returns a group's members the caller may see, in their stored order, and the statistics
// counted over them.
func (s *memberScope) apply(memberIDs []string) ([]string, *interfaces.Statistics) {
	visibleMembers := make([]string, 0, len(memberIDs))
	inGroup := make(map[string]struct{}, len(memberIDs))
	for _, memberID := range memberIDs {
		if _, ok := s.visibleObjectTypes[memberID]; ok {
			visibleMembers = append(visibleMembers, memberID)
			inGroup[memberID] = struct{}{}
		}
	}
	statistics := &interfaces.Statistics{OtTotal: len(visibleMembers)}
	for _, relationType := range s.relationTypes {
		_, source := inGroup[relationType.SourceObjectTypeID]
		_, target := inGroup[relationType.TargetObjectTypeID]
		if source && target {
			statistics.RtTotal++
		}
	}
	for _, actionType := range s.actionTypes {
		if _, ok := inGroup[actionType.ObjectTypeID]; ok {
			statistics.AtTotal++
		}
	}
	return visibleMembers, statistics
}

func keepByID[T any](items []T, keptIDs []string, id func(T) string) []T {
	kept := make(map[string]struct{}, len(keptIDs))
	for _, keptID := range keptIDs {
		kept[keptID] = struct{}{}
	}
	result := make([]T, 0, len(keptIDs))
	for _, item := range items {
		if _, ok := kept[id(item)]; ok {
			result = append(result, item)
		}
	}
	return result
}
