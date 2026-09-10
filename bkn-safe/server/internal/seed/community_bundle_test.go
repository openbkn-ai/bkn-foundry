// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"encoding/json"
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
		"catalog", "knowledge_network", "stream_data_pipeline", "connector_type",
		"tool_box", "mcp", "operator", "skill", "small_model", "large_model", "agent", "agent_tpl",
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
