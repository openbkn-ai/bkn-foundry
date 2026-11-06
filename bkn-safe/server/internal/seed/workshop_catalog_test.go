// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestWorkshopCatalogMatchesContract pins the operation vocabulary the
// enterprise Workshop service checks and grants. Both types are standalone
// roots; every operation but view and the type-wide create depends on view,
// directly or through modify.
func TestWorkshopCatalogMatchesContract(t *testing.T) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string][]string{
		"workshop_app": {
			"view": nil, "create": nil, "use": {"view"}, "modify": {"view"},
			"publish": {"modify"}, "delete": {"modify"}, "authorize": {"view"},
		},
		"graph_analysis": {
			"view": nil, "modify": {"view"}, "delete": {"modify"}, "authorize": {"view"},
		},
	}
	seen := map[string]bool{}
	for _, resourceType := range c.ResourceTypes {
		expected, ok := want[resourceType.ID]
		if !ok {
			continue
		}
		seen[resourceType.ID] = true
		if resourceType.ParentType != "" {
			t.Errorf("%s parent_type = %q, want standalone root", resourceType.ID, resourceType.ParentType)
		}
		got := map[string][]string{}
		for _, operation := range resourceType.Operations {
			if operation.ParentOperation != "" {
				t.Errorf("%s/%s unexpectedly declares parent_operation %q", resourceType.ID, operation.ID, operation.ParentOperation)
			}
			var requires []string
			if len(operation.Requires) > 0 {
				requires = operation.Requires
			}
			got[operation.ID] = requires
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s operations = %v, want %v", resourceType.ID, got, expected)
		}
	}
	for resourceType := range want {
		if !seen[resourceType] {
			t.Errorf("Workshop resource type %q is missing", resourceType)
		}
	}
}
