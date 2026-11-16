// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestPermissionProjectionRoleAndNetworkBoundary(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	seedCatalogOps(t, db, "knowledge_network", "view_detail")
	if err := db.Create(&model.Role{ID: "projection-role", Name: "Projection"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission("projection-role", "knowledge_network", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"subject": map[string]string{"type": "role", "id": "projection-role"}, "network_id": "kn-1", "resources": []map[string]string{{"type": "knowledge_network", "id": "kn-1"}}}
	path := "/api/safe/v1/admin/permission-projection"
	if w := tokReq(t, r, http.MethodPost, path, body, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", w.Code)
	}
	w := adminReq(t, r, http.MethodPost, path, body)
	if w.Code != http.StatusOK {
		t.Fatalf("projection = %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Entries []struct {
			Operations []string `json:"operations"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || len(result.Entries[0].Operations) != 1 || result.Entries[0].Operations[0] != "view_detail" {
		t.Fatalf("result = %s", w.Body.String())
	}
	body["resources"] = []map[string]string{{"type": "object_type", "id": "other/orders"}}
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusBadRequest {
		t.Fatalf("cross network = %d", w.Code)
	}
	body["resources"] = []map[string]string{{"type": "knowledge_network", "id": "kn-1"}}
	body["subject"] = map[string]string{"type": "role", "id": "missing-role"}
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusNotFound {
		t.Fatalf("missing role = %d", w.Code)
	}
	body["subject"] = map[string]string{"type": "user", "id": "missing-user"}
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusNotFound {
		t.Fatalf("missing user = %d", w.Code)
	}
}

func TestPermissionProjectionEffectiveDecisionsAndSources(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	seedCatalogOps(t, db, "knowledge_network", "view_detail", "modify", "create")
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: "projection-user", Account: "projection-user", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Role{ID: "source-role", Name: "Source role"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission("source-role", "knowledge_network", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("projection-user", "source-role"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantProfessionalObjectPermission("projection-user", "knowledge_network", "kn-1", "modify", authz.EffectDeny, authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	path := "/api/safe/v1/admin/permission-projection"
	body := map[string]any{"subject": map[string]string{"type": "user", "id": "projection-user"}, "network_id": "kn-1", "resources": []map[string]string{{"type": "knowledge_network", "id": "kn-1"}}, "include_sources": true}
	if w := tokReq(t, r, http.MethodPost, path, body, "projection-user"); w.Code != http.StatusForbidden {
		t.Fatalf("ordinary user = %d", w.Code)
	}
	w := adminReq(t, r, http.MethodPost, path, body)
	if w.Code != http.StatusOK {
		t.Fatalf("projection = %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Entries []struct {
			Operations []string `json:"operations"`
			Decisions  []struct {
				Operation string `json:"operation"`
				Decision  string `json:"decision"`
				Steps     []struct {
					Grants []struct {
						Subject string `json:"subject_id"`
						Kind    string `json:"subject_kind"`
					} `json:"matched_grants"`
				} `json:"steps"`
			} `json:"effective_decisions"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 {
		t.Fatalf("entries = %s", w.Body.String())
	}
	entry := response.Entries[0]
	if len(entry.Operations) != 1 || entry.Operations[0] != "view_detail" {
		t.Fatalf("allowed operations = %v", entry.Operations)
	}
	foundRole, foundDeny := false, false
	for _, decision := range entry.Decisions {
		if decision.Operation == "create" { t.Fatal("instance projection exposed type-level creation") }
		if decision.Operation == "modify" && decision.Decision == "deny" {
			foundDeny = true
		}
		for _, step := range decision.Steps {
			for _, grant := range step.Grants {
				if grant.Subject == "source-role" && grant.Kind == "role" {
					foundRole = true
				}
			}
		}
	}
	if !foundRole || !foundDeny {
		t.Fatalf("missing role source or deny: %s", w.Body.String())
	}
	body["subject"] = map[string]string{"type": "user", "id": adminSub}
	body["include_sources"] = false
	w = adminReq(t, r, http.MethodPost, path, body)
	if w.Code != http.StatusOK {
		t.Fatalf("superadmin = %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 || len(response.Entries[0].Operations) != 2 {
		t.Fatalf("superadmin permissions = %s", w.Body.String())
	}
	if err := db.Model(&model.User{}).Where("id = ?", "projection-user").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	body["subject"] = map[string]string{"type": "user", "id": "projection-user"}
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusNotFound {
		t.Fatalf("disabled user = %d", w.Code)
	}
}

func TestPermissionProjectionInputLimits(t *testing.T) {
	r, _, _, _ := newAdminServer(t)
	resources := make([]map[string]string, 101)
	for i := range resources {
		resources[i] = map[string]string{"type": "knowledge_network", "id": "kn-1"}
	}
	body := map[string]any{"subject": map[string]string{"type": "user", "id": adminSub}, "network_id": "kn-1", "resources": resources}
	path := "/api/safe/v1/admin/permission-projection"
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized = %d", w.Code)
	}
	body["resources"] = resources[:2]
	body["include_sources"] = true
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusBadRequest {
		t.Fatalf("bulk trace = %d", w.Code)
	}
	body["resources"] = []map[string]string{}
	body["include_sources"] = false
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusOK {
		t.Fatalf("empty = %d %s", w.Code, w.Body.String())
	}
	body["resources"] = []map[string]string{{"type": "object_type", "id": "kn-1/*"}}
	if w := adminReq(t, r, http.MethodPost, path, body); w.Code != http.StatusBadRequest {
		t.Fatalf("wildcard = %d", w.Code)
	}
}
