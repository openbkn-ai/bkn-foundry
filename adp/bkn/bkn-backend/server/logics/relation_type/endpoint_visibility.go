// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package relation_type

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

// withVisibleEndpoints keeps the relation types whose endpoints may be named to the caller.
//
// Endpoints are part of a relation type's definition, so a relation type that points at an object
// type the caller holds nothing on is left out as a whole rather than returned with that end
// blanked: a blank end would still carry the id, and the caller could not traverse it anyway
// (#1532).
func (rts *relationTypeService) withVisibleEndpoints(ctx context.Context, knID string,
	relationTypes []*interfaces.RelationType) ([]*interfaces.RelationType, error) {

	return keepVisibleEndpoints(ctx, rts.ps, knID, relationTypes)
}

// ReadableRelationTypes keeps, in order, the relation types the caller may read, by the rule every
// relation type read applies: view_detail on the relation type itself, and at least one effective
// operation on both of its endpoints. It serves callers outside this package that read relation
// type definitions straight from storage, such as relation type paths (#1553).
func ReadableRelationTypes(ctx context.Context, ps interfaces.PermissionService, knID string,
	relationTypes []*interfaces.RelationType) ([]*interfaces.RelationType, error) {

	readable, _, err := permission.FilterAndPaginateKNChildren(ctx, ps, interfaces.RESOURCE_TYPE_RELATION_TYPE,
		knID, relationTypes, func(relationType *interfaces.RelationType) string { return relationType.RTID }, 0, -1)
	if err != nil {
		return nil, err
	}
	return keepVisibleEndpoints(ctx, ps, knID, readable)
}

func keepVisibleEndpoints(ctx context.Context, ps interfaces.PermissionService, knID string,
	relationTypes []*interfaces.RelationType) ([]*interfaces.RelationType, error) {

	if len(relationTypes) == 0 {
		return relationTypes, nil
	}
	endpointIDs := make([]string, 0, len(relationTypes)*2)
	for _, relationType := range relationTypes {
		endpointIDs = append(endpointIDs, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID)
	}
	visible, err := permission.VisibleReferencedObjectTypes(ctx, ps, knID, endpointIDs)
	if err != nil {
		return nil, err
	}
	kept := make([]*interfaces.RelationType, 0, len(relationTypes))
	for _, relationType := range relationTypes {
		if permission.ReferencesVisible(visible, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID) {
			kept = append(kept, relationType)
		}
	}
	return kept, nil
}

// withVisibleEndpointIDs narrows relation type ids, in order, to those whose endpoints may be
// named to the caller. Search restricts its dataset query to these ids, so a hidden relation type
// is neither returned nor counted.
func (rts *relationTypeService) withVisibleEndpointIDs(ctx context.Context, knID, branch string,
	relationTypeIDs []string) ([]string, error) {

	if len(relationTypeIDs) == 0 {
		return relationTypeIDs, nil
	}
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	// One read of the network's relation types rather than an IN list of every visible id.
	relationTypes, err := rts.rta.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      knID,
		Branch:                    branch,
	})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}
	requested := make(map[string]struct{}, len(relationTypeIDs))
	for _, relationTypeID := range relationTypeIDs {
		requested[relationTypeID] = struct{}{}
	}
	candidates := make([]*interfaces.RelationType, 0, len(relationTypeIDs))
	for _, relationType := range relationTypes {
		if _, ok := requested[relationType.RTID]; ok {
			candidates = append(candidates, relationType)
		}
	}
	kept, err := rts.withVisibleEndpoints(ctx, knID, candidates)
	if err != nil {
		return nil, err
	}
	keptIDs := make(map[string]struct{}, len(kept))
	for _, relationType := range kept {
		keptIDs[relationType.RTID] = struct{}{}
	}
	visibleIDs := make([]string, 0, len(kept))
	for _, relationTypeID := range relationTypeIDs {
		if _, ok := keptIDs[relationTypeID]; ok {
			visibleIDs = append(visibleIDs, relationTypeID)
		}
	}
	return visibleIDs, nil
}
