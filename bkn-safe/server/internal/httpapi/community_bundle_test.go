// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestCommunityBundleHTTPReadPathsAgree(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	const user = "bundle-http-user"
	if err := db.Create(&model.User{ID: user, Account: user, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	approved := []string{"view_detail", "modify", "delete", "query_data", "execute"}
	seedCatalogOps(t, db, "knowledge_network",
		"view_detail", "create", "modify", "delete", "query_data", "authorize", "task_manage", "execute")
	if err := e.GrantCommunityBundle(user, "knowledge_network", "kn-1", authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	// The legacy row overlaps one projected bundle operation. Read APIs expose
	// the effective operation once rather than leaking duplicate source rows.
	if err := e.GrantObjectPermission(user, "knowledge_network", "kn-1", "view_detail"); err != nil {
		t.Fatal(err)
	}

	check := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "knowledge_network", "id": "kn-1"},
		"operation":   "execute",
	})
	if check.Code != http.StatusOK || !jsonBool(t, check.Body.Bytes(), "allowed") {
		t.Fatalf("check = %d %s; want allowed", check.Code, check.Body.String())
	}

	operations := do(t, r, http.MethodPost, "/api/safe/v1/authz/operations", map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "knowledge_network", "id": "kn-1"},
	})
	assertJSONOperations(t, operations.Code, operations.Body.Bytes(), approved)

	filtered := postFilter(t, r, map[string]any{
		"accessor_id":           user,
		"resource_type":         "knowledge_network",
		"resource_ids":          []string{"kn-1", "kn-2"},
		"visibility_operations": []string{"view_detail"},
		"candidate_operations":  []string{"view_detail", "create", "modify", "delete", "query_data", "authorize", "task_manage", "execute"},
	})
	if len(filtered) != 1 || filtered[0].ResourceID != "kn-1" {
		t.Fatalf("resource-filter = %+v; want only kn-1", filtered)
	}
	assertSameOperations(t, filtered[0].Operations, approved)

	resources := do(t, r, http.MethodGet,
		"/api/safe/v1/authz/resources?accessor_id="+user+"&resource_type=knowledge_network&operation=view_detail", nil)
	if resources.Code != http.StatusOK {
		t.Fatalf("resources = %d %s", resources.Code, resources.Body.String())
	}
	var resourceResponse struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(resources.Body.Bytes(), &resourceResponse); err != nil || !reflect.DeepEqual(resourceResponse.IDs, []string{"kn-1"}) {
		t.Fatalf("resources = %v, %v; want [kn-1]", resourceResponse.IDs, err)
	}

	policies := do(t, r, http.MethodGet,
		"/api/safe/v1/authz/policies?resource_type=knowledge_network&resource_id=kn-1", nil)
	if policies.Code != http.StatusOK {
		t.Fatalf("policies = %d %s", policies.Code, policies.Body.String())
	}
	var policyResponse struct {
		Entries []struct {
			AccessorID string   `json:"accessor_id"`
			Operations []string `json:"operations"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(policies.Body.Bytes(), &policyResponse); err != nil ||
		len(policyResponse.Entries) != 1 || policyResponse.Entries[0].AccessorID != user {
		t.Fatalf("policies = %+v, %v", policyResponse, err)
	}
	assertSameOperations(t, policyResponse.Entries[0].Operations, approved)

	mePermissions := tokReq(t, r, http.MethodGet,
		"/api/safe/v1/me/permissions?resource_type=knowledge_network&resource_id=kn-1", nil, user)
	if mePermissions.Code != http.StatusOK {
		t.Fatalf("me permissions = %d %s", mePermissions.Code, mePermissions.Body.String())
	}
	var meResponse struct {
		Permissions []struct {
			Operations []string `json:"operations"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(mePermissions.Body.Bytes(), &meResponse); err != nil || len(meResponse.Permissions) != 1 {
		t.Fatalf("me permissions = %+v, %v", meResponse, err)
	}
	assertSameOperations(t, meResponse.Permissions[0].Operations, approved)

	knGrants := tokReq(t, r, http.MethodGet, "/api/safe/v1/me/knowledge-network-grants", nil, user)
	if knGrants.Code != http.StatusOK {
		t.Fatalf("knowledge-network-grants = %d %s", knGrants.Code, knGrants.Body.String())
	}
	var grantResponse struct {
		Grants []struct {
			KnowledgeNetworkID string   `json:"knowledge_network_id"`
			Operations         []string `json:"operations"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(knGrants.Body.Bytes(), &grantResponse); err != nil ||
		len(grantResponse.Grants) != 1 || grantResponse.Grants[0].KnowledgeNetworkID != "kn-1" {
		t.Fatalf("knowledge-network-grants = %+v, %v", grantResponse, err)
	}
	assertSameOperations(t, grantResponse.Grants[0].Operations, approved)
}

func TestAgentBundleHTTPReadPathsAgree(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	const user = "agent-bundle-http-user"
	if err := db.Create(&model.User{ID: user, Account: user, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	approved := []string{
		"use", "publish", "unpublish", "publish_to_be_skill_agent",
		"publish_to_be_web_sdk_agent", "publish_to_be_api_agent",
		"publish_to_be_data_flow_agent", "see_trajectory_analysis",
	}
	allCatalogOperations := append(append([]string(nil), approved...),
		"unpublish_other_user_agent", "create_system_agent", "mgnt_built_in_agent")
	seedCatalogOps(t, db, "agent", allCatalogOperations...)
	if err := e.GrantCommunityBundle(user, "agent", "agent-1", authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}

	check := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "agent", "id": "agent-1"},
		"operation":   "use",
	})
	if check.Code != http.StatusOK || !jsonBool(t, check.Body.Bytes(), "allowed") {
		t.Fatalf("check = %d %s; want allowed", check.Code, check.Body.String())
	}

	operations := do(t, r, http.MethodPost, "/api/safe/v1/authz/operations", map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "agent", "id": "agent-1"},
	})
	assertJSONOperations(t, operations.Code, operations.Body.Bytes(), approved)

	filtered := postFilter(t, r, map[string]any{
		"accessor_id":           user,
		"resource_type":         "agent",
		"resource_ids":          []string{"agent-1", "agent-2"},
		"visibility_operations": []string{"use"},
		"candidate_operations":  allCatalogOperations,
	})
	if len(filtered) != 1 || filtered[0].ResourceID != "agent-1" {
		t.Fatalf("resource-filter = %+v; want only agent-1", filtered)
	}
	assertSameOperations(t, filtered[0].Operations, approved)

	resources := do(t, r, http.MethodGet,
		"/api/safe/v1/authz/resources?accessor_id="+user+"&resource_type=agent&operation=use", nil)
	if resources.Code != http.StatusOK {
		t.Fatalf("resources = %d %s", resources.Code, resources.Body.String())
	}
	var resourceResponse struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(resources.Body.Bytes(), &resourceResponse); err != nil ||
		!reflect.DeepEqual(resourceResponse.IDs, []string{"agent-1"}) {
		t.Fatalf("resources = %v, %v; want [agent-1]", resourceResponse.IDs, err)
	}

	policies := do(t, r, http.MethodGet,
		"/api/safe/v1/authz/policies?resource_type=agent&resource_id=agent-1", nil)
	if policies.Code != http.StatusOK {
		t.Fatalf("policies = %d %s", policies.Code, policies.Body.String())
	}
	var policyResponse struct {
		Entries []struct {
			AccessorID string   `json:"accessor_id"`
			Operations []string `json:"operations"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(policies.Body.Bytes(), &policyResponse); err != nil ||
		len(policyResponse.Entries) != 1 || policyResponse.Entries[0].AccessorID != user {
		t.Fatalf("policies = %+v, %v", policyResponse, err)
	}
	assertSameOperations(t, policyResponse.Entries[0].Operations, approved)

	mePermissions := tokReq(t, r, http.MethodGet,
		"/api/safe/v1/me/permissions?resource_type=agent&resource_id=agent-1", nil, user)
	if mePermissions.Code != http.StatusOK {
		t.Fatalf("me permissions = %d %s", mePermissions.Code, mePermissions.Body.String())
	}
	var meResponse struct {
		Permissions []struct {
			Operations []string `json:"operations"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(mePermissions.Body.Bytes(), &meResponse); err != nil || len(meResponse.Permissions) != 1 {
		t.Fatalf("me permissions = %+v, %v", meResponse, err)
	}
	assertSameOperations(t, meResponse.Permissions[0].Operations, approved)
}

func assertJSONOperations(t *testing.T, status int, body []byte, want []string) {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("operations = %d %s", status, body)
	}
	var response struct {
		Operations []string `json:"operations"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	assertSameOperations(t, response.Operations, want)
}

func assertSameOperations(t *testing.T, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operations = %v; want %v", got, want)
	}
	for _, op := range got {
		if op == authz.ActFullBusinessAccess {
			t.Fatal("logical full_business_access leaked through an HTTP permission read")
		}
	}
}
