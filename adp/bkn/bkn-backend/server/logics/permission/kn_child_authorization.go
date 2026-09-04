// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/interfaces"
)

const knChildResourceFilterChunkSizeEnv = "KN_CHILD_RESOURCE_FILTER_CHUNK_SIZE"

var knChildOperations = []string{
	interfaces.OPERATION_TYPE_VIEW_DETAIL,
	interfaces.OPERATION_TYPE_QUERY_DATA,
	interfaces.OPERATION_TYPE_MODIFY,
	interfaces.OPERATION_TYPE_DELETE,
	interfaces.OPERATION_TYPE_AUTHORIZE,
}

var actionTypeOperations = append(append([]string{}, knChildOperations...),
	interfaces.OPERATION_TYPE_TASK_MANAGE,
	interfaces.OPERATION_TYPE_EXECUTE,
)

type knImportPermissionPrecheckedKey struct{}

// WithKNImportPermissionPrechecked marks child creates invoked by an already
// authorized whole-KN create or overwrite operation.
func WithKNImportPermissionPrechecked(ctx context.Context) context.Context {
	return context.WithValue(ctx, knImportPermissionPrecheckedKey{}, true)
}

// KNImportPermissionPrechecked reports whether the parent KN operation already
// authorized the complete import transaction.
func KNImportPermissionPrechecked(ctx context.Context) bool {
	prechecked, _ := ctx.Value(knImportPermissionPrecheckedKey{}).(bool)
	return prechecked
}

// KNChildOperationCandidates returns the instance-level operations exposed to
// the current accessor for one knowledge-network child resource type.
func KNChildOperationCandidates(resourceType string) []string {
	if resourceType == interfaces.RESOURCE_TYPE_ACTION_TYPE {
		return append([]string{}, actionTypeOperations...)
	}
	return append([]string{}, knChildOperations...)
}

// CheckKNChildBatchPermission authorizes every requested child with
// all-or-nothing semantics. Write callers that already opened a transaction
// must roll it back when this check fails.
func CheckKNChildBatchPermission(ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID string, childIDs []string, childOperation string) error {

	if len(childIDs) == 0 {
		return nil
	}
	if err := ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return err
	}

	resourceIDs := interfaces.KNChildResourceIDs(knID, childIDs)
	matched, err := filterKNChildResourceIDs(ctx, ps, resourceType, resourceIDs, childOperation)
	if err != nil {
		return err
	}
	for _, resourceID := range resourceIDs {
		if _, ok := matched[resourceID]; !ok {
			return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
				WithErrorDetails(localizedPermissionDetail(ctx, "PermissionDenied"))
		}
	}
	return nil
}

// FilterKNChildIDs returns the authorized subset of trusted business IDs in
// candidate order. Callers must only pass IDs loaded from the business store.
func FilterKNChildIDs(ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID string, childIDs []string, operation string) ([]string, error) {

	if len(childIDs) == 0 {
		return []string{}, nil
	}
	validChildIDs := make([]string, 0, len(childIDs))
	for _, childID := range childIDs {
		if interfaces.IsValidAuthorizationID(childID) {
			validChildIDs = append(validChildIDs, childID)
		}
	}
	if len(validChildIDs) == 0 {
		return []string{}, nil
	}
	if err := ValidateKNChildAuthorizationIDs(ctx, knID, validChildIDs); err != nil {
		return nil, err
	}

	resourceIDs := interfaces.KNChildResourceIDs(knID, validChildIDs)
	matched, err := filterKNChildResourceIDs(ctx, ps, resourceType, resourceIDs, operation)
	if err != nil {
		return nil, err
	}

	visibleIDs := make([]string, 0, len(matched))
	for index, resourceID := range resourceIDs {
		if _, ok := matched[resourceID]; ok {
			visibleIDs = append(visibleIDs, validChildIDs[index])
		}
	}
	return visibleIDs, nil
}

// RestrictDatasetFilterToIDs combines a concept-search condition with the
// authorized business candidate IDs so the dataset owns total and cursor
// pagination over the already-authorized result set.
func RestrictDatasetFilterToIDs(filterCondition map[string]any, childIDs []string) map[string]any {
	idCondition := map[string]any{
		"field":      "id",
		"operation":  "in",
		"value":      childIDs,
		"value_from": "const",
	}
	if filterCondition == nil {
		return idCondition
	}
	return map[string]any{
		"operation": "and",
		"sub_conditions": []map[string]any{
			filterCondition,
			idCondition,
		},
	}
}

// FilterAndPaginateKNChildren filters a trusted, ordered business candidate set
// before applying pagination.
func FilterAndPaginateKNChildren[T any](ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID string, candidates []T, childID func(T) string,
	offset, limit int) ([]T, int, error) {

	if len(candidates) == 0 {
		return []T{}, 0, nil
	}
	validCandidates := make([]T, 0, len(candidates))
	childIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := childID(candidate)
		if interfaces.IsValidAuthorizationID(id) {
			validCandidates = append(validCandidates, candidate)
			childIDs = append(childIDs, id)
		}
	}
	if len(validCandidates) == 0 {
		return []T{}, 0, nil
	}
	if err := ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return nil, 0, err
	}

	resourceIDs := interfaces.KNChildResourceIDs(knID, childIDs)
	matched, err := filterKNChildResourceIDs(ctx, ps, resourceType,
		resourceIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, 0, err
	}

	visible := make([]T, 0, len(matched))
	for _, candidate := range validCandidates {
		canonicalID := interfaces.KNChildResourceID(knID, childID(candidate))
		if _, ok := matched[canonicalID]; ok {
			visible = append(visible, candidate)
		}
	}

	return PaginateKNChildCandidates(visible, offset, limit), len(visible), nil
}

