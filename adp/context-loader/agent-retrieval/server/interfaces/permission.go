// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

const (
	PermissionResourceTypeObjectType = "object_type"
	PermissionOperationQueryData     = "query_data"
)

// PermissionResource is one concrete bkn-safe authorization resource.
type PermissionResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// PermissionFilterRequest is bkn-safe's mixed-resource filter contract.
type PermissionFilterRequest struct {
	AccessorID           string               `json:"accessor_id"`
	Resources            []PermissionResource `json:"resources"`
	VisibilityOperations []string             `json:"visibility_operations"`
	CandidateOperations  []string             `json:"candidate_operations"`
}

// PermissionFilterResult is one allowed resource returned by bkn-safe.
type PermissionFilterResult struct {
	ResourceType string   `json:"resource_type"`
	ResourceID   string   `json:"resource_id"`
	Operations   []string `json:"operations"`
}

// PermissionFilterResponse keeps Resources as a pointer so an omitted field is
// distinguishable from the valid empty result used for deny-all and disabled
// accounts.
type PermissionFilterResponse struct {
	Resources *[]PermissionFilterResult `json:"resources"`
}

// PermissionAccess is the outbound bkn-safe resource-filter boundary.
type PermissionAccess interface {
	FilterResources(ctx context.Context, request PermissionFilterRequest) (PermissionFilterResponse, error)
}

// QueryCandidateAuthorizer filters server-generated Object Type candidates
// before context-loader fans out instance queries. It authorizes only the
// primary Object Type; ontology-query remains responsible for every actual data
// dependency.
type QueryCandidateAuthorizer interface {
	FilterObjectTypeIDs(ctx context.Context, knID string, candidateIDs []string) ([]string, error)
}
