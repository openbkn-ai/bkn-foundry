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

type unrestrictedPropertyAccessService struct{}

const (
	maxPropertyLevelObjectsPerCall    = 100
	maxPropertyLevelPropertiesPerItem = 200
	maxPropertyLevelPropertiesPerCall = 1000
)

func NewPermissionService(appSetting *common.AppSetting) interfaces.PermissionService {
	return &permissionService{access: permissionaccess.NewPermissionAccess(appSetting)}
}

func NewPropertyAccessService(appSetting *common.AppSetting) interfaces.PropertyAccessService {
	if !common.GetAuthEnabled() {
		return unrestrictedPropertyAccessService{}
	}
	return &permissionService{access: permissionaccess.NewPermissionAccess(appSetting)}
}

func (unrestrictedPropertyAccessService) ResolvePropertyLevels(_ context.Context,
	items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsDecisionEntry, error) {
	entries := make([]interfaces.PropertyLevelsDecisionEntry, 0, len(items))
	for _, item := range items {
		entry := interfaces.PropertyLevelsDecisionEntry{ObjectTypeRef: item.ObjectTypeRef}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, interfaces.PropertyAccessDecision{
				Name: name, Level: interfaces.PropertyAccessFull, Source: "authentication_disabled",
			})
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ResolvePropertyLevels splits oversized caller plans without weakening the
// limits enforced by bkn-safe. Chunks for the same object type are never sent
// in one request because the server rejects duplicate object_type_ref values.
func (ps *permissionService) ResolvePropertyLevels(ctx context.Context,
	items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsDecisionEntry, error) {
	account, ok := accountFromContext(ctx)
	if !ok {
		return nil, permissionDenied(ctx, "request subject is missing")
	}
	if ps == nil || ps.access == nil {
		return nil, permissionUnavailable(ctx, fmt.Errorf("permission access is not configured"))
	}

	chunks, err := propertyLevelChunks(items)
	if err != nil {
		return nil, permissionDenied(ctx, err.Error())
	}
	result := make([]interfaces.PropertyLevelsDecisionEntry, 0, len(items))
	resultIndex := make(map[string]int, len(items))
	for _, item := range items {
		resultIndex[item.ObjectTypeRef] = len(result)
		result = append(result, interfaces.PropertyLevelsDecisionEntry{ObjectTypeRef: item.ObjectTypeRef})
	}

	for _, batch := range propertyLevelBatches(chunks) {
		response, err := ps.access.ResolvePropertyLevels(ctx, interfaces.PropertyLevelsRequest{
			AccessorID: account.ID,
			Items:      batch,
		})
		if err != nil {
			return nil, permissionUnavailable(ctx, err)
		}
		if err := validatePropertyLevelResponse(batch, response.Entries); err != nil {
			return nil, permissionUnavailable(ctx, err)
		}
		for _, entry := range response.Entries {
			index := resultIndex[entry.ObjectTypeRef]
			result[index].Properties = append(result[index].Properties, entry.Properties...)
		}
	}
	return result, nil
}

func propertyLevelChunks(items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsRequestItem, error) {
	chunks := make([]interfaces.PropertyLevelsRequestItem, 0, len(items))
	seenObjects := make(map[string]struct{}, len(items))
	for _, item := range items {
		item.ObjectTypeRef = strings.TrimSpace(item.ObjectTypeRef)
		if item.ObjectTypeRef == "" {
			return nil, fmt.Errorf("object type reference is required")
		}
		if _, exists := seenObjects[item.ObjectTypeRef]; exists {
			return nil, fmt.Errorf("duplicate object type reference")
		}
		seenObjects[item.ObjectTypeRef] = struct{}{}
		properties := uniqueNonemptyStrings(item.Properties)
		if len(properties) == 0 {
			return nil, fmt.Errorf("at least one property is required")
		}
		for start := 0; start < len(properties); start += maxPropertyLevelPropertiesPerItem {
			end := min(start+maxPropertyLevelPropertiesPerItem, len(properties))
			chunks = append(chunks, interfaces.PropertyLevelsRequestItem{
				ObjectTypeRef: item.ObjectTypeRef,
				Properties:    properties[start:end],
			})
		}
	}
	return chunks, nil
}

func propertyLevelBatches(chunks []interfaces.PropertyLevelsRequestItem) [][]interfaces.PropertyLevelsRequestItem {
	var batches [][]interfaces.PropertyLevelsRequestItem
	var batch []interfaces.PropertyLevelsRequestItem
	propertyCount := 0
	seenObjects := map[string]struct{}{}
	flush := func() {
		if len(batch) > 0 {
			batches = append(batches, batch)
		}
		batch = nil
		propertyCount = 0
		seenObjects = map[string]struct{}{}
	}
	for _, chunk := range chunks {
		_, duplicate := seenObjects[chunk.ObjectTypeRef]
		if duplicate || len(batch) == maxPropertyLevelObjectsPerCall ||
			propertyCount+len(chunk.Properties) > maxPropertyLevelPropertiesPerCall {
			flush()
		}
		batch = append(batch, chunk)
		propertyCount += len(chunk.Properties)
		seenObjects[chunk.ObjectTypeRef] = struct{}{}
	}
	flush()
	return batches
}

func validatePropertyLevelResponse(request []interfaces.PropertyLevelsRequestItem,
	response []interfaces.PropertyLevelsDecisionEntry) error {
	if len(request) != len(response) {
		return fmt.Errorf("property-level response entry count mismatch")
	}
	for index, item := range request {
		entry := response[index]
		if entry.ObjectTypeRef != item.ObjectTypeRef || len(entry.Properties) != len(item.Properties) {
			return fmt.Errorf("property-level response shape mismatch")
		}
		for propertyIndex, name := range item.Properties {
			decision := entry.Properties[propertyIndex]
			if decision.Name != name || !decision.Level.Valid() {
				return fmt.Errorf("property-level response decision mismatch")
			}
		}
	}
	return nil
}

func uniqueNonemptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
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
