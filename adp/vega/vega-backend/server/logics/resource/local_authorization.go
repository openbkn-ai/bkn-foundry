// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

func composeVegaBaseDecision(operation string, resourceDecision, catalogDecision *interfaces.PermissionOperationDecision) interfaces.PermissionOperationDecision {
	if resourceDecision != nil && resourceDecision.Decision == interfaces.PermissionDecisionDeny {
		return copyDecisionForOperation(*resourceDecision, operation)
	}
	if resourceDecision != nil && resourceDecision.Decision == interfaces.PermissionDecisionAllow &&
		resourceDecision.Basis != interfaces.PermissionBasisWildcard {
		return copyDecisionForOperation(*resourceDecision, operation)
	}
	if catalogDecision != nil {
		switch catalogDecision.Decision {
		case interfaces.PermissionDecisionDeny, interfaces.PermissionDecisionAllow:
			decision := copyDecisionForOperation(*catalogDecision, operation)
			decision.Basis = interfaces.PermissionBasisInherited
			return decision
		}
	}
	if resourceDecision != nil && resourceDecision.Decision == interfaces.PermissionDecisionAllow &&
		resourceDecision.Basis == interfaces.PermissionBasisWildcard {
		return copyDecisionForOperation(*resourceDecision, operation)
	}
	return interfaces.PermissionOperationDecision{
		Operation: operation, Decision: interfaces.PermissionDecisionDeny, Basis: interfaces.PermissionBasisDefault,
	}
}

func copyDecisionForOperation(decision interfaces.PermissionOperationDecision, operation string) interfaces.PermissionOperationDecision {
	decision.Operation = operation
	decision.Requires = append([]string(nil), decision.Requires...)
	return decision
}

func applyVegaRequirements(operation string, base interfaces.PermissionOperationDecision,
	resolve func(string) interfaces.PermissionOperationDecision) interfaces.PermissionOperationDecision {
	if !base.Allowed() {
		return base
	}
	for _, requirement := range uniqueOperations(base.Requires) {
		required := resolve(requirement)
		if required.Allowed() {
			continue
		}
		return interfaces.PermissionOperationDecision{
			Operation: operation, Decision: interfaces.PermissionDecisionDeny,
			Basis: interfaces.PermissionBasisRequires, Requires: append([]string(nil), base.Requires...),
			DeniedRequirement: requirement, RequirementBasis: required.Basis,
		}
	}
	return base
}

func (rs *resourceService) localOperationDecision(ctx context.Context, resourceID, catalogID,
	operation string) (interfaces.PermissionOperationDecision, error) {
	local, ok := rs.ps.(interfaces.LocalPermissionService)
	if !ok {
		return interfaces.PermissionOperationDecision{}, interfaces.ErrLocalPermissionUnsupported
	}
	resourceCache := map[string]interfaces.PermissionOperationDecision{}
	catalogCache := map[string]interfaces.PermissionOperationDecision{}
	resolveBase := func(op string) (interfaces.PermissionOperationDecision, error) {
		var resourceDecision *interfaces.PermissionOperationDecision
		if resourceOwnOperations[op] {
			decision, exists := resourceCache[op]
			if !exists {
				var err error
				decision, err = local.LocalDecision(ctx, interfaces.PermissionResource{
					Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: resourceID,
				}, op)
				if err != nil {
					return interfaces.PermissionOperationDecision{}, err
				}
				resourceCache[op] = decision
			}
			resourceDecision = &decision
			if decision.Decision == interfaces.PermissionDecisionDeny ||
				(decision.Decision == interfaces.PermissionDecisionAllow &&
					decision.Basis != interfaces.PermissionBasisWildcard) {
				return copyDecisionForOperation(decision, op), nil
			}
		}
		var catalogDecision *interfaces.PermissionOperationDecision
		if catalogOp, mapped := resourceOpOnCatalog[op]; mapped && catalogID != "" {
			decision, exists := catalogCache[catalogOp]
			if !exists {
				var err error
				decision, err = local.LocalDecision(ctx, interfaces.PermissionResource{
					Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: catalogID,
				}, catalogOp)
				if err != nil {
					return interfaces.PermissionOperationDecision{}, err
				}
				catalogCache[catalogOp] = decision
			}
			catalogDecision = &decision
		}
		return composeVegaBaseDecision(op, resourceDecision, catalogDecision), nil
	}

	base, err := resolveBase(operation)
	if err != nil || !base.Allowed() {
		return base, err
	}
	var requirementErr error
	final := applyVegaRequirements(operation, base, func(requirement string) interfaces.PermissionOperationDecision {
		decision, err := resolveBase(requirement)
		if err != nil {
			requirementErr = err
			return interfaces.PermissionOperationDecision{}
		}
		return decision
	})
	if requirementErr != nil {
		return interfaces.PermissionOperationDecision{}, requirementErr
	}
	return final, nil
}

