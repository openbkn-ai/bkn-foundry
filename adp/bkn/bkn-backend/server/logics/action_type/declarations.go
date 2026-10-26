// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

import (
	"context"
	"net/http"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// The BKN spec 3.0 declarations of an action type are its aliases and its lifecycle. A save checks
// them with the checks a 3.0 import runs; an interactive update keeps the ones its request leaves
// out; an overwrite with a file that does not carry them clears them only once confirmed, the same as
// the property declarations of an object type.

// checkDeclarations checks the aliases and the lifecycle of an action type on their own, with the
// checks a 3.0 import runs.
func (ats *actionTypeService) checkDeclarations(ctx context.Context, actionType *interfaces.ActionType) error {
	actionType.Aliases = interfaces.NormalizeAliases(actionType.Aliases)
	actionType.Lifecycle = interfaces.NormalizeLifecycle(actionType.Lifecycle)
	if err := bknsdk.ValidateActionTypeDeclarations(logics.ToBKNActionType(actionType)); err != nil {
		return invalidDeclarations(ctx, err.Error())
	}
	return nil
}

// checkAliasNames checks the action types of one save against the network they are saved into: no
// alias is the name of another action type, in either direction. It reads the stored action types
// that have aliases, and the stored ones named like an alias of the save; the save's renames and
// alias changes count as done. A network without aliases answers both reads with no rows.
func (ats *actionTypeService) checkAliasNames(ctx context.Context, knID, branch string, actionTypes []*interfaces.ActionType) error {
	if len(actionTypes) == 0 {
		return nil
	}
	incoming := make([]logics.NamedDefinition, 0, len(actionTypes))
	var aliases []string
	for _, at := range actionTypes {
		incoming = append(incoming, logics.NamedDefinition{ID: at.ATID, Name: at.ATName, Aliases: at.Aliases})
		aliases = append(aliases, at.Aliases...)
	}
	storedAliased, err := ats.ata.ListAliasedActionTypes(ctx, knID, branch)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}
	var storedNamed []*interfaces.ActionType
	if len(aliases) > 0 {
		if storedNamed, err = ats.ata.GetActionTypeIdentitiesByIDsOrNames(ctx, knID, branch, nil, aliases); err != nil {
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
				WithErrorDetails(err.Error())
		}
	}
	if conflict := logics.AliasConflict("action type", incoming, namedActionTypes(storedNamed), namedActionTypes(storedAliased)); conflict != "" {
		return invalidDeclarations(ctx, conflict)
	}
	return nil
}

func namedActionTypes(actionTypes []*interfaces.ActionType) []logics.NamedDefinition {
	named := make([]logics.NamedDefinition, 0, len(actionTypes))
	for _, at := range actionTypes {
		named = append(named, logics.NamedDefinition{ID: at.ATID, Name: at.ATName, Aliases: at.Aliases})
	}
	return named
}

func invalidDeclarations(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter_Declarations).
		WithErrorDetails(detail)
}

// keepStoredDeclarations makes an interactive update keep the stored aliases and lifecycle its
// request left out. It reads the stored action type only then.
func (ats *actionTypeService) keepStoredDeclarations(ctx context.Context, actionType *interfaces.ActionType) error {
	if actionType.DeclaresAliases() && actionType.DeclaresLifecycle() {
		return nil
	}
	stored, err := ats.ata.GetActionTypesByIDs(ctx, actionType.KNID, actionType.Branch, []string{actionType.ATID})
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}
	if len(stored) == 1 {
		if !actionType.DeclaresAliases() {
			actionType.Aliases = stored[0].Aliases
		}
		if !actionType.DeclaresLifecycle() {
			actionType.Lifecycle = stored[0].Lifecycle
		}
	}
	return nil
}

// checkOverwriteKeepsDeclarations refuses an overwrite that would clear stored aliases or a stored
// lifecycle unless the
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
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}
	var cleared []interfaces.ClearedDeclaration
	var clearedIDs []string
	for _, s := range stored {
		in, ok := byID[s.ATID]
		if !ok {
			continue
		}
		var fields []string
		if len(s.Aliases) > 0 && len(in.Aliases) == 0 {
			fields = append(fields, interfaces.DECLARATION_ALIASES)
		}
		if s.Lifecycle != nil && interfaces.NormalizeLifecycle(in.Lifecycle) == nil {
			fields = append(fields, interfaces.DECLARATION_LIFECYCLE)
		}
		if len(fields) > 0 {
			cleared = append(cleared, interfaces.ClearedDeclaration{ActionTypeID: s.ATID, Fields: fields})
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
	return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ActionType_DeclarationsWouldBeCleared).
		WithErrorDetails(map[string]any{
			"cleared":      cleared,
			"confirm_with": interfaces.QueryParam_ConfirmClearDeclarations + "=true",
		})
}
