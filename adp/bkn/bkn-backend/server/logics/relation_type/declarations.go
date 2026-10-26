// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package relation_type

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

// The relation declarations of BKN spec 3.0 (SPECIFICATION.md §4.3, §7.6): reverse_name, aliases,
// cardinality, constraints, lifecycle, importance, fallback_for, verification and the relation
// properties. A save checks them with the checks a 3.0 import runs; an interactive update keeps the
// ones its request leaves out; an overwrite with a file that does not carry them clears them only
// once confirmed, the same as the declarations of an object type.

// validateDeclarations checks the declarations of a relation type on their own before it is saved,
// and stores the defaults as empty.
func validateDeclarations(ctx context.Context, relationType *interfaces.RelationType) error {
	relationType.NormalizeDeclarations()
	if err := bknsdk.ValidateRelationTypeDeclarations(logics.ToBKNRelationType(relationType)); err != nil {
		return invalidDeclarations(ctx, err.Error())
	}
	return nil
}

func invalidDeclarations(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter_Declarations).
		WithErrorDetails(detail)
}

// clearedDeclarations lists the declaration fields of stored that overwriting it with incoming would
// clear.
func clearedDeclarations(stored, incoming *interfaces.RelationType) []string {
	kept := incoming.DeclaredFields()
	var cleared []string
	for _, field := range stored.DeclaredFields() {
		if !slices.Contains(kept, field) {
			cleared = append(cleared, field)
		}
	}
	return cleared
}

// checkOverwriteKeepsDeclarations refuses an overwrite that would clear stored relation declarations
// unless the request confirmed it, and lists what it would clear. A file is the whole state of what
// it defines, so a declaration it does not carry is cleared; a 2.x package or an old JSON export
// never carries one, and clearing them silently would undo work nobody asked to undo.
//
// checkPermission asks for the modify check on the listed relation types, for a caller that has not
// made it yet; it runs only when there is something to list.
func (rts *relationTypeService) checkOverwriteKeepsDeclarations(ctx context.Context,
	updates []*interfaces.RelationType, checkPermission bool) error {
	if len(updates) == 0 || interfaces.ClearDeclarationsConfirmed(ctx) {
		return nil
	}
	ids := make([]string, 0, len(updates))
	byID := make(map[string]*interfaces.RelationType, len(updates))
	for _, rt := range updates {
		ids = append(ids, rt.RTID)
		byID[rt.RTID] = rt
	}
	stored, err := rts.rta.GetRelationTypesByIDs(ctx, updates[0].KNID, updates[0].Branch, ids)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_RelationType_InternalError_GetRelationTypesByIDsFailed).
			WithErrorDetails(err.Error())
	}
	var cleared []interfaces.ClearedDeclaration
	var clearedIDs []string
	for _, s := range stored {
		in, ok := byID[s.RTID]
		if !ok {
			continue
		}
		if fields := clearedDeclarations(s, in); len(fields) > 0 {
			cleared = append(cleared, interfaces.ClearedDeclaration{RelationTypeID: s.RTID, Fields: fields})
			clearedIDs = append(clearedIDs, s.RTID)
		}
	}
	if len(cleared) == 0 {
		return nil
	}
	if checkPermission {
		if err := permission.CheckKNChildBatchPermission(ctx, rts.ps, interfaces.RESOURCE_TYPE_RELATION_TYPE,
			updates[0].KNID, clearedIDs, interfaces.OPERATION_TYPE_MODIFY); err != nil {
			return err
		}
	}
	return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_RelationType_DeclarationsWouldBeCleared).
		WithErrorDetails(map[string]any{
			"cleared":      cleared,
			"confirm_with": interfaces.QueryParam_ConfirmClearDeclarations + "=true",
		})
}
