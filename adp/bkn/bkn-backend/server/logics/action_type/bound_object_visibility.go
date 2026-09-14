// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

// withVisibleBoundObjectTypes keeps the action types whose bound object type may be named to the
// caller.
//
// The bound object type is what an action acts on: running it starts from instances of that type.
// An action type bound to an object type the caller holds nothing on is left out as a whole rather
// than returned with the binding blanked, since the id would remain and the caller could not run
// it anyway (#1532).
//
// Affected object types and impact contracts are deliberately not checked. An action is also how a
// caller is let write to data they cannot read, and ontology-query reads action types with the
// caller's identity to run them; hiding an action because of what it affects would refuse exactly
// that delegation.
func (ats *actionTypeService) withVisibleBoundObjectTypes(ctx context.Context, knID string,
	actionTypes []*interfaces.ActionType) ([]*interfaces.ActionType, error) {

	if len(actionTypes) == 0 {
		return actionTypes, nil
	}
	boundIDs := make([]string, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		boundIDs = append(boundIDs, actionType.ObjectTypeID)
	}
	visible, err := permission.VisibleReferencedObjectTypes(ctx, ats.ps, knID, boundIDs)
	if err != nil {
		return nil, err
	}
	kept := make([]*interfaces.ActionType, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		if permission.ReferencesVisible(visible, actionType.ObjectTypeID) {
			kept = append(kept, actionType)
		}
	}
	return kept, nil
}

// withVisibleBoundObjectTypeIDs narrows action type ids, in order, to those whose bound object type
// may be named to the caller. Search restricts its dataset query to these ids, so a hidden action
// type is neither returned nor counted.
func (ats *actionTypeService) withVisibleBoundObjectTypeIDs(ctx context.Context, knID, branch string,
	actionTypeIDs []string) ([]string, error) {

	if len(actionTypeIDs) == 0 {
		return actionTypeIDs, nil
	}
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	// One read of the network's action types rather than an IN list of every visible id.
	actionTypes, err := ats.ata.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      knID,
		Branch:                    branch,
	})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}
	requested := make(map[string]struct{}, len(actionTypeIDs))
	for _, actionTypeID := range actionTypeIDs {
		requested[actionTypeID] = struct{}{}
	}
	candidates := make([]*interfaces.ActionType, 0, len(actionTypeIDs))
	for _, actionType := range actionTypes {
		if _, ok := requested[actionType.ATID]; ok {
			candidates = append(candidates, actionType)
		}
	}
	kept, err := ats.withVisibleBoundObjectTypes(ctx, knID, candidates)
	if err != nil {
		return nil, err
	}
	keptIDs := make(map[string]struct{}, len(kept))
	for _, actionType := range kept {
		keptIDs[actionType.ATID] = struct{}{}
	}
	visibleIDs := make([]string, 0, len(kept))
	for _, actionTypeID := range actionTypeIDs {
		if _, ok := keptIDs[actionTypeID]; ok {
			visibleIDs = append(visibleIDs, actionTypeID)
		}
	}
	return visibleIDs, nil
}
