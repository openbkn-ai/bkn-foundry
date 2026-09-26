// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package operation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedWireValues(t *testing.T) {
	want := map[ID]string{
		Authorize: "authorize", Create: "create", CreateSystemAgent: "create_system_agent",
		DataWrite: "data_write", Delete: "delete", Display: "display", Edit: "edit",
		Execute: "execute", FullBusinessAccess: "full_business_access", Grant: "grant",
		Heartbeat: "heartbeat",
		Manage:    "manage", Members: "members", ManageBuiltInAgent: "mgnt_built_in_agent",
		Modify: "modify", Permissions: "permissions", PublicAccess: "public_access",
		Publish: "publish", PublishToAPI: "publish_to_be_api_agent",
		PublishToDataFlow: "publish_to_be_data_flow_agent", PublishToSkill: "publish_to_be_skill_agent",
		PublishToWebSDK: "publish_to_be_web_sdk_agent", QueryData: "query_data",
		Read: "read", Reconcile: "reconcile",
		ResetPassword: "reset-password", ResourceManage: "resource_manage", Revoke: "revoke",
		SeeTrajectoryAnalysis: "see_trajectory_analysis", TaskManage: "task_manage", Toggle: "toggle",
		Unpublish: "unpublish", UnpublishOtherUserAgent: "unpublish_other_user_agent",
		UnpublishOtherUserAgentTpl: "unpublish_other_user_agent_tpl", Use: "use", View: "view",
		ViewDetail: "view_detail", ViewSummary: "view_summary", Write: "write",
	}
	if len(All()) != len(want) {
		t.Fatalf("published operation count = %d, want %d", len(All()), len(want))
	}
	for operation, wire := range want {
		if string(operation) != wire {
			t.Errorf("operation wire value = %q, want %q", operation, wire)
		}
		if !Known(wire) {
			t.Errorf("Known(%q) = false, want true", wire)
		}
	}
	if Known("not_a_published_operation") {
		t.Error("Known accepted an unknown operation")
	}
	if Known(string(Wildcard)) {
		t.Error("Known accepted the wildcard policy matcher as an operation")
	}
	if !KnownReference(string(Wildcard)) {
		t.Error("KnownReference rejected the wildcard policy matcher")
	}
}

func TestAllReturnsCopy(t *testing.T) {
	first := All()
	first[0] = "changed"
	if All()[0] == "changed" {
		t.Error("All exposes the package operation list for mutation")
	}
}

func TestMonorepoRegistryMatchesPublishedVocabulary(t *testing.T) {
	path := filepath.Join("..", "..", "..", "bkn-safe", "server", "internal", "seed", "data", "authorization-registry.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// The published comm-go module does not contain the monorepo's
			// bkn-safe checkout. The repository CI runs this assertion from
			// the monorepo; standalone module users do not have that fixture.
			t.Skip("bkn-safe registry is only available in the monorepo checkout")
		}
		t.Fatalf("read bkn-safe authorization registry: %v", err)
	}
	var catalog struct {
		ResourceTypes []struct {
			Operations []struct {
				ID string `json:"id"`
			} `json:"operations"`
		} `json:"resource_types"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("decode bkn-safe authorization registry: %v", err)
	}

	registered := make(map[string]struct{})
	for _, resourceType := range catalog.ResourceTypes {
		for _, operation := range resourceType.Operations {
			registered[operation.ID] = struct{}{}
			if !Known(operation.ID) {
				t.Errorf("registry operation %q is missing from comm-go vocabulary", operation.ID)
			}
		}
	}
	for _, operation := range All() {
		if operation == FullBusinessAccess {
			// This is a logical request bundle, intentionally absent from the
			// resource catalog and therefore not a registry operation.
			continue
		}
		if _, ok := registered[string(operation)]; !ok {
			t.Errorf("comm-go operation %q is missing from authorization registry", operation)
		}
	}
}
