// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestDepartmentAPIRejectsCyclesWithoutMutationsOrAudit(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	for _, dept := range []model.Department{
		{ID: "root", Name: "Root"},
		{ID: "child", Name: "Child", ParentID: "root"},
		{ID: "alt", Name: "Alt", ParentID: "child"},
		{ID: "leaf", Name: "Leaf", ParentID: "alt"},
	} {
		if err := db.Create(&dept).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, method, path, id, parent, code string
	}{
		{"self", http.MethodPut, "/child", "child", "child", "BknSafe.Department.Cycle"},
		{"child", http.MethodPut, "/child", "child", "alt", "BknSafe.Department.Cycle"},
		{"deep", http.MethodPut, "/child", "child", "leaf", "BknSafe.Department.Cycle"},
		{"missing-parent", http.MethodPut, "/child", "child", "missing", "BknSafe.Department.ParentNotFound"},
		{"create-self", http.MethodPost, "", "new", "new", "BknSafe.Department.Cycle"},
		{"create-missing-parent", http.MethodPost, "", "new", "missing", "BknSafe.Department.ParentNotFound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := adminReq(t, r, tc.method, "/api/safe/v1/admin/departments"+tc.path,
				map[string]any{"id": tc.id, "name": "Changed", "parent_id": tc.parent})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, body=%s", response.Code, response.Body.String())
			}
			var body struct {
				Code        string `json:"error_code"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.code || body.Description == "" {
				t.Fatalf("error=%+v, want code=%s and a readable description", body, tc.code)
			}
		})
	}
	var child model.Department
	if err := db.First(&child, "id = ?", "child").Error; err != nil || child.ParentID != "root" || child.Name != "Child" {
		t.Fatalf("rejected requests changed child: %+v, %v", child, err)
	}
	var count int64
	if err := db.Model(&model.Department{}).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("department count=%d, err=%v", count, err)
	}
	if err := db.Model(&model.AuditLog{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected requests generated business audit logs: count=%d, err=%v", count, err)
	}
	if response := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments", nil); response.Code != http.StatusOK {
		t.Fatalf("list after rejection: %d %s", response.Code, response.Body.String())
	}
}

func TestDepartmentAPIReadsLegacyCycle(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	for _, dept := range []model.Department{
		{ID: "a", Name: "Department A", ParentID: "b"},
		{ID: "b", Name: "Department B", ParentID: "a"},
	} {
		if err := db.Create(&dept).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"", "?parent_id=a", "?limit=1&offset=1"} {
		response := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments"+query, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("list %q: %d %s", query, response.Code, response.Body.String())
		}
	}
}
