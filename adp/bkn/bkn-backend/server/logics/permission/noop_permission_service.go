// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

// NoopPermissionService skips all permission checks.
type NoopPermissionService struct {
	appSetting *common.AppSetting
}

func NewNoopPermissionService(appSetting *common.AppSetting) interfaces.PermissionService {
	return &NoopPermissionService{appSetting: appSetting}
}

func (n *NoopPermissionService) CheckPermission(ctx context.Context, resource interfaces.PermissionResource, ops []string) error {
	return nil // Always allow; do not inspect accountInfo.
}

func (n *NoopPermissionService) CreateResources(ctx context.Context, resources []interfaces.PermissionResource, ops []string) error {
	return nil // Silently skip.
}

func (n *NoopPermissionService) DeleteResources(ctx context.Context, resourceType string, ids []string) error {
	return nil // Silently skip.
}

func (n *NoopPermissionService) FilterResources(ctx context.Context, resourceType string, ids []string,
	ops []string, allowOperation bool, fullOps []string) (map[string]interfaces.PermissionResourceOps, error) {
	// Return all resources without filtering.
	result := make(map[string]interfaces.PermissionResourceOps)
	for _, id := range ids {
		result[id] = interfaces.PermissionResourceOps{
			ResourceID: id,
			Operations: fullOps,
		}
	}
	return result, nil
}

func (n *NoopPermissionService) UpdateResource(ctx context.Context, resource interfaces.PermissionResource) error {
	return nil // Silently skip.
}
