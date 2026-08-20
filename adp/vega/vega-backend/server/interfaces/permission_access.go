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
	// Visitor type
	ACCESSOR_TYPE_USER = "user"
	ACCESSOR_TYPE_APP  = "app"

	// When creating, there is no resource id, which is indicated by *
	RESOURCE_ID_ALL = "*"

	// Resource type
	AUTH_RESOURCE_TYPE_CATALOG        = "catalog"
	AUTH_RESOURCE_TYPE_RESOURCE       = "resource"
	AUTH_RESOURCE_TYPE_CONNECTOR_TYPE = "connector_type"

	// Internal resource type: The system's internal catalog and its affiliated resources are registered as independent types.
	// catalog of business roles :*/resource:* Wildcard authorization does not match, only visible to the super administrator (* wildcard)
	AUTH_RESOURCE_TYPE_INTERNAL_CATALOG  = "internal_catalog"
	AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE = "internal_resource"

	// Resource operation type
	OPERATION_TYPE_VIEW_DETAIL = "view_detail"
	OPERATION_TYPE_CREATE      = "create"
	OPERATION_TYPE_MODIFY      = "modify"
	OPERATION_TYPE_DELETE      = "delete"
	OPERATION_TYPE_AUTHORIZE   = "authorize"
	OPERATION_TYPE_TASK_MANAGE = "task_manage"

	// OPERATION_TYPE_QUERY_DATA is "the data that can be retrieved from this table", and view_detail (only looking at the structure)
	// Separate. OPERATION_TYPE_RESOURCE_MANAGE is the "data Table under the Management Directory" on the data directory.
	// Both are in the permission vocabulary (#801), and a constant is added here for determination.
	OPERATION_TYPE_QUERY_DATA      = "query_data"
	OPERATION_TYPE_RESOURCE_MANAGE = "resource_manage"

	// Update the topic of the resource name
	AUTHORIZATION_RESOURCE_NAME_MODIFY = "authorization.resource.name.modify"
)

var (
	COMMON_OPERATIONS = []string{
		OPERATION_TYPE_VIEW_DETAIL,
		OPERATION_TYPE_CREATE,
		OPERATION_TYPE_MODIFY,
		OPERATION_TYPE_DELETE,
		OPERATION_TYPE_AUTHORIZE,
		OPERATION_TYPE_TASK_MANAGE,
	}
)

// AuthorizedScope answers "which objects of this type may I act on", in the two
// shapes a listing query can consume: an explicit allow-list, or "everything
// except these".
//
// Two shapes rather than one boolean, because a type-wide grant is written
// against the business type alone. `resource:*` says nothing about
// `internal_resource`, so reporting its holder as "sees everything" hands the
// platform's own tables to any business role — which is how the build and
// semantic task listings leaked internal-catalog tasks (#472).
type AuthorizedScope struct {
	// All reports a type-wide grant on the business type. IDs is then empty and
	// Excluded carries the internal objects the grant does not reach.
	All bool
	// IDs is the explicit allow-list, meaningful only when All is false. Empty
	// there means the caller sees nothing — which is why this cannot collapse
	// into a plain slice: "sees everything" and "sees nothing" would look alike.
	IDs []string
	// Excluded is meaningful only when All is true.
	Excluded []string
}

// Empty reports that the scope admits nothing, so a listing may answer with an
// empty page without querying at all.
func (s AuthorizedScope) Empty() bool { return !s.All && len(s.IDs) == 0 }

// Unfiltered reports that the scope admits everything, so a listing needs no
// visibility predicate.
func (s AuthorizedScope) Unfiltered() bool { return s.All && len(s.Excluded) == 0 }

// Allows reports whether one id falls inside the scope.
func (s AuthorizedScope) Allows(id string) bool {
	if s.All {
		for _, excluded := range s.Excluded {
			if excluded == id {
				return false
			}
		}
		return true
	}
	for _, allowed := range s.IDs {
		if allowed == id {
			return true
		}
	}
	return false
}

// Check permissions
type PermissionCheck struct {
	Accessor   PermissionAccessor `json:"accessor"`
	Resource   PermissionResource `json:"resource"`
	Operations []string           `json:"operation"`
	Method     string             `json:"method"`
}

// Check the permission result
type PermissionCheckResult struct {
	Result bool `json:"result"`
}

// Visitor Information
type PermissionAccessor struct {
	Type string `json:"type,omitempty"` // Separate user: real name, app: application account
	ID   string `json:"id,omitempty"`   // User ID
}

// Resource information
type PermissionResource struct {
	Type string `json:"type,omitempty"` // Resource type
	ID   string `json:"id,omitempty"`   // Resource ID
	Name string `json:"name,omitempty"` // Resource name
}

// Filter/Delete
type PermissionResourcesFilter struct {
	Accessor       PermissionAccessor   `json:"accessor,omitempty"`
	Resources      []PermissionResource `json:"resources,omitempty"`
	Operations     []string             `json:"operation,omitempty"`
	AllowOperation bool                 `json:"allow_operation"`
	Method         string               `json:"method,omitempty"`
}

// Set permissions
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
