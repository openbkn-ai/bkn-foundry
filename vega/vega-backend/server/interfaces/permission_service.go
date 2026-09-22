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
	CheckPermissions(ctx context.Context, requirements []PermissionRequirement) ([]PermissionCheckResult, error)
	RequirePermissions(ctx context.Context, requirements []PermissionRequirement) error
	// FilterVisibleResources returns only resources satisfying the visibility rule.
	FilterVisibleResources(ctx context.Context, resourceType string, ids []string,
		visibilityOperations []string, visibilityMatch string) (map[string]PermissionResourceOps, error)
	// FilterVisibleResourcesWithOperations also returns each visible resource's
	// complete registry-backed effective operation set.
	FilterVisibleResourcesWithOperations(ctx context.Context, resourceType string, ids []string,
		visibilityOperations []string, visibilityMatch string) (map[string]PermissionResourceOps, error)

	CreateResources(ctx context.Context, resources []PermissionResource, ops []string) error
	DeleteResources(ctx context.Context, resourceType string, ids []string) error
	UpsertResourceParents(ctx context.Context, resourceType, parentType string, items []PermissionResourceParent) error
	DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error
	GetResourceParents(ctx context.Context, resourceType string, resourceIDs []string) (map[string]PermissionResourceParent, error)
	UpdateResource(ctx context.Context, resource PermissionResource) error
}
