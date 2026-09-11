// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

// The Community whitelist is intentionally maintained in code rather than
// derived from catalog.json. This cross-check only prevents an approved entry
// from becoming a dead operation after a catalog edit; it never adds catalog
// operations to the bundle.
func TestCommunityBundleWhitelistOperationsExistInCatalog(t *testing.T) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		t.Fatal(err)
	}
	registered := map[string]map[string]bool{}
	for _, resourceType := range c.ResourceTypes {
		registered[resourceType.ID] = map[string]bool{}
		for _, operation := range resourceType.Operations {
			registered[resourceType.ID][operation.ID] = true
		}
	}
	for _, resourceType := range []string{
		"catalog", "knowledge_network", "connector_type", "tool_box", "mcp", "operator", "skill", "small_model", "large_model",
		"agent", "agent_tpl",
	} {
		operations, ok := authz.CommunityBundleOperations(resourceType)
		if !ok {
			t.Errorf("reviewed Community root %q is missing its explicit whitelist", resourceType)
			continue
		}
		for _, operation := range operations {
			if !registered[resourceType][operation] {
				t.Errorf("Community whitelist contains unregistered operation %s:%s", resourceType, operation)
			}
		}
	}
}

// TestAgentCatalogMatchesReviewedContract pins the resource-owner review from
// openbkn-ai/bkn-docs#117 at 8e28e43b. Agent execution is named "use" in the
// compatibility vocabulary; neither root has a natural parent or an operation
// prerequisite, and no generic view/modify/delete verbs are invented here.
func TestAgentCatalogMatchesReviewedContract(t *testing.T) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"agent": {
			"use", "publish", "unpublish", "unpublish_other_user_agent",
			"publish_to_be_skill_agent", "publish_to_be_web_sdk_agent",
			"publish_to_be_api_agent", "publish_to_be_data_flow_agent",
			"create_system_agent", "mgnt_built_in_agent", "see_trajectory_analysis",
		},
		"agent_tpl": {"publish", "unpublish", "unpublish_other_user_agent_tpl"},
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
		got := make([]string, 0, len(resourceType.Operations))
		for _, operation := range resourceType.Operations {
			got = append(got, operation.ID)
			if operation.ParentOperation != "" || len(operation.Requires) != 0 {
				t.Errorf("%s/%s unexpectedly declares parent=%q requires=%v",
					resourceType.ID, operation.ID, operation.ParentOperation, operation.Requires)
			}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s operations = %v, want %v", resourceType.ID, got, expected)
		}
	}
	for resourceType := range want {
		if !seen[resourceType] {
			t.Errorf("reviewed Agent resource type %q is missing", resourceType)
		}
	}
}
