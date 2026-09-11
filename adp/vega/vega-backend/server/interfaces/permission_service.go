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
	FilterResources(ctx context.Context, resourceType string, ids []string,
		ops []string, allowOperation bool, fullOps []string) (map[string]PermissionResourceOps, error)

	CreateResources(ctx context.Context, resources []PermissionResource, ops []string) error
	DeleteResources(ctx context.Context, resourceType string, ids []string) error
	UpdateResource(ctx context.Context, resource PermissionResource) error
}

// LocalPermissionService exposes bkn-safe's non-final local decision only to
// the Resource service, which owns the trusted Resource-to-Catalog relation.
// Other business services continue to use PermissionService's effective APIs.
type LocalPermissionService interface {
	LocalDecision(ctx context.Context, resource PermissionResource, op string) (PermissionOperationDecision, error)
	LocalResourceDecisions(ctx context.Context, resourceType string, ids, ops []string) (map[string]map[string]PermissionOperationDecision, error)
}
