// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type checkDecisionResponse struct {
	Allowed           bool     `json:"allowed"`
	EvaluationScope   string   `json:"evaluation_scope"`
	Decision          string   `json:"decision"`
	Basis             string   `json:"basis"`
	Requires          []string `json:"requires"`
	DeniedRequirement string   `json:"denied_requirement"`
	RequirementBasis  string   `json:"requirement_basis"`
}

func TestCheckReturnsOperationRequirementReason(t *testing.T) {
	r, enforcer, db := newTestServer(t)
	const user = "http-requires-user"
	seedEnabledUser(t, db, user)
	seedCatalogOps(t, db, "catalog", "view_detail")
	seedCatalogOpRequires(t, db, "catalog", "resource_manage", "view_detail")
	if err := enforcer.GrantObjectPermission(user, "catalog", "catalog-1", "resource_manage"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.DenyObjectPermission(user, "catalog", "catalog-1", "view_detail"); err != nil {
		t.Fatal(err)
	}

	request := map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "catalog", "id": "catalog-1"},
		"operation":   "resource_manage",
	}
	response := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request)
	if response.Code != http.StatusOK {
		t.Fatalf("effective check = %d %s", response.Code, response.Body.String())
	}
	got := decodeCheckDecision(t, response.Body.Bytes())
	if got.Allowed || got.Decision != "deny" || got.Basis != "requires" ||
		got.DeniedRequirement != "view_detail" || got.RequirementBasis != "direct" ||
		len(got.Requires) != 1 || got.Requires[0] != "view_detail" {
		t.Fatalf("effective check = %+v", got)
	}

	request["evaluation_scope"] = "local"
	response = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request)
	got = decodeCheckDecision(t, response.Body.Bytes())
	if !got.Allowed || got.Decision != "allow" || got.Basis != "direct" ||
		len(got.Requires) != 1 || got.Requires[0] != "view_detail" || got.DeniedRequirement != "" {
		t.Fatalf("local check = %+v", got)
	}

	filterResponse := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":          user,
		"resources":            []map[string]string{{"type": "catalog", "id": "catalog-1"}},
		"candidate_operations": []string{"resource_manage"},
		"evaluation_scope":     "local",
	})
	if filterResponse.Code != http.StatusOK {
		t.Fatalf("local filter = %d %s", filterResponse.Code, filterResponse.Body.String())
	}
	var filtered struct {
		Resources []struct {
			Decisions []struct {
				Operation string   `json:"operation"`
				Decision  string   `json:"decision"`
				Requires  []string `json:"requires"`
			} `json:"decisions"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(filterResponse.Body.Bytes(), &filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered.Resources) != 1 || len(filtered.Resources[0].Decisions) != 2 {
		t.Fatalf("local filter decisions = %s", filterResponse.Body.String())
	}
	byOperation := map[string]struct {
		Decision string
		Requires []string
	}{}
	for _, decision := range filtered.Resources[0].Decisions {
		byOperation[decision.Operation] = struct {
			Decision string
			Requires []string
		}{decision.Decision, decision.Requires}
	}
	if target := byOperation["resource_manage"]; target.Decision != "allow" ||
		len(target.Requires) != 1 || target.Requires[0] != "view_detail" {
		t.Fatalf("resource_manage local metadata = %+v", target)
	}
	if requirement := byOperation["view_detail"]; requirement.Decision != "deny" {
		t.Fatalf("view_detail local decision = %+v", requirement)
	}
}

func decodeCheckDecision(t *testing.T, body []byte) checkDecisionResponse {
	t.Helper()
	var got checkDecisionResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode check decision: %v (%s)", err, body)
	}
	return got
}

func TestCheckEvaluationScopes(t *testing.T) {
	r, enforcer, db := newTestServer(t)
	const user, role = "scope-user", "scope-reader"
	seedEnabledUser(t, db, user)
	if err := enforcer.GrantRolePermission(role, "resource", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AssignRole(user, role); err != nil {
		t.Fatal(err)
	}

	request := map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "resource", "id": "r-1"},
		"operation":   "view_detail",
	}
	response := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request)
	if response.Code != http.StatusOK {
		t.Fatalf("default check = %d %s", response.Code, response.Body.String())
	}
	got := decodeCheckDecision(t, response.Body.Bytes())
	if !got.Allowed || got.EvaluationScope != "effective" || got.Decision != "allow" || got.Basis != "wildcard" {
		t.Fatalf("default check = %+v", got)
	}

	request["operation"] = "query_data"
	request["evaluation_scope"] = "local"
	response = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request)
	got = decodeCheckDecision(t, response.Body.Bytes())
	if got.Allowed || got.EvaluationScope != "local" || got.Decision != "none" || got.Basis != "none" {
		t.Fatalf("local miss = %+v", got)
	}

	delete(request, "evaluation_scope")
	response = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request)
	got = decodeCheckDecision(t, response.Body.Bytes())
	if got.Allowed || got.Decision != "deny" || got.Basis != "default" {
		t.Fatalf("effective miss = %+v", got)
	}

	request["evaluation_scope"] = "unknown"
	response = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown scope = %d, want 400 (%s)", response.Code, response.Body.String())
	}
}

func TestResourceFilterLocalReturnsEveryDecision(t *testing.T) {
	r, enforcer, db := newTestServer(t)
	const user, role = "filter-scope-user", "filter-scope-reader"
	seedEnabledUser(t, db, user)
	if err := enforcer.GrantRolePermission(role, "resource", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AssignRole(user, role); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.DenyObjectPermission(user, "resource", "r-1", "view_detail"); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"accessor_id":           user,
		"resource_type":         "resource",
		"resource_ids":          []string{"r-1", "r-2"},
		"visibility_operations": []string{"view_detail"},
		"candidate_operations":  []string{"view_detail", "query_data"},
		"evaluation_scope":      "local",
	}
	response := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", body)
	if response.Code != http.StatusOK {
		t.Fatalf("local filter = %d %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Resources []struct {
			ResourceID string   `json:"resource_id"`
			Operations []string `json:"operations"`
			Decisions  []struct {
				Operation string `json:"operation"`
				Decision  string `json:"decision"`
				Basis     string `json:"basis"`
			} `json:"decisions"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Resources) != 2 {
		t.Fatalf("local filter returned %d resources, want every input: %s", len(decoded.Resources), response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"operations":null`) {
		t.Fatalf("local filter must encode an empty operation set as []: %s", response.Body.String())
	}
	byID := map[string]map[string][2]string{}
	for _, resource := range decoded.Resources {
		byID[resource.ResourceID] = map[string][2]string{}
		for _, decision := range resource.Decisions {
			byID[resource.ResourceID][decision.Operation] = [2]string{decision.Decision, decision.Basis}
		}
	}
	if got := byID["r-1"]["view_detail"]; got != [2]string{"deny", "direct"} {
		t.Fatalf("r-1 view_detail = %v", got)
	}
	if got := byID["r-2"]["view_detail"]; got != [2]string{"allow", "wildcard"} {
		t.Fatalf("r-2 view_detail = %v", got)
	}
	if got := byID["r-2"]["query_data"]; got != [2]string{"none", "none"} {
		t.Fatalf("r-2 query_data = %v", got)
	}

	delete(body, "evaluation_scope")
	response = do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", body)
	if response.Code != http.StatusOK {
		t.Fatalf("effective filter = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"decisions"`) {
		t.Fatalf("default response must preserve the old shape: %s", response.Body.String())
	}
}

func TestLocalScopeUsesInternalTrustBoundaryAndValidatesShape(t *testing.T) {
	r, enforcer, db := newTestServer(t)
	seedEnabledUser(t, db, "local-user")
	if err := enforcer.GrantObjectPermission("local-user", "resource", "resource-1", "view_detail"); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"accessor_id":      "local-user",
		"resource":         map[string]string{"type": "resource", "id": "resource-1"},
		"operation":        "view_detail",
		"evaluation_scope": "local",
	}

	withoutHeader := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request)
	withForgedHeader := doWithCallerService(t, r, http.MethodPost, "/api/safe/v1/authz/check", request, "not-vega")
	if withoutHeader.Code != http.StatusOK || withForgedHeader.Code != http.StatusOK ||
		withoutHeader.Body.String() != withForgedHeader.Body.String() {
		t.Fatalf("caller-service header changed local decision: missing=%d %s forged=%d %s",
			withoutHeader.Code, withoutHeader.Body.String(), withForgedHeader.Code, withForgedHeader.Body.String())
	}

	request["resource"] = map[string]string{"type": "catalog", "id": "catalog-1"}
	request["operation"] = "resource_manage"
	if response := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request); response.Code != http.StatusOK {
		t.Fatalf("approved Catalog operation = %d %s, want 200", response.Code, response.Body.String())
	}

	request["resource"] = map[string]string{"type": "object_type", "id": "kn-1/type-1"}
	request["operation"] = "view_detail"
	if response := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request); response.Code != http.StatusBadRequest {
		t.Fatalf("non-Vega resource = %d %s, want 400", response.Code, response.Body.String())
	}
	request["resource"] = map[string]string{"type": "resource", "id": "*"}
	if response := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request); response.Code != http.StatusBadRequest {
		t.Fatalf("wildcard Resource id = %d %s, want 400", response.Code, response.Body.String())
	}
	request["resource"] = map[string]string{"type": "resource", "id": "resource-1"}
	request["operation"] = "modify"
	if response := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request); response.Code != http.StatusBadRequest {
		t.Fatalf("unapproved Resource operation = %d %s, want 400", response.Code, response.Body.String())
	}

	delete(request, "evaluation_scope")
	if response := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", request); response.Code != http.StatusOK {
		t.Fatalf("effective compatibility = %d %s, want 200", response.Code, response.Body.String())
	}
}

func TestLocalResourceFilterRejectsAnyOutOfBoundaryDecision(t *testing.T) {
	r, _, db := newTestServer(t)
	seedEnabledUser(t, db, "filter-local-user")
	body := map[string]any{
		"accessor_id":          "filter-local-user",
		"resources":            []map[string]string{{"type": "resource", "id": "resource-1"}},
		"candidate_operations": []string{"view_detail", "modify"},
		"evaluation_scope":     "local",
	}
	response := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("mixed approved/unapproved local batch = %d %s, want 400", response.Code, response.Body.String())
	}
}
