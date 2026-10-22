// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"database/sql"
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

// The property declarations of BKN spec 3.0 (SPECIFICATION.md §6.8) ride in the data properties of
// an object type. A save checks them with the checks a 3.0 import runs; an interactive update keeps
// the declarations its request leaves out; an overwrite with a file that does not carry them clears
// them only once confirmed.

// validateDeclarations checks the property declarations of an object type before it is saved.
func validateDeclarations(ctx context.Context, objectType *interfaces.ObjectType) error {
	for _, p := range objectType.DataProperties {
		if p == nil {
			continue
		}
		// Value types are stored from #2281; a reference to one could not be resolved.
		if p.Constraints != nil && p.Constraints.ValueType != "" {
			return invalidDeclarations(ctx, fmt.Sprintf("data property %q: constraints value_type is not supported by this version yet", p.Name))
		}
		if p.Declares(interfaces.DECLARATION_REQUIRED) && !p.Required && slices.Contains(objectType.PrimaryKeys, p.Name) {
			return invalidDeclarations(ctx, fmt.Sprintf("data property %q: a primary key is required by definition; leave required out instead of writing false", p.Name))
		}
		if p.Constraints.IsEmpty() {
			p.Constraints = nil
		}
	}
	if err := bknsdk.ValidateObjectTypeDeclarations(logics.ToBKNObjectType(objectType)); err != nil {
		return invalidDeclarations(ctx, err.Error())
	}
	return nil
}

func invalidDeclarations(ctx context.Context, detail string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter_Declarations).
		WithErrorDetails(detail)
}

// keepUndeclared merges an interactive update into the stored object type: a data property of the
// request keeps each declaration field it leaves out from the stored property of the same name.
func keepUndeclared(update *interfaces.ObjectType, stored *interfaces.ObjectType) {
	if stored == nil {
		return
	}
	storedByName := make(map[string]*interfaces.DataProperty, len(stored.DataProperties))
	for _, p := range stored.DataProperties {
		if p != nil {
			storedByName[p.Name] = p
		}
	}
	for _, p := range update.DataProperties {
		if p != nil {
			p.KeepUndeclared(storedByName[p.Name])
		}
	}
}

// clearedDeclarations lists the declarations of stored that overwriting it with incoming would
// clear: a declaration incoming does not carry, on a property it keeps or drops.
func clearedDeclarations(stored, incoming *interfaces.ObjectType) []interfaces.ClearedDeclaration {
	incomingByName := map[string]*interfaces.DataProperty{}
	for _, p := range incoming.DataProperties {
		if p != nil {
			incomingByName[p.Name] = p
		}
	}
	var cleared []interfaces.ClearedDeclaration
	for _, s := range stored.DataProperties {
		if !s.HasDeclarations(stored.PrimaryKeys) {
			continue
		}
		in := incomingByName[s.Name]
		var fields []string
		if s.Semantic != "" && (in == nil || in.Semantic == "") {
			fields = append(fields, interfaces.DECLARATION_SEMANTIC)
		}
		if s.Required && !slices.Contains(stored.PrimaryKeys, s.Name) && (in == nil || !in.Required) {
			fields = append(fields, interfaces.DECLARATION_REQUIRED)
		}
		if !s.Constraints.IsEmpty() && (in == nil || in.Constraints.IsEmpty()) {
			fields = append(fields, interfaces.DECLARATION_CONSTRAINTS)
		}
		if len(fields) > 0 {
			cleared = append(cleared, interfaces.ClearedDeclaration{ObjectTypeID: stored.OTID, Property: s.Name, Fields: fields})
		}
	}
	return cleared
}

// checkOverwriteKeepsDeclarations refuses an overwrite that would clear stored declarations unless
// the request confirmed it, and lists what it would clear. A file is the whole state of what it
// defines, so a declaration it does not carry is cleared; a 2.x package or an old JSON export never
// carries one, and clearing them silently would undo work nobody asked to undo.
//
// The list shows stored declarations, so only a caller who may modify those object types sees it.
// checkPermission asks for that check here, for a caller that has not made it yet; it runs only when
// there is something to list, so an overwrite of object types without declarations costs nothing
// more than before.
func (ots *objectTypeService) checkOverwriteKeepsDeclarations(ctx context.Context, tx *sql.Tx,
	updates []*interfaces.ObjectType, checkPermission bool) error {
	if len(updates) == 0 || interfaces.ClearDeclarationsConfirmed(ctx) {
		return nil
	}
	ids := make([]string, 0, len(updates))
	byID := make(map[string]*interfaces.ObjectType, len(updates))
	for _, ot := range updates {
		ids = append(ids, ot.OTID)
		byID[ot.OTID] = ot
	}
	stored, err := ots.ota.GetObjectTypesByIDs(ctx, tx, updates[0].KNID, updates[0].Branch, ids)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError_GetObjectTypeByIDFailed).
			WithErrorDetails(err.Error())
	}
	var cleared []interfaces.ClearedDeclaration
	for _, s := range stored {
		if in, ok := byID[s.OTID]; ok {
			cleared = append(cleared, clearedDeclarations(s, in)...)
		}
	}
	if len(cleared) == 0 {
		return nil
	}
	if checkPermission {
		var ids []string
		for _, c := range cleared {
			if !slices.Contains(ids, c.ObjectTypeID) {
				ids = append(ids, c.ObjectTypeID)
			}
		}
		if err := permission.CheckKNChildBatchPermission(ctx, ots.ps, interfaces.RESOURCE_TYPE_OBJECT_TYPE,
			updates[0].KNID, ids, interfaces.OPERATION_TYPE_MODIFY); err != nil {
			return err
		}
	}
	return rest.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared).
		WithErrorDetails(map[string]any{
			"cleared":      cleared,
			"confirm_with": interfaces.QueryParam_ConfirmClearDeclarations + "=true",
		})
}
