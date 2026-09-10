// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestAuthzExplainRequiresAdminViewAndShowsRequirementDenial(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(), &model.User{
		ID: "explain-target", Account: "explain-target", Enabled: true,
	}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if err := users.CreateLocalUser(t.Context(), &model.User{
		ID: "ordinary-user", Account: "ordinary-user", Enabled: true,
	}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "catalog", "modify").
		Update("implied_operation_ids", "view_detail").Error; err != nil {
		t.Fatal(err)
	}
	if err := e.GrantProfessionalObjectPermission("explain-target", "catalog", "c-1", "modify",
		authz.EffectAllow, authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantProfessionalObjectPermission("explain-target", "catalog", "c-1", "view_detail",
		authz.EffectDeny, authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"accessor_id": "explain-target",
		"resource":    map[string]any{"type": "catalog", "id": "c-1"},
		"operation":   "modify",
	}
	path := "/api/safe/v1/authz/explain"
	if w := tokReq(t, r, http.MethodPost, path, body, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated explain = %d", w.Code)
	}
	if w := tokReq(t, r, http.MethodPost, path, body, "ordinary-user"); w.Code != http.StatusForbidden {
		t.Fatalf("ordinary explain = %d %s", w.Code, w.Body.String())
	}

	w := adminReq(t, r, http.MethodPost, path, body)
	if w.Code != http.StatusOK {
		t.Fatalf("admin explain = %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Evaluation struct {
			Decision          string `json:"decision"`
			Basis             string `json:"basis"`
			DeniedRequirement string `json:"denied_requirement"`
			RequirementBasis  string `json:"requirement_basis"`
		} `json:"evaluation"`
		Steps []struct {
			MatchedGrants []struct {
				GrantID string `json:"grant_id"`
			} `json:"matched_grants"`
		} `json:"steps"`
		Requirements []json.RawMessage `json:"requirements"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Evaluation.Decision != "deny" || response.Evaluation.Basis != "requires" ||
		response.Evaluation.DeniedRequirement != "view_detail" || response.Evaluation.RequirementBasis != "direct" {
		t.Fatalf("evaluation = %+v", response.Evaluation)
	}
	if len(response.Steps) != 1 || len(response.Steps[0].MatchedGrants) != 1 ||
		response.Steps[0].MatchedGrants[0].GrantID == "" || len(response.Requirements) != 1 {
		t.Fatalf("proof shape = %+v", response)
	}
	if strings.Contains(w.Body.String(), "property_name") || strings.Contains(w.Body.String(), "mask") {
		t.Fatalf("explain leaked a property-level payload: %s", w.Body.String())
	}

	if err := e.GrantCommunityBundle("explain-target", "knowledge_network", "kn-bundle",
		authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	bundleBody := map[string]any{
		"accessor_id": "explain-target",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-bundle"},
		"operation":   "query_data",
	}
	w = adminReq(t, r, http.MethodPost, path, bundleBody)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"basis":"bundle"`) ||
		!strings.Contains(w.Body.String(), `"match":"bundle"`) {
		t.Fatalf("bundle explanation = %d %s", w.Code, w.Body.String())
	}

	if err := db.Create(&model.ResourceType{
		ID: "object_type", Name: "object_type", ParentTypeID: "knowledge_network",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{
		ResourceTypeID: "object_type", ID: "view_detail", Name: "view_detail",
		ParentOperationID: "view_detail",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "object_type", ResourceID: "kn-parent/order",
		ParentTypeID: "knowledge_network", ParentID: "kn-parent",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission("explain-role", "knowledge_network", "kn-parent", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("explain-target", "explain-role"); err != nil {
		t.Fatal(err)
	}
	parentBody := map[string]any{
		"accessor_id": "explain-target",
		"resource":    map[string]any{"type": "object_type", "id": "kn-parent/order"},
		"operation":   "view_detail",
	}
	w = adminReq(t, r, http.MethodPost, path, parentBody)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"basis":"inherited"`) ||
		!strings.Contains(w.Body.String(), `"subject_kind":"role"`) ||
		!strings.Contains(w.Body.String(), `"parent_operation":"view_detail"`) {
		t.Fatalf("role/parent explanation = %d %s", w.Code, w.Body.String())
	}

	inactiveBody := map[string]any{
		"accessor_id": "missing-user",
		"resource":    map[string]any{"type": "catalog", "id": "c-1"},
		"operation":   "view_detail",
	}
	w = adminReq(t, r, http.MethodPost, path, inactiveBody)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"scope":"effective"`) ||
		!strings.Contains(w.Body.String(), `"resource_type":"catalog"`) ||
		!strings.Contains(w.Body.String(), `"account_active":false`) || strings.Contains(w.Body.String(), `"resource":`) {
		t.Fatalf("inactive explanation shape = %d %s", w.Code, w.Body.String())
	}
}
