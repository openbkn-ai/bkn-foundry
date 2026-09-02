// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"strings"
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
	RESOURCE_TYPE_KN            = "knowledge_network"
	RESOURCE_TYPE_CONCEPT_GROUP = "concept_group"
	RESOURCE_TYPE_OBJECT_TYPE   = "object_type"
	RESOURCE_TYPE_RELATION_TYPE = "relation_type"
	RESOURCE_TYPE_ACTION_TYPE   = "action_type"
	RESOURCE_TYPE_METRIC        = "metric"
	RESOURCE_TYPE_RISK_TYPE     = "risk_type"

	// Resource operation types.
	OPERATION_TYPE_VIEW_DETAIL = "view_detail"
	OPERATION_TYPE_CREATE      = "create"
	OPERATION_TYPE_MODIFY      = "modify"
	OPERATION_TYPE_DELETE      = "delete"
	OPERATION_TYPE_QUERY_DATA  = "query_data"
	OPERATION_TYPE_AUTHORIZE   = "authorize"
	OPERATION_TYPE_TASK_MANAGE = "task_manage"
	OPERATION_TYPE_EXECUTE     = "execute"

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

// PermissionResourceParent records one concrete child-to-parent relationship.
type PermissionResourceParent struct {
	ResourceID string `json:"resource_id"`
	ParentID   string `json:"parent_id"`
}

// KNChildResourceID returns the canonical Safe ID for a child resource.
func KNChildResourceID(knID, childID string) string {
	return knID + "/" + childID
}

// KNChildPermissionResource builds the canonical Safe reference for a KN child.
func KNChildPermissionResource(resourceType, knID, childID string) PermissionResource {
	return PermissionResource{Type: resourceType, ID: KNChildResourceID(knID, childID)}
}

// KNChildResourceParents builds concrete child-to-KN parent rows for bkn-safe.
func KNChildResourceParents(knID string, childIDs []string) []PermissionResourceParent {
	items := make([]PermissionResourceParent, 0, len(childIDs))
	for _, childID := range childIDs {
		items = append(items, PermissionResourceParent{
			ResourceID: KNChildResourceID(knID, childID),
			ParentID:   knID,
		})
	}
	return items
}

// KNChildResourceIDs returns canonical Safe IDs for concrete KN children.
func KNChildResourceIDs(knID string, childIDs []string) []string {
	ids := make([]string, 0, len(childIDs))
	for _, childID := range childIDs {
		ids = append(ids, KNChildResourceID(knID, childID))
	}
	return ids
}

// IsValidAuthorizationID reports whether an ID is safe to embed in a canonical
// authorization resource ID. Empty IDs and wildcard/path separators are invalid.
func IsValidAuthorizationID(id string) bool {
	return id != "" && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "/*")
}

// PermissionResourcesFilter is used for filtering and deletion.
//
// Operations and CandidateOperations are independent dimensions.
// Operations determine visibility: a resource must hold all listed operations to be returned.
// CandidateOperations determine which operations are returned to the frontend. When empty,
// they fall back to Operations for backward compatibility.
//
// BKN Safe intersects the requested candidate list, so callers must pass the
// candidates explicitly when they differ from the visibility operations.
type PermissionResourcesFilter struct {
	Accessor       PermissionAccessor   `json:"accessor,omitempty"`
	Resources      []PermissionResource `json:"resources,omitempty"`
	Operations     []string             `json:"operation,omitempty"`
	AllowOperation bool                 `json:"allow_operation"`
	// CandidateOperations is an adapter-only projection hint.
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
	UpsertResourceParents(ctx context.Context, resourceType, parentType string, items []PermissionResourceParent) error
	DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error
}
