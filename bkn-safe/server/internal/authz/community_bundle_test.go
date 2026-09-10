// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"reflect"
	"sort"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestCommunityBundleWhitelistIsExplicitAndDefensive(t *testing.T) {
	want := map[string][]string{
		"catalog":              {"view_detail", "modify", "delete", "query_data", "resource_manage", "task_manage"},
		"knowledge_network":    {"view_detail", "modify", "delete", "query_data", "execute"},
		"stream_data_pipeline": {"view_detail", "modify", "delete", "query_data"},
		"connector_type":       {"view_detail", "modify", "delete", "task_manage"},
		"tool_box":             {"view", "modify", "delete", "publish", "unpublish", "execute"},
		"mcp":                  {"view", "modify", "delete", "publish", "unpublish", "execute"},
		"operator":             {"view", "modify", "delete", "publish", "unpublish", "execute"},
		"skill":                {"view", "modify", "delete", "publish", "unpublish", "execute"},
		"small_model":          {"display", "modify", "delete", "execute"},
		"large_model":          {"display", "modify", "delete", "execute"},
		"agent": {
			"use", "publish", "unpublish", "publish_to_be_skill_agent",
			"publish_to_be_web_sdk_agent", "publish_to_be_api_agent",
			"publish_to_be_data_flow_agent", "see_trajectory_analysis",
		},
		"agent_tpl": {"publish", "unpublish"},
	}
	for resourceType, expected := range want {
		got, ok := CommunityBundleOperations(resourceType)
		if !ok || !reflect.DeepEqual(got, expected) {
			t.Errorf("CommunityBundleOperations(%q) = %v, %v; want %v, true", resourceType, got, ok, expected)
		}
		if len(got) > 0 {
			got[0] = "caller_mutation"
			again, _ := CommunityBundleOperations(resourceType)
			if len(again) > 0 && again[0] == "caller_mutation" {
				t.Fatalf("CommunityBundleOperations(%q) exposed its stored whitelist", resourceType)
			}
		}
	}
	for _, unsupported := range []string{"resource", "object_type", "admin-authz", "safe_admin", "unknown"} {
		if got, ok := CommunityBundleOperations(unsupported); ok || got != nil {
			t.Errorf("unsupported type %q returned %v, %v", unsupported, got, ok)
		}
	}
}

func TestCommunityBundlePersistsOneLogicalPolicyAndExpandsOnlyApprovedOps(t *testing.T) {
	edition := useEdition(t, licverify.EditionCommunity)
	e := newTestEnforcer(t)
	const user = "bundle-holder"

	mustNoErr(t, e.GrantCommunityBundle(user, "knowledge_network", "kn-1", AuthoritySourceAdminAuthz))
	mustNoErr(t, e.GrantCommunityBundle(user, "knowledge_network", "kn-1", AuthoritySourceAdminAuthz))
	records, err := e.PolicyRecords(PolicyFilter{AccessorID: user})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Operation != ActFullBusinessAccess ||
		records[0].PolicySource != PolicySourceCommunityBundle || records[0].Effect != EffectAllow {
		t.Fatalf("persisted bundle = %+v; want one logical community_bundle row", records)
	}

	for _, op := range []string{"view_detail", "modify", "delete", "query_data", "execute"} {
		allowed, err := e.Check(user, "knowledge_network", "kn-1", op)
		if err != nil || !allowed {
			t.Errorf("bundle Check(%q) = %v, %v; want true", op, allowed, err)
		}
	}
	for _, excluded := range []string{"create", "authorize", "task_manage", "public_access", ActFullBusinessAccess} {
		allowed, err := e.Check(user, "knowledge_network", "kn-1", excluded)
		if err != nil || allowed {
			t.Errorf("bundle Check(excluded %q) = %v, %v; want false", excluded, allowed, err)
		}
	}
	allowed, err := e.Check(user, "knowledge_network", "kn-2", "view_detail")
	if err != nil || allowed {
		t.Fatalf("bundle leaked to sibling: allowed=%v err=%v", allowed, err)
	}
	for _, upgraded := range []licverify.Edition{
		licverify.EditionProfessional, licverify.EditionEnterprise, licverify.EditionIndustry,
	} {
		*edition = upgraded
		allowed, err := e.Check(user, "knowledge_network", "kn-1", "execute")
		if err != nil || !allowed {
			t.Errorf("bundle stopped after upgrade to %q: allowed=%v err=%v", upgraded, allowed, err)
		}
	}
	afterReads, err := e.PolicyRecords(PolicyFilter{AccessorID: user})
	if err != nil || len(afterReads) != 1 || afterReads[0].Operation != ActFullBusinessAccess {
		t.Fatalf("permission reads materialized bundle operations: %+v, %v", afterReads, err)
	}
}

