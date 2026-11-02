package sso

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestCasdoorGroupHierarchyUpdatesExistingDepartments(t *testing.T) {
	t.Setenv("TEST_CASDOOR_SECRET", "test-secret")
	parentName := "Parent display"
	parentParent := "organization_openbkn"
	apiCalls := 0
	s, provider, claims := identityFixtureWithAPI(t, func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		client, secret, ok := r.BasicAuth()
		if !ok || client != "safe-client" || secret != "test-secret" {
			t.Error("group API must authenticate with client credentials in the header")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var obj any
		switch r.URL.Path + "?" + r.URL.Query().Get("id") {
		case "/api/get-group?organization_openbkn/department-1-1":
			obj = map[string]string{"owner": "organization_openbkn", "name": "department-1-1", "displayName": "Child display", "parentId": "department-1"}
		case "/api/get-group?organization_openbkn/department-1":
			obj = map[string]string{"owner": "organization_openbkn", "name": "department-1", "displayName": parentName, "parentId": parentParent}
		case "/api/get-group?organization_openbkn/new-parent":
			obj = map[string]string{"owner": "organization_openbkn", "name": "new-parent", "displayName": "New parent", "parentId": "organization_openbkn"}
		case "/api/get-organization?admin/organization_openbkn":
			obj = map[string]string{"owner": "admin", "name": "organization_openbkn", "displayName": "Organization display"}
		default:
			t.Errorf("unexpected API request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": obj})
	})
	provider.Adapter, provider.ClientID, provider.ClientSecretEnv = "casdoor", "safe-client", "TEST_CASDOOR_SECRET"
	claims["groups"] = []string{"organization_openbkn/department-1-1", "organization_openbkn/department-1"}
	for _, name := range []string{"department-1", "department-1-1"} {
		dept := model.Department{ID: name, Name: "organization_openbkn/" + name}
		mapping := model.SSODepartment{ID: "map-" + name, ProviderID: provider.ID, ExternalID: dept.Name, DeptID: dept.ID}
		if err := s.db.Create(&dept).Error; err != nil {
			t.Fatal(err)
		}
		if err := s.db.Create(&mapping).Error; err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.ResolveUser(t.Context(), provider, "user-token", "user1-subject")
	if err != nil {
		t.Fatal(err)
	}
	var parent, child, root model.Department
	if err := s.db.First(&parent, "id = ?", "department-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.First(&child, "id = ?", "department-1-1").Error; err != nil {
		t.Fatal(err)
	}
	if parent.Name != parentName || child.Name != "Child display" || child.ParentID != parent.ID || parent.ParentID == "" {
		t.Fatalf("source hierarchy and display names not applied: parent=%+v child=%+v", parent, child)
	}
	if err := s.db.First(&root, "id = ?", parent.ParentID).Error; err != nil {
		t.Fatal(err)
	}
	if root.Name != "Organization display" || root.ParentID != "" || apiCalls != 3 {
		t.Fatalf("unexpected organization root or duplicate fetches: root=%+v calls=%d", root, apiCalls)
	}
	var count int64
	if err := s.db.Model(&model.UserDepartment{}).Where("user_id = ?", u.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("direct memberships should not include ancestors: count=%d err=%v", count, err)
	}
	parentName = "Renamed parent"
	parentParent = "organization_openbkn/new-parent"
	claims["groups"] = []string{"organization_openbkn/department-1-1"}
	if _, err := s.ResolveUser(t.Context(), provider, "user-token", "user1-subject"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.First(&parent, "id = ?", "department-1").Error; err != nil || parent.Name != parentName {
		t.Fatalf("rename must retain department ID: parent=%+v err=%v", parent, err)
	}
	var newParent model.Department
	if err := s.db.First(&newParent, "id = ?", parent.ParentID).Error; err != nil || newParent.Name != "New parent" {
		t.Fatalf("qualified parent ID was not applied: parent=%+v err=%v", newParent, err)
	}
	if err := s.db.Model(&model.UserDepartment{}).Where("user_id = ?", u.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("ancestor must not become a direct membership: count=%d err=%v", count, err)
	}
	if err := auth.NewUserStore(s.db).DeleteUser(t.Context(), u.ID); err != nil {
		t.Fatal(err)
	}
	if err := directory.New(s.db).DeleteDepartment(t.Context(), child.ID); err != nil {
		t.Fatal(err)
	}
	u, err = s.ResolveUser(t.Context(), provider, "user-token", "user1-subject")
	if err != nil {
		t.Fatalf("login after deleting user and department: %v", err)
	}
	var recreated model.SSODepartment
	if err := s.db.First(&recreated, "provider_id = ? AND external_id = ?", provider.ID, "organization_openbkn/department-1-1").Error; err != nil {
		t.Fatal(err)
	}
	if recreated.DeptID == child.ID {
		t.Fatal("deleted department ID was reused")
	}
	var recreatedDept model.Department
	if err := s.db.First(&recreatedDept, "id = ?", recreated.DeptID).Error; err != nil || recreatedDept.ParentID != parent.ID {
		t.Fatalf("recreated department lost hierarchy: dept=%+v err=%v", recreatedDept, err)
	}
}

func TestCasdoorInvalidHierarchyPreservesExistingUserAndMemberships(t *testing.T) {
	for _, failure := range []string{"cycle", "cross organization parent", "wrong object", "missing object", "API forbidden", "redirect"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("TEST_CASDOOR_SECRET", "test-secret")
			broken := false
			s, provider, _ := identityFixtureWithAPI(t, func(w http.ResponseWriter, r *http.Request) {
				obj := map[string]string{"owner": "organization_openbkn", "name": "department-1", "displayName": "Original department", "parentId": "organization_openbkn"}
				if r.URL.Path == "/api/get-organization" {
					obj = map[string]string{"owner": "admin", "name": "organization_openbkn", "displayName": "Organization"}
				} else if r.URL.Path != "/api/get-group" {
					t.Errorf("unexpected API or followed redirect: %s", r.URL.Path)
					w.WriteHeader(http.StatusForbidden)
					return
				} else if broken {
					switch failure {
					case "cycle":
						obj["parentId"] = "department-1"
					case "cross organization parent":
						obj["parentId"] = "other-organization/parent"
					case "wrong object":
						obj["name"] = "wrong-group"
					case "missing object":
						obj = nil
					case "API forbidden":
						w.WriteHeader(http.StatusForbidden)
						return
					case "redirect":
						http.Redirect(w, r, "/credentials-must-not-be-forwarded", http.StatusFound)
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": obj})
			})
			provider.Adapter, provider.ClientID, provider.ClientSecretEnv = "casdoor", "safe-client", "TEST_CASDOOR_SECRET"
			u, err := s.ResolveUser(t.Context(), provider, "user-token", "user1-subject")
			if err != nil {
				t.Fatal(err)
			}
			broken = true
			if _, err := s.ResolveUser(t.Context(), provider, "user-token", "user1-subject"); err == nil {
				t.Fatal("expected invalid source hierarchy to be rejected")
			}
			var membership model.UserDepartment
			if err := s.db.First(&membership, "user_id = ?", u.ID).Error; err != nil {
				t.Fatalf("source failure removed existing membership: %v", err)
			}
			var dept model.Department
			if err := s.db.First(&dept, "id = ?", membership.DepartmentID).Error; err != nil || dept.Name != "Original department" {
				t.Fatalf("source failure changed department: dept=%+v err=%v", dept, err)
			}
		})
	}
}
