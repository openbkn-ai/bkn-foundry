// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"ontology-query/common"
	permissionaccess "ontology-query/drivenadapters/permission"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

type permissionService struct {
	access interfaces.PermissionAccess
}

func NewPermissionService(appSetting *common.AppSetting) interfaces.PermissionService {
	return &permissionService{access: permissionaccess.NewPermissionAccess(appSetting)}
}

func (ps *permissionService) FilterQueryData(ctx context.Context,
	resources []interfaces.PermissionResource) ([]interfaces.PermissionResource, error) {
	account, ok := accountFromContext(ctx)
	if !ok {
		return nil, permissionDenied(ctx, "request subject is missing")
	}

	resources, err := normalizeResources(resources)
	if err != nil {
		return nil, permissionDenied(ctx, err.Error())
	}
	if len(resources) == 0 {
		return nil, permissionDenied(ctx, "query dependencies are empty")
	}
	if ps == nil || ps.access == nil {
		return nil, permissionUnavailable(ctx, fmt.Errorf("permission access is not configured"))
	}

	response, err := ps.access.FilterResources(ctx, interfaces.PermissionFilterRequest{
		AccessorID:           account.ID,
		Resources:            resources,
		VisibilityOperations: []string{interfaces.PermissionOperationQueryData},
		CandidateOperations:  []string{interfaces.PermissionOperationQueryData},
	})
	if err != nil {
		return nil, permissionUnavailable(ctx, err)
	}

	allowed := make(map[string]interfaces.PermissionResource, len(response.Resources))
	requested := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		requested[resourceKey(resource.Type, resource.ID)] = struct{}{}
	}
	for _, resource := range response.Resources {
		key := resourceKey(resource.ResourceType, resource.ResourceID)
		if _, exists := requested[key]; !exists || !contains(resource.Operations, interfaces.PermissionOperationQueryData) {
			continue
		}
		allowed[key] = interfaces.PermissionResource{Type: resource.ResourceType, ID: resource.ResourceID}
	}
	result := make([]interfaces.PermissionResource, 0, len(allowed))
	for _, resource := range resources {
		if _, ok := allowed[resourceKey(resource.Type, resource.ID)]; ok {
			result = append(result, resource)
		}
	}
	return result, nil
}

func (ps *permissionService) RequireQueryData(ctx context.Context, resources []interfaces.PermissionResource) error {
	normalized, err := normalizeResources(resources)
	if err != nil {
		return permissionDenied(ctx, err.Error())
	}
	allowed, err := ps.FilterQueryData(ctx, normalized)
	if err != nil {
		return err
	}
	if len(allowed) != len(normalized) {
		return permissionDenied(ctx, "query_data was not granted for every required resource")
	}
	return nil
}

func (ps *permissionService) RequirePermissions(ctx context.Context,
	requirements []interfaces.PermissionRequirement) error {
	account, ok := accountFromContext(ctx)
	if !ok {
		return permissionDenied(ctx, "request subject is missing")
	}

	normalized, err := normalizeRequirements(requirements)
	if err != nil {
		return permissionDenied(ctx, err.Error())
	}
	if len(normalized) == 0 {
		return permissionDenied(ctx, "permission requirements are empty")
	}
	if ps == nil || ps.access == nil {
		return permissionUnavailable(ctx, fmt.Errorf("permission access is not configured"))
	}

	resources := make([]interfaces.PermissionResource, 0, len(normalized))
	operations := make([]string, 0, len(normalized))
	seenOperations := make(map[string]struct{}, len(normalized))
	for _, requirement := range normalized {
		resources = append(resources, interfaces.PermissionResource{
			Type: requirement.ResourceType,
			ID:   requirement.ResourceID,
		})
		if _, exists := seenOperations[requirement.Operation]; !exists {
			seenOperations[requirement.Operation] = struct{}{}
			operations = append(operations, requirement.Operation)
		}
	}

	response, err := ps.access.FilterResources(ctx, interfaces.PermissionFilterRequest{
		AccessorID:          account.ID,
		Resources:           resources,
		CandidateOperations: operations,
	})
	if err != nil {
		return permissionUnavailable(ctx, err)
	}

	allowed := make(map[string]map[string]struct{}, len(response.Resources))
	requested := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		requested[resourceKey(resource.Type, resource.ID)] = struct{}{}
	}
	for _, result := range response.Resources {
		key := resourceKey(strings.TrimSpace(result.ResourceType), strings.TrimSpace(result.ResourceID))
		if _, exists := requested[key]; !exists {
			continue
		}
		if _, exists := allowed[key]; !exists {
			allowed[key] = make(map[string]struct{}, len(result.Operations))
		}
		for _, operation := range result.Operations {
			allowed[key][strings.TrimSpace(operation)] = struct{}{}
		}
	}
	for _, requirement := range normalized {
		operationsForResource := allowed[resourceKey(requirement.ResourceType, requirement.ResourceID)]
		if _, exists := operationsForResource[requirement.Operation]; !exists {
			return permissionDenied(ctx, fmt.Sprintf("%s was not granted for %s:%s",
				requirement.Operation, requirement.ResourceType, requirement.ResourceID))
		}
	}
	return nil
}