func TestCommunityBundleDecisionPathsAndParentFallbackAgree(t *testing.T) {
	useEdition(t, licverify.EditionCommunity)
	e, db := newTestEnforcerDB(t)
	for _, row := range []model.ResourceType{
		{ID: "knowledge_network"},
		{ID: "object_type", ParentTypeID: "knowledge_network"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for child, parent := range map[string]string{
		"view_detail": "view_detail",
		"query_data":  "query_data",
		"modify":      "modify",
		"delete":      "modify",
	} {
		if err := db.Create(&model.Operation{ResourceTypeID: "object_type", ID: child, ParentOperationID: parent}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "object_type", ResourceID: "kn-1/customer",
		ParentTypeID: "knowledge_network", ParentID: "kn-1",
	}).Error; err != nil {
		t.Fatal(err)
	}

	const user = "bundle-holder"
	mustNoErr(t, e.GrantCommunityBundle(user, "knowledge_network", "kn-1", AuthoritySourceAdminAuthz))
	candidates := []string{"view_detail", "modify", "delete", "query_data", "execute", "authorize"}

	for _, op := range candidates[:5] {
		allowed, err := e.Check(user, "knowledge_network", "kn-1", op)
		if err != nil || !allowed {
			t.Fatalf("Check(%q) = %v, %v; want true", op, allowed, err)
		}
	}
	allowedOps, err := e.AllowedOps(user, "knowledge_network", "kn-1", candidates)
	if err != nil || !reflect.DeepEqual(allowedOps, candidates[:5]) {
		t.Fatalf("AllowedOps = %v, %v; want %v", allowedOps, err, candidates[:5])
	}
	filtered, err := e.FilterResourceOps(user,
		[]ResourceRef{{Type: "knowledge_network", ID: "kn-1"}, {Type: "knowledge_network", ID: "kn-2"}},
		[]string{"view_detail"}, candidates)
	if err != nil || len(filtered) != 1 || filtered[0].ID != "kn-1" || !reflect.DeepEqual(filtered[0].Operations, candidates[:5]) {
		t.Fatalf("FilterResourceOps = %+v, %v; want only expanded kn-1", filtered, err)
	}
	ids, err := e.AccessibleResources(user, "knowledge_network", "view_detail")
	if err != nil || !reflect.DeepEqual(ids, []string{"kn-1"}) {
		t.Fatalf("AccessibleResources(root) = %v, %v; want [kn-1]", ids, err)
	}
	childIDs, err := e.AccessibleResources(user, "object_type", "view_detail")
	if err != nil || !reflect.DeepEqual(childIDs, []string{"kn-1/customer"}) {
		t.Fatalf("AccessibleResources(child) = %v, %v; want inherited child", childIDs, err)
	}
	for _, op := range []string{"view_detail", "query_data", "modify", "delete"} {
		allowed, err := e.Check(user, "object_type", "kn-1/customer", op)
		if err != nil || !allowed {
			t.Errorf("inherited child Check(%q) = %v, %v; want true", op, allowed, err)
		}
	}

	_, effective, err := e.EffectivePermissions(user, PermQuery{ResourceType: "knowledge_network"})
	if err != nil || len(effective) != 1 || effective[0].Object != "knowledge_network:kn-1" {
		t.Fatalf("EffectivePermissions = %+v, %v", effective, err)
	}
	assertBusinessOps(t, effective[0].Operations, candidates[:5])
	policies, err := e.ResourcePolicies("knowledge_network", "kn-1")
	if err != nil || len(policies) != 1 || policies[0].AccessorID != user {
		t.Fatalf("ResourcePolicies = %+v, %v", policies, err)
	}
	assertBusinessOps(t, policies[0].Operations, candidates[:5])
	grants, err := e.ListObjectGrants(user, "knowledge_network", "kn-1")
	if err != nil || len(grants) != 1 {
		t.Fatalf("ListObjectGrants = %+v, %v", grants, err)
	}
	assertBusinessOps(t, grants[0].Operations, candidates[:5])
}

func TestCommunityBundleCombinedProjectionPreservesAnOriginSubject(t *testing.T) {
	rows := [][]string{{
		"bundle-role", "knowledge_network:kn-1", ActFullBusinessAccess,
		EffectAllow, string(PolicySourceCommunityBundle), string(AuthoritySourceAdminAuthz),
	}}

	projected := projectCommunityBundleRows(rows, true)
	if len(projected) == 0 {
		t.Fatal("combined bundle projection is empty")
	}
	for _, row := range projected {
		if len(row) == 0 || row[0] != "bundle-role" {
			t.Fatalf("combined bundle projection = %+v; want subject bundle-role", row)
		}
	}
}

func TestCommunityBundleProfessionalDenyAndLegacyRemainIndependent(t *testing.T) {
	edition := useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	const user = "mixed-holder"
	mustNoErr(t, e.GrantCommunityBundle(user, "knowledge_network", "kn-1", AuthoritySourceAdminAuthz))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "legacy_only"))
	mustNoErr(t, e.GrantSystemObjectPermission(user, "knowledge_network", "kn-1", "authorize"))
	mustNoErr(t, e.GrantProfessionalObjectPermission(
		user, "knowledge_network", "kn-1", "query_data", EffectDeny, AuthoritySourceAdminAuthz,
	))

	queryAllowed, err := e.Check(user, "knowledge_network", "kn-1", "query_data")
	if err != nil || queryAllowed {
		t.Fatalf("Professional deny did not override bundle: allowed=%v err=%v", queryAllowed, err)
	}
	if ok, err := e.Check(user, "knowledge_network", "kn-1", "legacy_only"); err != nil || !ok {
		t.Fatalf("legacy rule stopped participating: allowed=%v err=%v", ok, err)
	}
	if ok, err := e.Check(user, "knowledge_network", "kn-1", "authorize"); err != nil || !ok {
		t.Fatalf("system-derived rule stopped participating: allowed=%v err=%v", ok, err)
	}
	*edition = licverify.EditionCommunity
	if ok, err := e.Check(user, "knowledge_network", "kn-1", "query_data"); err != nil || !ok {
		t.Fatalf("Community did not recover bundle operation: allowed=%v err=%v", ok, err)
	}

	removed, err := e.RemoveCommunityBundle(user, "knowledge_network", "kn-1", AuthoritySourceAdminAuthz)
	if err != nil || !removed {
		t.Fatalf("RemoveCommunityBundle = %v, %v; want true", removed, err)
	}
	if ok, err := e.Check(user, "knowledge_network", "kn-1", "view_detail"); err != nil || ok {
		t.Fatalf("revoked bundle still effective: allowed=%v err=%v", ok, err)
	}
	for _, op := range []string{"legacy_only", "authorize"} {
		if ok, err := e.Check(user, "knowledge_network", "kn-1", op); err != nil || !ok {
			t.Errorf("revoking bundle removed %q sibling source: allowed=%v err=%v", op, ok, err)
		}
	}
	records, err := e.PolicyRecords(PolicyFilter{AccessorID: user})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.PolicySource == PolicySourceCommunityBundle {
			t.Fatalf("bundle row survived source-scoped revoke: %+v", records)
		}
	}
}