func (rs *resourceService) localFilterResourcePermissions(ctx context.Context, ids, visibilityOperations []string,
	allowOperation bool) (map[string]interfaces.PermissionResourceOps, error) {
	local, ok := rs.ps.(interfaces.LocalPermissionService)
	if !ok {
		return nil, interfaces.ErrLocalPermissionUnsupported
	}
	ids = uniqueOperations(ids)
	if len(ids) == 0 {
		return map[string]interfaces.PermissionResourceOps{}, nil
	}
	refs, err := rs.ra.GetPermissionRefsByIDs(ctx, ids)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	catalogOf := make(map[string]string, len(refs))
	catalogIDs := make([]string, 0, len(refs))
	seenCatalog := map[string]bool{}
	for _, ref := range refs {
		catalogOf[ref.ResourceID] = ref.CatalogID
		if ref.CatalogID != "" && !seenCatalog[ref.CatalogID] {
			seenCatalog[ref.CatalogID] = true
			catalogIDs = append(catalogIDs, ref.CatalogID)
		}
	}

	candidateOperations := uniqueOperations(append(append([]string(nil), visibilityOperations...), interfaces.COMMON_OPERATIONS...))
	resourceOperations, catalogOperations := splitVegaOperations(candidateOperations)
	resourceDecisions, err := local.LocalResourceDecisions(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ids, resourceOperations)
	if err != nil {
		return nil, err
	}
	catalogDecisions, err := local.LocalResourceDecisions(ctx, interfaces.AUTH_RESOURCE_TYPE_CATALOG, catalogIDs, catalogOperations)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interfaces.PermissionResourceOps, len(ids))
	for _, id := range ids {
		catalogID := catalogOf[id]
		final := make(map[string]interfaces.PermissionOperationDecision, len(candidateOperations))
		resolve := func(operation string) interfaces.PermissionOperationDecision {
			resourceDecision := decisionPointer(resourceDecisions[id], operation, resourceOwnOperations[operation])
			catalogOp, mapped := resourceOpOnCatalog[operation]
			catalogDecision := decisionPointer(catalogDecisions[catalogID], catalogOp, mapped && catalogID != "")
			return composeVegaBaseDecision(operation, resourceDecision, catalogDecision)
		}
		for _, operation := range candidateOperations {
			base := resolve(operation)
			final[operation] = applyVegaRequirements(operation, base, resolve)
		}
		if !resourceVisible(final, visibilityOperations, allowOperation) {
			continue
		}
		entry := interfaces.PermissionResourceOps{ResourceID: id, Operations: []string{}}
		for _, operation := range interfaces.COMMON_OPERATIONS {
			if final[operation].Allowed() {
				entry.Operations = append(entry.Operations, operation)
			}
		}
		result[id] = entry
	}
	return result, nil
}

func splitVegaOperations(operations []string) (resourceOperations, catalogOperations []string) {
	seenResource, seenCatalog := map[string]bool{}, map[string]bool{}
	for _, operation := range operations {
		if resourceOwnOperations[operation] && !seenResource[operation] {
			seenResource[operation] = true
			resourceOperations = append(resourceOperations, operation)
		}
		if catalogOperation, ok := resourceOpOnCatalog[operation]; ok && !seenCatalog[catalogOperation] {
			seenCatalog[catalogOperation] = true
			catalogOperations = append(catalogOperations, catalogOperation)
		}
	}
	return resourceOperations, catalogOperations
}

func decisionPointer(decisions map[string]interfaces.PermissionOperationDecision, operation string,
	wanted bool) *interfaces.PermissionOperationDecision {
	if !wanted {
		return nil
	}
	decision, ok := decisions[operation]
	if !ok {
		return nil
	}
	return &decision
}

func resourceVisible(decisions map[string]interfaces.PermissionOperationDecision, operations []string,
	allowOperation bool) bool {
	operations = uniqueOperations(operations)
	if len(operations) == 0 {
		return true
	}
	if allowOperation {
		for _, operation := range operations {
			if decisions[operation].Allowed() {
				return true
			}
		}
		return false
	}
	for _, operation := range operations {
		if !decisions[operation].Allowed() {
			return false
		}
	}
	return true
}

func uniqueOperations(operations []string) []string {
	result := make([]string, 0, len(operations))
	seen := make(map[string]bool, len(operations))
	for _, operation := range operations {
		if operation == "" || seen[operation] {
			continue
		}
		seen[operation] = true
		result = append(result, operation)
	}
	return result
}

func permissionDeniedForDecision(ctx context.Context, operation string,
	decision interfaces.PermissionOperationDecision) error {
	details := fmt.Sprintf("Access denied: insufficient permissions for[%v]", operation)
	if decision.Basis == interfaces.PermissionBasisRequires {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).WithErrorDetails(map[string]any{
			"message":            details,
			"basis":              decision.Basis,
			"denied_requirement": decision.DeniedRequirement,
			"requirement_basis":  decision.RequirementBasis,
		})
	}
	return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).WithErrorDetails(details)
}
