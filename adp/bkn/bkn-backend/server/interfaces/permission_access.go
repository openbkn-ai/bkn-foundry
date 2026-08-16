// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

const (
	ADMIN_ACCOUNT_ID   = "266c6a42-6131-4d62-8f39-853e7093701c"
	ADMIN_ACCOUNT_TYPE = "user"

	// Accessor types.
	ACCESSOR_TYPE_USER = "user"
	ACCESSOR_TYPE_APP  = "app"

	// Use * when no resource ID exists during creation.
	RESOURCE_ID_ALL = "*"

	// Resource types.
	RESOURCE_TYPE_KN = "knowledge_network"

	// Resource operation types.
	OPERATION_TYPE_VIEW_DETAIL = "view_detail"
	OPERATION_TYPE_CREATE      = "create"
	OPERATION_TYPE_MODIFY      = "modify"
	OPERATION_TYPE_DELETE      = "delete"
	OPERATION_TYPE_QUERY_DATA  = "query_data"
	OPERATION_TYPE_AUTHORIZE   = "authorize"
	OPERATION_TYPE_TASK_MANAGE = "task_manage"

	// Topic used to update a resource name.
	AUTHORIZATION_RESOURCE_NAME_MODIFY = "authorization.resource.name.modify"
)

var (
	COMMON_OPERATIONS = []string{
		OPERATION_TYPE_VIEW_DETAIL,
		OPERATION_TYPE_CREATE,
		OPERATION_TYPE_MODIFY,
		OPERATION_TYPE_DELETE,
		OPERATION_TYPE_QUERY_DATA,
		OPERATION_TYPE_AUTHORIZE,
		OPERATION_TYPE_TASK_MANAGE,
	}
)

// PermissionCheck describes a permission check.
type PermissionCheck struct {
	Accessor   PermissionAccessor `json:"accessor"`
	Resource   PermissionResource `json:"resource"`
	Operations []string           `json:"operation"`
	Method     string             `json:"method"`
}

// PermissionCheckResult is the result of a permission check.
type PermissionCheckResult struct {
	Result bool `json:"result"`
}

// PermissionAccessor identifies an accessor.
type PermissionAccessor struct {
	Type string `json:"type,omitempty"` // user for a named user, app for an application account
	ID   string `json:"id,omitempty"`   // User ID
}

// PermissionResource identifies a resource.
type PermissionResource struct {
	Type string `json:"type,omitempty"` // Resource type
	ID   string `json:"id,omitempty"`   // Resource ID
	Name string `json:"name,omitempty"` // Resource name
}

// PermissionResourcesFilter is used for filtering and deletion.
//
// Operations and CandidateOperations are independent dimensions.
// Operations determine visibility: a resource must hold all listed operations to be returned.
// CandidateOperations determine which operations are returned to the frontend. When empty,
// they fall back to Operations for backward compatibility.
//
// ISF previously used Operations for both dimensions and returned every allowed operation when
// allow_operation was true. BKN Safe intersects the requested candidate list, so the candidates
// must be explicitly passed to the adapter.
type PermissionResourcesFilter struct {
	Accessor       PermissionAccessor   `json:"accessor,omitempty"`
	Resources      []PermissionResource `json:"resources,omitempty"`
	Operations     []string             `json:"operation,omitempty"`
	AllowOperation bool                 `json:"allow_operation"`
	Method         string               `json:"method,omitempty"`
	// json:"-" keeps this new field out of the ISF request contract.
	CandidateOperations []string `json:"-"`
}

// PermissionPolicy describes a policy to apply.
type PermissionPolicy struct {
	Accessor   PermissionAccessor  `json:"accessor"`
	Resource   PermissionResource  `json:"resource"`
	Operations PermissionPolicyOps `json:"operation"`
	Condition  string              `json:"condition"`
	ExpiresAt  string              `json:"expires_at,omitempty"`
}

type PermissionPolicyOps struct {
	Allow []PermissionOperation `json:"allow"`
	Deny  []PermissionOperation `json:"deny"`
}

type PermissionOperation struct {
	Operation string `json:"id"`
}

type PermissionResourceOps struct {
	ResourceID string   `json:"id"`
	Operations []string `json:"operation,omitempty"`
}

//go:generate mockgen -source ../interfaces/permission_access.go -destination ../interfaces/mock/mock_permission_access.go
type PermissionAccess interface {
	CheckPermission(ctx context.Context, check PermissionCheck) (bool, error)
	FilterResources(ctx context.Context, filter PermissionResourcesFilter) (map[string]PermissionResourceOps, error)

	CreateResources(ctx context.Context, policies []PermissionPolicy) error
	DeleteResources(ctx context.Context, resources []PermissionResource) error
}