func accountFromContext(ctx context.Context) (interfaces.AccountInfo, bool) {
	if ctx == nil {
		return interfaces.AccountInfo{}, false
	}
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	account.ID = strings.TrimSpace(account.ID)
	account.Type = strings.TrimSpace(account.Type)
	if !ok || account.ID == "" || !supportedSubjectType(account.Type) {
		return interfaces.AccountInfo{}, false
	}
	return account, true
}

func supportedSubjectType(accountType string) bool {
	switch accountType {
	case "user", "realname", "app":
		return true
	default:
		return false
	}
}

func normalizeResources(resources []interfaces.PermissionResource) ([]interfaces.PermissionResource, error) {
	normalized := make([]interfaces.PermissionResource, 0, len(resources))
	seen := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		resource.Type = strings.TrimSpace(resource.Type)
		resource.ID = strings.TrimSpace(resource.ID)
		if resource.Type == "" || resource.ID == "" {
			return nil, fmt.Errorf("permission resource type and id are required")
		}
		if strings.Contains(resource.ID, "*") {
			return nil, fmt.Errorf("type-wide resources cannot authorize data queries")
		}
		key := resourceKey(resource.Type, resource.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, resource)
	}
	return normalized, nil
}

func normalizeRequirements(requirements []interfaces.PermissionRequirement) ([]interfaces.PermissionRequirement, error) {
	normalized := make([]interfaces.PermissionRequirement, 0, len(requirements))
	seen := make(map[string]struct{}, len(requirements))
	for _, requirement := range requirements {
		requirement.ResourceType = strings.TrimSpace(requirement.ResourceType)
		requirement.ResourceID = strings.TrimSpace(requirement.ResourceID)
		requirement.Operation = strings.TrimSpace(requirement.Operation)
		if requirement.ResourceType == "" || requirement.ResourceID == "" || requirement.Operation == "" {
			return nil, fmt.Errorf("permission resource type, id, and operation are required")
		}
		if strings.Contains(requirement.ResourceID, "*") {
			return nil, fmt.Errorf("type-wide resources cannot authorize action execution")
		}
		key := resourceKey(requirement.ResourceType, requirement.ResourceID) + "\x00" + requirement.Operation
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, requirement)
	}
	return normalized, nil
}

func resourceKey(resourceType, resourceID string) string {
	return resourceType + "\x00" + resourceID
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func permissionDenied(ctx context.Context, detail string) error {
	httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).WithErrorDetails(detail)
	otellog.LogError(ctx, "Query permission denied", httpErr)
	return httpErr
}

func permissionUnavailable(ctx context.Context, err error) error {
	httpErr := rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
		oerrors.OntologyQuery_InternalError_CheckPermissionFailed).WithErrorDetails(err.Error())
	otellog.LogError(ctx, "Query permission check failed", httpErr)
	return httpErr
}
