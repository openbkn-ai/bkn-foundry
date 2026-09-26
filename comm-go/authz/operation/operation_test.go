// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package operation

import "testing"

func TestPublishedWireValues(t *testing.T) {
	want := map[ID]string{
		Authorize: "authorize", Create: "create", CreateSystemAgent: "create_system_agent",
		DataWrite: "data_write", Delete: "delete", Display: "display", Edit: "edit",
		Execute: "execute", FullBusinessAccess: "full_business_access", Grant: "grant",
		Manage: "manage", Members: "members", ManageBuiltInAgent: "mgnt_built_in_agent",
		Modify: "modify", Permissions: "permissions", PublicAccess: "public_access",
		Publish: "publish", PublishToAPI: "publish_to_be_api_agent",
		PublishToDataFlow: "publish_to_be_data_flow_agent", PublishToSkill: "publish_to_be_skill_agent",
		PublishToWebSDK: "publish_to_be_web_sdk_agent", QueryData: "query_data",
		ResetPassword: "reset-password", ResourceManage: "resource_manage", Revoke: "revoke",
		SeeTrajectoryAnalysis: "see_trajectory_analysis", TaskManage: "task_manage", Toggle: "toggle",
		Unpublish: "unpublish", UnpublishOtherUserAgent: "unpublish_other_user_agent",
		UnpublishOtherUserAgentTpl: "unpublish_other_user_agent_tpl", Use: "use", View: "view",
		ViewDetail: "view_detail", ViewSummary: "view_summary",
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
}

func TestAllReturnsCopy(t *testing.T) {
	first := All()
	first[0] = "changed"
	if All()[0] == "changed" {
		t.Error("All exposes the package operation list for mutation")
	}
}