func TestCommunityBundleStableGrantSurvivesReloadAndRevokesIndependently(t *testing.T) {
	useEdition(t, licverify.EditionCommunity)
	e, db := newTestEnforcerDB(t)
	const user = "stable-bundle-holder"

	mustNoErr(t, e.GrantCommunityBundle(user, "knowledge_network", "kn-1", AuthoritySourceAdminAuthz))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "view_detail"))
	records, err := e.PolicyRecords(PolicyFilter{AccessorID: user, Object: "knowledge_network:kn-1"})
	if err != nil || len(records) != 2 {
		t.Fatalf("persisted grants = %+v, %v; want bundle and legacy grants", records, err)
	}
	var bundleID, legacyID string
	for _, record := range records {
		switch record.PolicySource {
		case PolicySourceCommunityBundle:
			bundleID = record.GrantID
		case PolicySourceLegacy:
			legacyID = record.GrantID
		}
	}
	if bundleID == "" || legacyID == "" || bundleID == legacyID {
		t.Fatalf("grant identities = bundle %q legacy %q; want distinct stable ids", bundleID, legacyID)
	}

	reloaded, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	reloadedRecords, err := reloaded.PolicyRecords(PolicyFilter{AccessorID: user, Object: "knowledge_network:kn-1"})
	if err != nil || len(reloadedRecords) != 2 {
		t.Fatalf("reloaded grants = %+v, %v; want two independent records", reloadedRecords, err)
	}
	reloadedIDs := map[string]bool{}
	for _, record := range reloadedRecords {
		reloadedIDs[record.GrantID] = true
	}
	if !reloadedIDs[bundleID] || !reloadedIDs[legacyID] {
		t.Fatalf("reloaded grant ids = %v; want bundle %q and legacy %q", reloadedIDs, bundleID, legacyID)
	}
	if ok, err := reloaded.Check(user, "knowledge_network", "kn-1", "execute"); err != nil || !ok {
		t.Fatalf("reloaded bundle execute = %v, %v; want true", ok, err)
	}

	removed, err := reloaded.RevokePolicy(bundleID)
	if err != nil || !removed {
		t.Fatalf("RevokePolicy(bundle) = %v, %v; want true", removed, err)
	}
	if ok, err := reloaded.Check(user, "knowledge_network", "kn-1", "execute"); err != nil || ok {
		t.Fatalf("revoked bundle execute = %v, %v; want false", ok, err)
	}
	if ok, err := reloaded.Check(user, "knowledge_network", "kn-1", "view_detail"); err != nil || !ok {
		t.Fatalf("revoking bundle removed legacy sibling: allowed=%v err=%v", ok, err)
	}
	remaining, err := reloaded.PolicyRecords(PolicyFilter{AccessorID: user, Object: "knowledge_network:kn-1"})
	if err != nil || len(remaining) != 1 || remaining[0].GrantID != legacyID ||
		remaining[0].PolicySource != PolicySourceLegacy {
		t.Fatalf("remaining grants = %+v, %v; want only legacy %q", remaining, err, legacyID)
	}
}

