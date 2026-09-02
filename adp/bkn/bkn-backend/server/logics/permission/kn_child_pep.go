// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"os"
	"strings"

	"bkn-backend/interfaces"
)

const knChildResourcePEPEnabledEnv = "KN_CHILD_RESOURCE_PEP_ENABLED"

// KNChildResourcePEPEnabled reports whether single-resource KN child PEPs use
// canonical child resources. It defaults to false until existing authorization
// data has been migrated.
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
