// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// An object type's kinds give it platform behaviours, each configured by the field of the same
// name. The module that provides a kind registers it with logics.RegisterObjectTypeKind; a save
// hands every object type of a kind to it, to check the configuration, fill what the platform
// keeps and keep what the kind makes beside the object type.

func invalidKinds(ctx context.Context, format string, args ...any) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf(format, args...))
}

// checkKindList checks the kinds an object type lists against what the platform provides and
// against the stored object type it replaces: kinds are fixed once the object type exists.
func checkKindList(ctx context.Context, objectType, stored *interfaces.ObjectType) error {
	seen := make(map[string]bool, len(objectType.Kinds))
	for _, name := range objectType.Kinds {
		if seen[name] {
			return invalidKinds(ctx, "kinds lists %q more than once", name)
		}
		seen[name] = true
		kind := logics.ObjectTypeKindNamed(name)
		if kind == nil || !kind.Available() {
			// The same answer whether the kind is unknown or not licensed: the license is
			// told by the capability snapshot, not by an error.
			return invalidKinds(ctx, "unsupported object type kind %q", name)
		}
	}
	if objectType.Event != nil && !seen[interfaces.ObjectTypeKindEvent] {
		return invalidKinds(ctx, "event is the configuration of the event kind, which kinds does not list")
	}
	if stored == nil {
		return nil
	}
	before, after := slices.Clone(stored.Kinds), slices.Clone(objectType.Kinds)
	slices.Sort(before)
	slices.Sort(after)
	if !slices.Equal(before, after) {
		return invalidKinds(ctx, "the kinds of object type %s are fixed once it exists: %v, not %v",
			objectType.OTID, stored.Kinds, objectType.Kinds)
	}
	return nil
}

// prepareKinds checks the kinds of an object type about to be saved over stored, nil for a new
// one, and fills the definitions they keep.
func prepareKinds(ctx context.Context, objectType, stored *interfaces.ObjectType) error {
	if len(objectType.Kinds) == 0 && objectType.Event == nil && (stored == nil || len(stored.Kinds) == 0) {
		return nil
	}
	if err := checkKindList(ctx, objectType, stored); err != nil {
		return err
	}
	for _, kind := range logics.KindsOf(objectType) {
		if err := kind.Complete(ctx, objectType, stored); err != nil {
			return err
		}
		if err := kind.Check(ctx, objectType, stored); err != nil {
			return err
		}
	}
	return nil
}

// prepareSavedKinds runs prepareKinds on what a create or import saves. An object type an
// ignore import leaves as it is, and one a normal import refuses for existing, is not checked
// against what is stored.
func (ots *objectTypeService) prepareSavedKinds(ctx context.Context, objectTypes []*interfaces.ObjectType,
	mode string) (map[string]*interfaces.ObjectType, error) {
	stored := map[string]*interfaces.ObjectType{}
	if len(objectTypes) == 0 || !logics.AnyObjectTypeKind() ||
		mode != interfaces.ImportMode_Overwrite && mode != interfaces.ImportMode_Ignore {
		return stored, nil
	}
	ids := make([]string, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		if objectType.OTID != "" {
			ids = append(ids, objectType.OTID)
		}
	}
	if len(ids) == 0 {
		return stored, nil
	}
	found, err := ots.ota.GetObjectTypesByIDs(ctx, nil, objectTypes[0].KNID, objectTypes[0].Branch, ids)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).WithErrorDetails(err.Error())
	}
	for _, objectType := range found {
		stored[objectType.OTID] = objectType
	}
	return stored, nil
}

// savedKinds keeps what the kinds make beside the object types a save wrote, in its transaction.
// stored holds the object types they replaced, by ID.
func savedKinds(ctx context.Context, tx *sql.Tx, objectTypes []*interfaces.ObjectType,
	stored map[string]*interfaces.ObjectType) error {
	for _, objectType := range objectTypes {
		for _, kind := range logics.KindsOf(objectType) {
			if err := kind.Saved(logics.WithKindWrite(ctx), tx, objectType, stored[objectType.OTID]); err != nil {
				return err
			}
		}
	}
	return nil
}

// deletingKinds lets the kinds of the object types being deleted refuse the deletion or remove
// what they keep beside them, in the transaction that deletes them.
func deletingKinds(ctx context.Context, tx *sql.Tx, objectTypes []*interfaces.ObjectType) error {
	for _, objectType := range objectTypes {
		for _, kind := range logics.KindsOf(objectType) {
			if err := kind.Deleting(logics.WithKindWrite(ctx), tx, objectType); err != nil {
				return err
			}
		}
	}
	return nil
}

// CompleteKinds fills the definitions the kinds of a new or changed object type keep, before the
// rest of it is validated, without reading what is stored: the save checks the kinds again
// against the object type it replaces.
func CompleteKinds(ctx context.Context, objectType *interfaces.ObjectType) error {
	if len(objectType.Kinds) == 0 && objectType.Event == nil {
		return nil
	}
	if err := checkKindList(ctx, objectType, nil); err != nil {
		return err
	}
	for _, kind := range logics.KindsOf(objectType) {
		if err := kind.Complete(ctx, objectType, nil); err != nil {
			return err
		}
	}
	return nil
}