func TestCommunityBundleDoesNotExpandLegacyOrAcceptChildTargets(t *testing.T) {
	useEdition(t, licverify.EditionCommunity)
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission("legacy-holder", "knowledge_network", "kn-1", "view_detail"))
	mustNoErr(t, e.GrantCommunityBundle("bundle-holder", "knowledge_network", "kn-1", AuthoritySourceAdminAuthz))

	const newlyApproved = "newly_approved_operation"
	if ok, err := e.Check("bundle-holder", "knowledge_network", "kn-1", newlyApproved); err != nil || ok {
		t.Fatalf("unapproved operation was available before whitelist update: allowed=%v err=%v", ok, err)
	}
	original := communityBundleOperations["knowledge_network"]
	communityBundleOperations["knowledge_network"] = append(append([]string(nil), original...), newlyApproved)
	t.Cleanup(func() { communityBundleOperations["knowledge_network"] = original })
	if ok, err := e.Check("bundle-holder", "knowledge_network", "kn-1", newlyApproved); err != nil || !ok {
		t.Fatalf("existing bundle did not acquire newly approved operation: allowed=%v err=%v", ok, err)
	}
	if ok, err := e.Check("legacy-holder", "knowledge_network", "kn-1", newlyApproved); err != nil || ok {
		t.Fatalf("legacy holder acquired new bundle operation: allowed=%v err=%v", ok, err)
	}

	for _, target := range []struct{ resourceType, resourceID string }{
		{"object_type", "kn-1/customer"},
		{"resource", "r-1"},
		{"knowledge_network", ""},
		{"knowledge_network", "*"},
		{"knowledge_network", "kn-*"},
	} {
		if err := e.GrantCommunityBundle("u-1", target.resourceType, target.resourceID, AuthoritySourceAdminAuthz); err == nil {
			t.Errorf("GrantCommunityBundle(%q,%q) succeeded; want rejection", target.resourceType, target.resourceID)
		}
	}
	records, err := e.PolicyRecords(PolicyFilter{AccessorID: "u-1"})
	if err != nil || len(records) != 0 {
		t.Fatalf("invalid targets produced policies: %+v, %v", records, err)
	}
}

func assertBusinessOps(t *testing.T, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("business operations = %v; want %v", got, want)
	}
}
