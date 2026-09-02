// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"testing"

	"bkn-backend/interfaces"
)

func TestKNChildResourcePEPDefaultsToDisabled(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "")
	if KNChildResourcePEPEnabled() {
		t.Fatal("KN child resource PEP must default to disabled")
	}
	resource, operation := ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		"kn-1", "legacy/id", interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_DELETE)
	if resource.Type != interfaces.RESOURCE_TYPE_KN || resource.ID != "kn-1" {
		t.Fatalf("permission resource = %#v, want parent KN", resource)
	}
	if operation != interfaces.OPERATION_TYPE_MODIFY {
		t.Fatalf("permission operation = %q, want legacy modify", operation)
	}
	if err := ValidateKNChildPEPAuthorizationIDs(context.Background(), "kn-1", []string{"legacy/id"}); err != nil {
		t.Fatalf("disabled PEP rejected a historical ID: %v", err)
	}
}

func TestKNChildResourcePEPEnabledUsesCanonicalChild(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "true")
	if !KNChildResourcePEPEnabled() {
		t.Fatal("KN child resource PEP must be enabled")
	}
	resource, operation := ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		"kn-1", "ot-1", interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_DELETE)
	if resource.Type != interfaces.RESOURCE_TYPE_OBJECT_TYPE || resource.ID != "kn-1/ot-1" {
		t.Fatalf("permission resource = %#v, want canonical child", resource)
	}
	if operation != interfaces.OPERATION_TYPE_DELETE {
		t.Fatalf("permission operation = %q, want child delete", operation)
	}
	if err := ValidateKNChildPEPAuthorizationIDs(context.Background(), "kn-1", []string{"bad/id"}); err == nil {
		t.Fatal("enabled PEP must reject ambiguous child IDs")
	}
}
