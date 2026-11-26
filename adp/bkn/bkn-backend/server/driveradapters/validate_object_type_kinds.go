// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/object_type"
)

// completeObjectTypeKinds fills what the kinds of an object type keep before it is validated.
func completeObjectTypeKinds(ctx context.Context, objectType *interfaces.ObjectType) error {
	return object_type.CompleteKinds(ctx, objectType)
}

// withoutKindRelations leaves out of relationTypes those the kinds of the object types otIDs
// keep for them: they are deleted with the object types rather than keeping them.
func (r *restHandler) withoutKindRelations(ctx context.Context, knID, branch string, otIDs []string,
	relationTypes []*interfaces.RelationType) ([]*interfaces.RelationType, error) {
	if len(relationTypes) == 0 || !logics.AnyObjectTypeKind() {
		return relationTypes, nil
	}
	objectTypes, err := r.ots.GetObjectTypesMapByIDs(ctx, knID, branch, otIDs, false)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, objectType := range objectTypes {
		for _, id := range logics.OwnedRelationTypeIDs(objectType) {
			owned[id] = true
		}
	}
	if len(owned) == 0 {
		return relationTypes, nil
	}
	kept := make([]*interfaces.RelationType, 0, len(relationTypes))
	for _, relationType := range relationTypes {
		if !owned[relationType.RTID] {
			kept = append(kept, relationType)
		}
	}
	return kept, nil
}
