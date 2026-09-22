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
	OPERATION_TYPE_VIEW_DETAIL          = "view_detail"
	OPERATION_TYPE_CREATE               = "create"
	OPERATION_TYPE_MODIFY               = "modify"
	OPERATION_TYPE_DELETE               = "delete"
	OPERATION_TYPE_QUERY_DATA           = "query_data"
	OPERATION_TYPE_AUTHORIZE            = "authorize"
	OPERATION_TYPE_EXECUTE              = "execute"
	OPERATION_TYPE_FULL_BUSINESS_ACCESS = "full_business_access"

	// Topic used to update a resource name.
	AUTHORIZATION_RESOURCE_NAME_MODIFY = "authorization.resource.name.modify"
)

var (
	// KN_CREATOR_OPERATIONS asks bkn-safe to atomically install the Community
	// business bundle and the system-derived authorize permission. Create remains
	// a type-level capability.
	KN_CREATOR_OPERATIONS = []string{
		OPERATION_TYPE_FULL_BUSINESS_ACCESS,
		OPERATION_TYPE_AUTHORIZE,
	}
)

// PermissionCheck describes a permission check.
type PermissionCheck struct {
	Accessor   PermissionAccessor `json:"accessor"`
	Resource   PermissionResource `json:"resource"`
	Operations []string           `json:"operation"`
}

// PermissionRequirement is one exact resource-operation authorization
// requirement. Multiple requirements are evaluated atomically by bkn-safe's
// /checks endpoint.
type PermissionRequirement struct {
	Resource  PermissionResource `json:"resource"`
	Operation string             `json:"operation"`
}

type PermissionChecksRequest struct {
	AccessorID string                  `json:"accessor_id"`
	Checks     []PermissionRequirement `json:"checks"`
}

type PermissionCheckResult struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Operation    string `json:"operation"`
	Allowed      bool   `json:"allowed"`
}

type PermissionChecksResponse struct {
	Allowed bool                    `json:"allowed"`
	Results []PermissionCheckResult `json:"results"`
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

// KNChildResourceCandidate ties one persisted child resource to its owning
// knowledge network for restricted navigation visibility checks.
type KNChildResourceCandidate struct {
	KNID       string
	ResourceID string
	Type       string
}

// KNChildResourceID returns the canonical Safe ID for a child resource.
func KNChildResourceID(knID, childID string) string {
	return knID + "/" + childID
}

// KNChildIDFromResourceID extracts a child ID from one network-scoped Safe ID.
func KNChildIDFromResourceID(knID, resourceID string) (string, bool) {
	childID, ok := strings.CutPrefix(resourceID, knID+"/")
	return childID, ok && IsValidAuthorizationID(childID)
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
// Operations determine visibility: a resource must hold all listed operations
// to be returned. When operation projection is enabled, bkn-safe returns the
// complete effective operation set from its authorization catalog.
type PermissionResourcesFilter struct {
	Accessor   PermissionAccessor   `json:"accessor,omitempty"`
	Resources  []PermissionResource `json:"resources,omitempty"`
	Operations []string             `json:"operation,omitempty"`
	// IncludeOperations selects the independent operation-projection axis. False
	// returns visible resources only; true returns each resource's effective ops.
	IncludeOperations bool `json:"include_operations"`
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

type PropertyLevelsRequest struct {
	AccessorID string                      `json:"accessor_id"`
	Items      []PropertyLevelsRequestItem `json:"items"`
}

type PropertyLevelsRequestItem struct {
	ObjectTypeRef string   `json:"object_type_ref"`
	Properties    []string `json:"properties"`
}

type PropertyLevelsResponse struct {
	Entries []PropertyLevelsDecisionEntry `json:"entries"`
}

type PropertyLevelsDecisionEntry struct {
	ObjectTypeRef string                   `json:"object_type_ref"`
	Properties    []PropertyAccessDecision `json:"properties"`
}

// Effective property access levels returned by bkn-safe (#1374). none means
// the property does not exist for the caller; schema and masked mean they may
// know it exists but not read its values as stored.
const (
	PROPERTY_ACCESS_FULL   = "full"
	PROPERTY_ACCESS_MASKED = "masked"
	PROPERTY_ACCESS_SCHEMA = "schema"
	PROPERTY_ACCESS_NONE   = "none"
)

type PropertyAccessDecision struct {
	Name   string `json:"name"`
	Level  string `json:"level"`
	Source string `json:"source"`
}

// RowFilterValue is an exact value selected by a row-filter policy. Keeping
// the value tagged avoids a JSON number being silently widened to float64
// while it crosses the bkn-safe boundary.
type RowFilterValue struct {
	Type    string  `json:"type"`
	String  *string `json:"string,omitempty"`
	Integer *int64  `json:"integer,omitempty"`
	Boolean *bool   `json:"boolean,omitempty"`
}

// RowFilterPredicate is the deliberately small predicate language returned
// by bkn-safe. Query services validate and compile it against their local
// published model before it is ever sent to a data engine.
type RowFilterPredicate struct {
	Kind       string               `json:"kind"`
	Property   string               `json:"property,omitempty"`
	Values     []RowFilterValue     `json:"values,omitempty"`
	Predicates []RowFilterPredicate `json:"predicates,omitempty"`
}

type RowFiltersRequest struct {
	AccessorID     string   `json:"accessor_id"`
	ObjectTypeRefs []string `json:"object_type_refs"`
}

type RowFilterDecisionEntry struct {
	ObjectTypeRef            string             `json:"object_type_ref"`
	Predicate                RowFilterPredicate `json:"predicate"`
	EffectiveRowFilterDigest string             `json:"effective_row_filter_digest"`
}

type RowFiltersResponse struct {
	Entries []RowFilterDecisionEntry `json:"entries"`
}

// PermissionResourceScope describes the storage-planning scope for one accessor.
// RequiresCandidateFilter preserves exact wildcard and deny semantics when the
// visible set cannot be expressed as a finite list of concrete IDs.
type PermissionResourceScope struct {
	Unrestricted            bool     `json:"unrestricted"`
	RequiresCandidateFilter bool     `json:"requires_candidate_filter"`
	ResourceIDs             []string `json:"ids"`
}

//go:generate mockgen -source ../interfaces/permission_access.go -destination ../interfaces/mock/mock_permission_access.go
type PermissionAccess interface {
	CheckPermission(ctx context.Context, check PermissionCheck) (bool, error)
	CheckPermissions(ctx context.Context, request PermissionChecksRequest) (PermissionChecksResponse, error)
	FilterResources(ctx context.Context, filter PermissionResourcesFilter) (map[string]PermissionResourceOps, error)
	ListAccessibleResources(ctx context.Context, accessor PermissionAccessor, resourceType, operation string) (PermissionResourceScope, error)
	ResolvePropertyLevels(ctx context.Context, request PropertyLevelsRequest) (PropertyLevelsResponse, error)
	ResolveRowFilters(ctx context.Context, request RowFiltersRequest) (RowFiltersResponse, error)

	CreateResources(ctx context.Context, policies []PermissionPolicy) error
	DeleteResources(ctx context.Context, resources []PermissionResource) error
	UpsertResourceParents(ctx context.Context, resourceType, parentType string, items []PermissionResourceParent) error
	DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error
}
