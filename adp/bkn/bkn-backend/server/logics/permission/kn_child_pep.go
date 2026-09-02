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

const knChildResourcePEPEnabledEnv = "KN_CHILD_RESOURCE_PEP_ENABLED"
const knChildResourceFilterChunkSizeEnv = "KN_CHILD_RESOURCE_FILTER_CHUNK_SIZE"

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

// KNChildResourcePEPEnabled reports whether KN child PEPs use canonical child
// resources. It defaults to false until existing authorization data has been
// migrated.
func KNChildResourcePEPEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(knChildResourcePEPEnabledEnv)))
	return value == "true" || value == "1"
}

// ValidateKNChildPEPAuthorizationIDs applies canonical-ID validation only when
// child-resource PEPs are enabled. Disabled deployments retain the legacy KN
// authorization behavior for existing resources with historical IDs.
func ValidateKNChildPEPAuthorizationIDs(ctx context.Context, knID string, childIDs []string) error {
	if !KNChildResourcePEPEnabled() {
		return nil
	}
	return ValidateKNChildAuthorizationIDs(ctx, knID, childIDs)
}

// ResolveKNChildPermissionTarget selects the legacy parent-KN target while the
// child-resource PEP is disabled, and the canonical child target when enabled.
func ResolveKNChildPermissionTarget(resourceType, knID, childID, legacyOperation,
	childOperation string) (interfaces.PermissionResource, string) {

	if !KNChildResourcePEPEnabled() {
		return interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID}, legacyOperation
	}
	return interfaces.KNChildPermissionResource(resourceType, knID, childID), childOperation
}

// CheckKNChildBatchPermission authorizes every child before business writes.
// Callers that already opened a transaction must roll it back when this check
// fails.
func CheckKNChildBatchPermission(ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID string, childIDs []string, legacyOperation, childOperation string) error {

	if !KNChildResourcePEPEnabled() {
		return ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   knID,
		}, []string{legacyOperation})
	}
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
	if err := ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return nil, err
	}

	resourceIDs := interfaces.KNChildResourceIDs(knID, childIDs)
	matched, err := filterKNChildResourceIDs(ctx, ps, resourceType, resourceIDs, operation)
	if err != nil {
		return nil, err
	}

	visibleIDs := make([]string, 0, len(matched))
	for index, resourceID := range resourceIDs {
		if _, ok := matched[resourceID]; ok {
			visibleIDs = append(visibleIDs, childIDs[index])
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
// before applying pagination. Disabled deployments retain the legacy parent-KN
// visibility check; enabled deployments filter canonical child resources.
func FilterAndPaginateKNChildren[T any](ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID string, candidates []T, childID func(T) string,
	offset, limit int) ([]T, int, error) {

	if !KNChildResourcePEPEnabled() {
		if err := ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   knID,
		}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}); err != nil {
			return nil, 0, err
		}
		return PaginateKNChildCandidates(candidates, offset, limit), len(candidates), nil
	}

	if len(candidates) == 0 {
		return []T{}, 0, nil
	}
	childIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		childIDs = append(childIDs, childID(candidate))
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
	for _, candidate := range candidates {
		canonicalID := interfaces.KNChildResourceID(knID, childID(candidate))
		if _, ok := matched[canonicalID]; ok {
			visible = append(visible, candidate)
		}
	}

	return PaginateKNChildCandidates(visible, offset, limit), len(visible), nil
}

func filterKNChildResourceIDs(ctx context.Context, ps interfaces.PermissionService,
	resourceType string, resourceIDs []string, operation string) (map[string]interfaces.PermissionResourceOps, error) {

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
			[]string{operation}, true, []string{operation})
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
