// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

const (
	PermissionResourceTypeKnowledgeNetwork = "knowledge_network"
	PermissionResourceTypeObjectType       = "object_type"
	PermissionResourceTypeRelationType     = "relation_type"
	PermissionResourceTypeActionType       = "action_type"
	PermissionResourceTypeMetric           = "metric"
	PermissionResourceTypeToolBox          = "tool_box"
	PermissionResourceTypeMCP              = "mcp"

	PermissionOperationQueryData = "query_data"
	PermissionOperationExecute   = "execute"
)

// PermissionResource identifies one concrete authorization resource.
type PermissionResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// PermissionFilterRequest is the bkn-safe mixed-resource filter request.
type PermissionFilterRequest struct {
	AccessorID           string               `json:"accessor_id"`
	Resources            []PermissionResource `json:"resources"`
	VisibilityOperations []string             `json:"visibility_operations"`
	CandidateOperations  []string             `json:"candidate_operations"`
}

// PermissionFilterResult describes one allowed resource returned by bkn-safe.
type PermissionFilterResult struct {
	ResourceType string   `json:"resource_type"`
	ResourceID   string   `json:"resource_id"`
	Operations   []string `json:"operations"`
}

// PermissionFilterResponse is the bkn-safe mixed-resource filter response.
type PermissionFilterResponse struct {
	Resources []PermissionFilterResult `json:"resources"`
}

// PermissionRequirement is an immutable authorization fact captured for an
// action execution and checked again immediately before every external call.
type PermissionRequirement struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Operation    string `json:"operation"`
}

//go:generate mockgen -source ../interfaces/permission_service.go -destination ../interfaces/mock/mock_permission_service.go
type PermissionAccess interface {
	FilterResources(ctx context.Context, request PermissionFilterRequest) (PermissionFilterResponse, error)
}

type PermissionService interface {
	FilterQueryData(ctx context.Context, resources []PermissionResource) ([]PermissionResource, error)
	RequireQueryData(ctx context.Context, resources []PermissionResource) error
	RequirePermissions(ctx context.Context, requirements []PermissionRequirement) error
}

// ActionExecutionPermissionService checks the heterogeneous requirements of
// one action execution as the account stored in the request context.
type ActionExecutionPermissionService interface {
	RequirePermissions(ctx context.Context, requirements []PermissionRequirement) error
}

// KNChildPermissionResource builds the canonical Safe reference for a KN child.
func KNChildPermissionResource(resourceType, knID, childID string) PermissionResource {
	return PermissionResource{Type: resourceType, ID: knID + "/" + childID}
}
