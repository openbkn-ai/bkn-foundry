// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestRolePermissionRejectsUnregisteredOperations(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		t.Run(fmt.Sprintf("snapshot=%t", snapshot), func(t *testing.T) {
			r, e, db, _ := newAdminServer(t)
			const path = "/api/safe/v1/admin/roles/registry-validation"
			if err := db.Create(&model.Role{ID: "registry-validation", Name: "Registry validation", Source: model.RoleSourceCustom}).Error; err != nil {
				t.Fatal(err)
			}
			seedCatalogOps(t, db, "catalog", "view_detail", "resource_manage")
			seedCatalogOps(t, db, "other", "other_operation")
			if err := db.Model(&model.Operation{}).Where("resource_type_id = ? AND id = ?", "catalog", "resource_manage").Updates(model.Operation{RequiredOperationIDs: "view_detail"}).Error; err != nil {
				t.Fatal(err)
			}
			notGrantable := false
			if err := db.Create(&model.Operation{ResourceTypeID: "catalog", ID: "summary", Grantable: &notGrantable}).Error; err != nil {
				t.Fatal(err)
			}
			if snapshot {
				if err := e.InitializeRegistry(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			readPermissions := func() json.RawMessage {
				t.Helper()
				w := adminReq(t, r, http.MethodGet, path, nil)
				if w.Code != http.StatusOK {
					t.Fatalf("read role: %d %s", w.Code, w.Body.String())
				}
				var body struct {
					Permissions json.RawMessage `json:"permissions"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				return body.Permissions
			}
			for i, tc := range []struct {
				name, resourceType string
				operations         []string
				unknown            string
			}{
				{"unknown", "catalog", []string{"invalid_sdk_operation"}, "invalid_sdk_operation"},
				{"empty operation", "catalog", []string{""}, ""},
				{"whitespace operation", "catalog", []string{" "}, " "},
				{"valid first", "catalog", []string{"resource_manage", "invalid_sdk_operation"}, "invalid_sdk_operation"},
				{"unknown first", "catalog", []string{"invalid_sdk_operation", "resource_manage"}, "invalid_sdk_operation"},
				{"wrong type", "catalog", []string{"other_operation"}, "other_operation"},
				{"unknown type", "missing", []string{"view_detail"}, ""},
				{"not grantable", "catalog", []string{"summary"}, ""},
			} {
				// The first rejection starts empty; later rejections must preserve
				// an existing permission on the same resource as well.
				if i == 1 {
					if err := e.GrantRolePermission("registry-validation", "catalog", "owned-catalog", "view_detail"); err != nil {
						t.Fatal(err)
					}
				}
				t.Run(tc.name, func(t *testing.T) {
					before := readPermissions()
					var auditBefore int64
					if err := db.Model(&model.AuditLog{}).Count(&auditBefore).Error; err != nil {
						t.Fatal(err)
					}
					w := adminReq(t, r, http.MethodPost, path+"/permissions", map[string]any{
						"resource": map[string]string{"type": tc.resourceType, "id": "owned-catalog"}, "operations": tc.operations,
					})
					if w.Code != http.StatusBadRequest {
						t.Fatalf("grant: %d %s", w.Code, w.Body.String())
					}
					var body struct {
						Code    string          `json:"error_code"`
						Details json.RawMessage `json:"error_details"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					if body.Code != "BknSafe.AdminWrite.Invalid" {
						t.Fatalf("error_code = %q", body.Code)
					}
					if tc.name != "not grantable" {
						var details map[string]string
						if err := json.Unmarshal(body.Details, &details); err != nil {
							t.Fatal(err)
						}
						if details["resource_type"] != tc.resourceType || details["operation"] != tc.unknown {
							t.Fatalf("error details = %v", details)
						}
						wantReason := "unregistered_operation"
						if tc.resourceType == "missing" {
							wantReason = "unregistered_resource_type"
						}
						if details["reason"] != wantReason {
							t.Fatalf("reason = %q, want %q", details["reason"], wantReason)
						}
					}
					if after := readPermissions(); !reflect.DeepEqual(before, after) {
						t.Fatalf("permissions changed: %s -> %s", before, after)
					}
					var auditAfter int64
					if err := db.Model(&model.AuditLog{}).Count(&auditAfter).Error; err != nil {
						t.Fatal(err)
					}
					if auditBefore != auditAfter {
						t.Fatal("rejected grant created an audit event")
					}
				})
			}
			w := adminReq(t, r, http.MethodPost, path+"/permissions", map[string]any{
				"resource": map[string]string{"type": "catalog", "id": "owned-catalog"}, "operations": []string{"resource_manage"},
			})
			if w.Code != http.StatusNoContent {
				t.Fatalf("valid grant: %d %s", w.Code, w.Body.String())
			}
			var grants []struct {
				Operations []string `json:"operations"`
			}
			if err := json.Unmarshal(readPermissions(), &grants); err != nil {
				t.Fatal(err)
			}
			if len(grants) != 1 || len(grants[0].Operations) != 2 {
				t.Fatalf("grants with dependencies = %+v", grants)
			}
			for _, op := range []string{"resource_manage", "view_detail"} {
				if ok, err := e.Check("registry-validation", "catalog", "owned-catalog", op); err != nil || !ok {
					t.Fatalf("%s: allowed=%t err=%v", op, ok, err)
				}
			}
		})
	}
}

func TestRolePermissionRegistryFailureRemainsInternalError(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		t.Run(fmt.Sprintf("snapshot=%t", snapshot), func(t *testing.T) {
			r, e, db, _ := newAdminServer(t)
			const roleID = "registry-failure"
			if err := db.Create(&model.Role{ID: roleID, Name: roleID, Source: model.RoleSourceCustom}).Error; err != nil {
				t.Fatal(err)
			}
			backendFailure := errors.New("registry backend unavailable")
			if err := db.Callback().Query().Before("gorm:query").Register("test:registry-failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "resource_types" || tx.Statement.Table == "operations" {
					_ = tx.AddError(backendFailure)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Query().Remove("test:registry-failure") })
			if snapshot {
				if err := e.InitializeRegistry(t.Context()); !errors.Is(err, backendFailure) {
					t.Fatalf("initialize: %v", err)
				}
			}
			err := newAdminWriteServices(e, db).GrantRolePermission(t.Context(), roleID, "catalog", "c-1", "view_detail")
			if !errors.Is(err, backendFailure) || errors.Is(err, adminwrite.ErrInvalid) {
				t.Fatalf("grant error = %v, want backend failure", err)
			}
			w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles/"+roleID+"/permissions", map[string]any{
				"resource": map[string]string{"type": "catalog", "id": "c-1"}, "operations": []string{"view_detail"},
			})
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("grant: %d %s", w.Code, w.Body.String())
			}
			grants, err := e.RolePermissions(roleID)
			if err != nil || len(grants) != 0 {
				t.Fatalf("grants = %v, err = %v", grants, err)
			}
			var count int64
			if err := db.Model(&model.AuditLog{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("registry failure created an audit event")
			}
		})
	}
}