// FilterAndPaginateKNChildrenWithOperations filters children by view_detail,
// applies pagination after filtering, and returns the allowed operations for
// every visible canonical child resource.
func FilterAndPaginateKNChildrenWithOperations[T any](ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID string, candidates []T, childID func(T) string,
	offset, limit int) ([]T, int, map[string]interfaces.PermissionResourceOps, error) {

	candidateOperations := KNChildOperationCandidates(resourceType)
	if len(candidates) == 0 {
		return []T{}, 0, map[string]interfaces.PermissionResourceOps{}, nil
	}
	validCandidates := make([]T, 0, len(candidates))
	childIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := childID(candidate)
		if interfaces.IsValidAuthorizationID(id) {
			validCandidates = append(validCandidates, candidate)
			childIDs = append(childIDs, id)
		}
	}
	if len(validCandidates) == 0 {
		return []T{}, 0, map[string]interfaces.PermissionResourceOps{}, nil
	}
	if err := ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return nil, 0, nil, err
	}

	resourceIDs := interfaces.KNChildResourceIDs(knID, childIDs)
	matched, err := filterKNChildResourceIDs(ctx, ps, resourceType, resourceIDs,
		interfaces.OPERATION_TYPE_VIEW_DETAIL, candidateOperations)
	if err != nil {
		return nil, 0, nil, err
	}

	visible := make([]T, 0, len(matched))
	for _, candidate := range validCandidates {
		canonicalID := interfaces.KNChildResourceID(knID, childID(candidate))
		if _, ok := matched[canonicalID]; ok {
			visible = append(visible, candidate)
		}
	}
	return PaginateKNChildCandidates(visible, offset, limit), len(visible), matched, nil
}

// GetKNChildOperations returns the allowed operations for a visible child.
func GetKNChildOperations(ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID, childID string) ([]string, error) {

	if err := ValidateKNChildAuthorizationIDs(ctx, knID, []string{childID}); err != nil {
		return nil, err
	}
	canonicalID := interfaces.KNChildResourceID(knID, childID)
	matched, err := filterKNChildResourceIDs(ctx, ps, resourceType, []string{canonicalID},
		interfaces.OPERATION_TYPE_VIEW_DETAIL, KNChildOperationCandidates(resourceType))
	if err != nil {
		return nil, err
	}
	resourceOps, ok := matched[canonicalID]
	if !ok {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden)
	}
	return resourceOps.Operations, nil
}

func filterKNChildResourceIDs(ctx context.Context, ps interfaces.PermissionService,
	resourceType string, resourceIDs []string, operation string, candidateOperations ...[]string) (map[string]interfaces.PermissionResourceOps, error) {

	fullOperations := []string{operation}
	if len(candidateOperations) > 0 {
		fullOperations = candidateOperations[0]
	}

	chunkSize := len(resourceIDs)
	if configured, err := strconv.Atoi(strings.TrimSpace(os.Getenv(knChildResourceFilterChunkSizeEnv))); err == nil && configured > 0 && configured < chunkSize {
		chunkSize = configured
	}
	matched := make(map[string]interfaces.PermissionResourceOps, len(resourceIDs))
	for start := 0; start < len(resourceIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(resourceIDs) {
			end = len(resourceIDs)
		}
		blockIDs := resourceIDs[start:end]
		block, err := ps.FilterResources(ctx, resourceType, blockIDs,
			[]string{operation}, true, fullOperations)
		if err != nil {
			return nil, err
		}
		if err := validateFilterResponse(ctx, blockIDs, block); err != nil {
			return nil, err
		}
		for resourceID, resourceOps := range block {
			matched[resourceID] = resourceOps
		}
	}
	return matched, nil
}

func validateFilterResponse(ctx context.Context, requestedIDs []string,
	matched map[string]interfaces.PermissionResourceOps) error {

	requested := make(map[string]struct{}, len(requestedIDs))
	for _, resourceID := range requestedIDs {
		requested[resourceID] = struct{}{}
	}
	for key, resourceOps := range matched {
		if _, ok := requested[key]; !ok || resourceOps.ResourceID != key {
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				rest.PublicError_InternalServerError).
				WithErrorDetails("invalid resource-filter response")
		}
	}
	return nil
}

// PaginateKNChildCandidates applies list pagination to an already authorized
// ordered candidate set.
func PaginateKNChildCandidates[T any](candidates []T, offset, limit int) []T {
	if limit == -1 || limit == 0 {
		return candidates
	}
	if limit < -1 || offset < 0 || offset >= len(candidates) {
		return []T{}
	}
	end := offset + limit
	if end > len(candidates) {
		end = len(candidates)
	}
	return candidates[offset:end]
}
