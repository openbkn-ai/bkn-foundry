// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// The BKN spec 3.0 declarations of an action type are its aliases. A save checks them with the
// checks a 3.0 import runs; an interactive update keeps the aliases its request leaves out; an
// overwrite with a file that does not carry them clears them only once confirmed, the same as the
// property declarations of an object type.

// checkDeclarations checks the aliases of an action type before it is saved: on their own, and
// against the names of the other action types of the network. An action type without aliases
// reads nothing more.
func (ats *actionTypeService) checkDeclarations(ctx context.Context, actionType *interfaces.ActionType) error {
	actionType.Aliases = interfaces.NormalizeAliases(actionType.Aliases)
	if err := bknsdk.ValidateActionTypeDeclarations(logics.ToBKNActionType(actionType)); err != nil {
		return invalidDeclarations(ctx, err.Error())
	}
	if len(actionType.Aliases) == 0 {
		return nil
	}
	named, err := ats.ata.GetActionTypeIdentitiesByIDsOrNames(ctx, actionType.KNID, actionType.Branch, nil, actionType.Aliases)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}
	for _, other := range named {
		if other.ATID != actionType.ATID {
			return invalidDeclarations(ctx, fmt.Sprintf("action type %q alias %q is the name of action type %q",
				actionType.ATID, other.ATName, other.ATID))
		}
	}
	return nil
}

func invalidDeclarations(ctx context.Context, detail string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter_Declarations).
		WithErrorDetails(detail)
}

// keepStoredAliases makes an interactive update keep the stored aliases when its request left
// them out. It reads the stored action type only then.
func (ats *actionTypeService) keepStoredAliases(ctx context.Context, actionType *interfaces.ActionType) error {
	if actionType.DeclaresAliases() {
		return nil
	}
	stored, err := ats.ata.GetActionTypesByIDs(ctx, actionType.KNID, actionType.Branch, []string{actionType.ATID})
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}
	if len(stored) == 1 {
		actionType.Aliases = stored[0].Aliases
	}
	return nil
}

// checkOverwriteKeepsDeclarations refuses an overwrite that would clear stored aliases unless the
// request confirmed it, and lists what it would clear. checkPermission asks for the modify check
// on the listed action types, for a caller that has not made it yet; it runs only when there is
// something to list.
func (ats *actionTypeService) checkOverwriteKeepsDeclarations(ctx context.Context,
	updates []*interfaces.ActionType, checkPermission bool) error {
	if len(updates) == 0 || interfaces.ClearDeclarationsConfirmed(ctx) {
		return nil
	}
	ids := make([]string, 0, len(updates))
	byID := make(map[string]*interfaces.ActionType, len(updates))
	for _, at := range updates {
		ids = append(ids, at.ATID)
		byID[at.ATID] = at
	}
	stored, err := ats.ata.GetActionTypesByIDs(ctx, updates[0].KNID, updates[0].Branch, ids)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}
	var cleared []interfaces.ClearedDeclaration
	var clearedIDs []string
	for _, s := range stored {
		if in, ok := byID[s.ATID]; ok && len(s.Aliases) > 0 && len(in.Aliases) == 0 {
			cleared = append(cleared, interfaces.ClearedDeclaration{ActionTypeID: s.ATID, Fields: []string{interfaces.DECLARATION_ALIASES}})
			if !slices.Contains(clearedIDs, s.ATID) {
				clearedIDs = append(clearedIDs, s.ATID)
			}
		}
	}
	if len(cleared) == 0 {
		return nil
	}
	if checkPermission {
		if err := permission.CheckKNChildBatchPermission(ctx, ats.ps, interfaces.RESOURCE_TYPE_ACTION_TYPE,
			updates[0].KNID, clearedIDs, interfaces.OPERATION_TYPE_MODIFY); err != nil {
			return err
		}
	}
	return rest.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ActionType_DeclarationsWouldBeCleared).
		WithErrorDetails(map[string]any{
			"cleared":      cleared,
			"confirm_with": interfaces.QueryParam_ConfirmClearDeclarations + "=true",
		})
}
