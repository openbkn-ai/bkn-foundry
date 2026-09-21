// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/permission_service.go -destination ../interfaces/mock/mock_permission_service.go
type PermissionService interface {
	CheckPermission(ctx context.Context, resource PermissionResource, ops []string) error
	RequirePermissions(ctx context.Context, requirements []PermissionRequirement) error
	FilterFullPropertyAccess(ctx context.Context, objectTypeRef string, properties []string) ([]string, error)
	// FilterVisiblePropertyAccess returns properties whose effective access level is not none.
	FilterVisiblePropertyAccess(ctx context.Context, objectTypeRef string, properties []string) ([]string, error)
	// ResolvePropertyAccessLevels returns the caller's effective level for each
	// named property of one object type: PROPERTY_ACCESS_FULL, _MASKED, _SCHEMA
	// or _NONE.
	ResolvePropertyAccessLevels(ctx context.Context, objectTypeRef string, properties []string) (map[string]string, error)
	// ResolveRowFilters returns the effective row predicate for every requested
	// object class in one bkn-safe decision. It is fail-closed for service-app
	// principals because bkn-safe evaluates row policies as directory users.
	ResolveRowFilters(ctx context.Context, objectTypeRefs []string) ([]RowFilterDecisionEntry, error)
	RequireFullPropertyAccess(ctx context.Context, objectTypeRef string, properties []string) error
	// FilterVisibleResources returns only resources satisfying every visibility operation.
	FilterVisibleResources(ctx context.Context, resourceType string, ids []string,
		visibilityOperations []string) (map[string]PermissionResourceOps, error)
	// FilterVisibleResourcesWithOperations also returns each visible resource's
	// complete registry-backed effective operation set.
	FilterVisibleResourcesWithOperations(ctx context.Context, resourceType string, ids []string,
		visibilityOperations []string) (map[string]PermissionResourceOps, error)

	CreateResources(ctx context.Context, resources []PermissionResource, ops []string) error
	DeleteResources(ctx context.Context, resourceType string, ids []string) error
	UpsertResourceParents(ctx context.Context, resourceType, parentType string, items []PermissionResourceParent) error
	DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error
	UpdateResource(ctx context.Context, resource PermissionResource) error
}
