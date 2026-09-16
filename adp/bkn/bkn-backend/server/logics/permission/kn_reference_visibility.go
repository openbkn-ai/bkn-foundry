// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"sort"

	"bkn-backend/interfaces"
)

// VisibleReferencedObjectTypes returns which of the given object types may be named to the caller
// through a child resource that points at them: those the caller holds at least one effective
// operation on.
//
// A relation type names its endpoints, an action type its bound and affected object types, and a
// concept group its members. Those references belong to a definition the caller may read, but an
// object type the caller holds nothing on must not surface through them (#1532).
//
// Any effective operation, rather than view_detail, because query_data and execute are entry
// points of their own: ontology-query reads relation and action types with the caller's identity
// when it runs their queries and actions, and requiring view_detail on every referenced object
// type would break exactly the callers granted query_data alone. This is the same projection
// parent navigation uses.
//
// Empty ids are not references and are skipped. An id that cannot be an authorization id is never
// visible. An authorization failure is returned rather than read as "nothing is visible".
func VisibleReferencedObjectTypes(ctx context.Context, ps interfaces.PermissionService,
	knID string, objectTypeIDs []string) (map[string]struct{}, error) {

	visible, err := VisibleReferencedObjectTypesByKN(ctx, ps, map[string][]string{knID: objectTypeIDs})
	if err != nil {
		return nil, err
	}
	return visible[knID], nil
}

// VisibleReferencedObjectTypesByKN is VisibleReferencedObjectTypes for references spread over
// several networks, resolved in one authorization call rather than one per network. The result
// has an entry, possibly empty, for every network asked about.
func VisibleReferencedObjectTypesByKN(ctx context.Context, ps interfaces.PermissionService,
	objectTypeIDsByKN map[string][]string) (map[string]map[string]struct{}, error) {

	knIDs := make([]string, 0, len(objectTypeIDsByKN))
	for knID := range objectTypeIDsByKN {
		knIDs = append(knIDs, knID)
	}
	sort.Strings(knIDs)

	type reference struct{ knID, objectTypeID string }
	visible := make(map[string]map[string]struct{}, len(knIDs))
	referenceByResource := map[string]reference{}
	resourceIDs := make([]string, 0)
	for _, knID := range knIDs {
		visible[knID] = map[string]struct{}{}
		validated := false
		for _, objectTypeID := range objectTypeIDsByKN[knID] {
			if objectTypeID == "" || !interfaces.IsValidAuthorizationID(objectTypeID) {
				continue
			}
			resourceID := interfaces.KNChildResourceID(knID, objectTypeID)
			if _, seen := referenceByResource[resourceID]; seen {
				continue
			}
			if !validated {
				if err := ValidateKNChildAuthorizationIDs(ctx, knID, nil); err != nil {
					return nil, err
				}
				validated = true
			}
			referenceByResource[resourceID] = reference{knID: knID, objectTypeID: objectTypeID}
			resourceIDs = append(resourceIDs, resourceID)
		}
	}
	if len(resourceIDs) == 0 {
		return visible, nil
	}

	matched, err := FilterKNChildResourceIDsWithAnyOperation(ctx, ps, interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		resourceIDs)
	if err != nil {
		return nil, err
	}
	for resourceID := range matched {
		ref := referenceByResource[resourceID]
		visible[ref.knID][ref.objectTypeID] = struct{}{}
	}
	return visible, nil
}

// ReferencesVisible reports whether every non-empty object type id is in visible.
func ReferencesVisible(visible map[string]struct{}, objectTypeIDs ...string) bool {
	for _, objectTypeID := range objectTypeIDs {
		if objectTypeID == "" {
			continue
		}
		if _, ok := visible[objectTypeID]; !ok {
			return false
		}
	}
	return true
}
