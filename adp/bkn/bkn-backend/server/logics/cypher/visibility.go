// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"

	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

// permissionVisibility answers what the caller may query by asking bkn-safe
// about the knowledge network's children.
//
// The operation asked for is query_data on each object type and relation type,
// which is the same grant the rest of the service uses to read a concept's
// data. A knowledge-network-wide grant still works without being checked
// separately: query_data on a child inherits from query_data on its parent
// network, so a caller holding the network grant passes the child check, while
// a caller whose access to one object type was revoked does not.
type permissionVisibility struct {
	ps interfaces.PermissionService
}

func (v *permissionVisibility) PermittedObjectTypes(ctx context.Context, knID string, otIDs []string) (map[string]bool, error) {
	return v.permitted(ctx, interfaces.RESOURCE_TYPE_OBJECT_TYPE, knID, otIDs)
}

func (v *permissionVisibility) PermittedRelationTypes(ctx context.Context, knID string, rtIDs []string) (map[string]bool, error) {
	return v.permitted(ctx, interfaces.RESOURCE_TYPE_RELATION_TYPE, knID, rtIDs)
}

func (v *permissionVisibility) permitted(ctx context.Context, resourceType, knID string, childIDs []string) (map[string]bool, error) {
	if len(childIDs) == 0 {
		return map[string]bool{}, nil
	}

	resourceIDs := interfaces.KNChildResourceIDs(knID, childIDs)
	matched, err := permission.FilterKNChildResourceIDs(ctx, v.ps, resourceType, resourceIDs,
		interfaces.OPERATION_TYPE_QUERY_DATA)
	if err != nil {
		return nil, err
	}

	permitted := make(map[string]bool, len(matched))
	for _, childID := range childIDs {
		if _, ok := matched[interfaces.KNChildResourceID(knID, childID)]; ok {
			permitted[childID] = true
		}
	}
	return permitted, nil
}
