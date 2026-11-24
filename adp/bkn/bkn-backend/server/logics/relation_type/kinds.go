// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package relation_type

import (
	"context"
	"net/http"
	"sort"
	"strings"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// refuseKindRelations refuses a write of relation types that a kind keeps for its object types,
// such as an event class's relation to the object type its events are about. Only the kind
// writes them, with the object type. incoming are the relation types being saved, and ids the
// ones being deleted.
func (rts *relationTypeService) refuseKindRelations(ctx context.Context, knID, branch string,
	incoming []*interfaces.RelationType, ids []string) error {
	if logics.IsKindWrite(ctx) || !logics.AnyObjectTypeKind() {
		return nil
	}
	written := map[string]bool{}
	owners := map[string]bool{}
	for _, relationType := range incoming {
		written[relationType.RTID] = true
		if relationType.SourceObjectTypeID != "" {
			owners[relationType.SourceObjectTypeID] = true
		}
	}
	for _, id := range ids {
		written[id] = true
	}
	if len(written) == 0 {
		return nil
	}
	// The stored relation type of an ID decides as much as the one being written: an import must
	// not take over a kind's relation by giving it another source.
	writtenIDs := make([]string, 0, len(written))
	for id := range written {
		if id != "" {
			writtenIDs = append(writtenIDs, id)
		}
	}
	stored, err := rts.rta.GetRelationTypesByIDs(ctx, knID, branch, writtenIDs)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_RelationType_InternalError).
			WithErrorDetails(err.Error())
	}
	for _, relationType := range stored {
		if relationType.SourceObjectTypeID != "" {
			owners[relationType.SourceObjectTypeID] = true
		}
	}
	if len(owners) == 0 {
		return nil
	}
	ownerIDs := make([]string, 0, len(owners))
	for id := range owners {
		ownerIDs = append(ownerIDs, id)
	}
	objectTypes, err := rts.ots.GetObjectTypesMapByIDs(ctx, knID, branch, ownerIDs, false)
	if err != nil {
		return err
	}
	var refused []string
	for _, objectType := range objectTypes {
		for _, id := range logics.OwnedRelationTypeIDs(objectType) {
			if written[id] {
				refused = append(refused, id)
			}
		}
	}
	if len(refused) == 0 {
		return nil
	}
	sort.Strings(refused)
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
		WithErrorDetails("the platform keeps relation types " + strings.Join(refused, ", ") +
			" with the object types they start from; change those object types instead")
}
