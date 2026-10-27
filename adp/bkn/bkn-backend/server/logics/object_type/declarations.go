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

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

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
		if slices.Contains(objectType.PrimaryKeys, p.Name) {
			if p.Declares(interfaces.DECLARATION_REQUIRED) && !p.Required {
				return invalidDeclarations(ctx, fmt.Sprintf("data property %q: a primary key is required by definition; leave required out instead of writing false", p.Name))
			}
			// A primary key is required by definition, so required on it says nothing. It is not
			// stored, as the 3.0 export already leaves it out; a value stored before is dropped on
			// the next save.
			p.DropImpliedRequired()
		}
		if p.Constraints.IsEmpty() {
			p.Constraints = nil
		}
		p.Aliases = interfaces.NormalizeAliases(p.Aliases)
	}
	objectType.Aliases = interfaces.NormalizeAliases(objectType.Aliases)
	objectType.Lifecycle = interfaces.NormalizeLifecycle(objectType.Lifecycle)
	if err := bknsdk.ValidateObjectTypeDeclarations(logics.ToBKNObjectType(objectType)); err != nil {
		return invalidDeclarations(ctx, err.Error())
	}
	return nil
}

// checkNetworkDeclarations resolves the value types the data properties of an object type name:
// each must be a value type of the network, with the property's Type as its base_type. An object
// type without value types reads nothing more.
func (ots *objectTypeService) checkNetworkDeclarations(ctx context.Context, objectType *interfaces.ObjectType) error {
	var ids []string
	for _, p := range objectType.DataProperties {
		if p != nil && p.Constraints != nil && p.Constraints.ValueType != "" && !slices.Contains(ids, p.Constraints.ValueType) {
			ids = append(ids, p.Constraints.ValueType)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	// An import resolves the value types it writes itself before the stored ones: they are not
	// committed yet, and they replace the stored ones of the same ID.
	var valueTypes []*interfaces.ValueType
	var stored []string
	pending := map[string]*interfaces.ValueType{}
	for _, vt := range interfaces.PendingValueTypes(ctx) {
		pending[vt.VTID] = vt
	}
	for _, id := range ids {
		if vt, ok := pending[id]; ok {
			valueTypes = append(valueTypes, vt)
		} else {
			stored = append(stored, id)
		}
	}
	if len(stored) > 0 {
		found, err := ots.vta.GetValueTypesByIDs(ctx, nil, objectType.KNID, objectType.Branch, stored)
		if err != nil {
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(err.Error())
		}
		valueTypes = append(valueTypes, found...)
	}
	// The network holds this object type and its value types only, so it checks the value type
	// references alone: the lifecycle refers to other object types, which are not in it.
	ot := logics.ToBKNObjectType(objectType)
	ot.Lifecycle = nil
	doc := &bknsdk.BknNetwork{ObjectTypes: []*bknsdk.BknObjectType{ot}}
	for _, vt := range valueTypes {
		doc.ValueTypes = append(doc.ValueTypes, logics.ToBKNValueType(vt))
	}
	if err := bknsdk.ValidateNetworkDeclarations(doc); err != nil {
		return invalidDeclarations(ctx, err.Error())
	}
	return nil
}

// checkAliasNames checks the object types of one save against the network they are saved into: no
// alias is the name of another object type, in either direction. It reads the stored object types
// that have aliases, and the stored ones named like an alias of the save; the save's renames and
// alias changes count as done. A network without aliases answers both reads with no rows.
func (ots *objectTypeService) checkAliasNames(ctx context.Context, knID, branch string, objectTypes []*interfaces.ObjectType) error {
	if len(objectTypes) == 0 {
		return nil
	}
	incoming := make([]logics.NamedDefinition, 0, len(objectTypes))
	var aliases []string
	for _, ot := range objectTypes {
		incoming = append(incoming, logics.NamedDefinition{ID: ot.OTID, Name: ot.OTName, Aliases: ot.Aliases})
		aliases = append(aliases, ot.Aliases...)
	}
	storedAliased, err := ots.ota.ListAliasedObjectTypes(ctx, knID, branch)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError).
			WithErrorDetails(err.Error())
	}
	var storedNamed []*interfaces.ObjectType
	if len(aliases) > 0 {
		if storedNamed, err = ots.ota.GetObjectTypeIdentitiesByIDsOrNames(ctx, knID, branch, nil, aliases); err != nil {
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(err.Error())
		}
	}
	if conflict := logics.AliasConflict("object type", incoming, namedObjectTypes(storedNamed), namedObjectTypes(storedAliased)); conflict != "" {
		return invalidDeclarations(ctx, conflict)
	}
	return nil
}

func namedObjectTypes(objectTypes []*interfaces.ObjectType) []logics.NamedDefinition {
	named := make([]logics.NamedDefinition, 0, len(objectTypes))
	for _, ot := range objectTypes {
		named = append(named, logics.NamedDefinition{ID: ot.OTID, Name: ot.OTName, Aliases: ot.Aliases})
	}
	return named
}

func invalidDeclarations(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter_Declarations).
		WithErrorDetails(detail)
}

// keepUndeclared merges an interactive update into the stored object type: a data property of the
// request keeps each declaration field it leaves out from the stored property of the same name.
func keepUndeclared(update *interfaces.ObjectType, stored *interfaces.ObjectType) {
	if stored == nil {
		return
	}
	if !update.DeclaresAliases() {
		update.Aliases = stored.Aliases
	}
	if !update.DeclaresLifecycle() {
		update.Lifecycle = stored.Lifecycle
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
	var own []string
	if len(stored.Aliases) > 0 && len(incoming.Aliases) == 0 {
		own = append(own, interfaces.DECLARATION_ALIASES)
	}
	if stored.Lifecycle != nil && interfaces.NormalizeLifecycle(incoming.Lifecycle) == nil {
		own = append(own, interfaces.DECLARATION_LIFECYCLE)
	}
	if len(own) > 0 {
		cleared = append(cleared, interfaces.ClearedDeclaration{ObjectTypeID: stored.OTID, Fields: own})
	}
	for _, s := range stored.DataProperties {
		if !s.HasDeclarations(stored.PrimaryKeys) {
			continue
		}
		in := incomingByName[s.Name]
		var fields []string
		if len(s.Aliases) > 0 && (in == nil || len(in.Aliases) == 0) {
			fields = append(fields, interfaces.DECLARATION_ALIASES)
		}
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
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError_GetObjectTypeByIDFailed).
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
	return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared).
		WithErrorDetails(map[string]any{
			"cleared":      cleared,
			"confirm_with": interfaces.QueryParam_ConfirmClearDeclarations + "=true",
		})
}
