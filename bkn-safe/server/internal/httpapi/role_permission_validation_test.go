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
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestRolePermissionRejectsSubResourceWildcards(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		t.Run(fmt.Sprintf("snapshot=%t", snapshot), func(t *testing.T) {
			r, e, db, _ := newAdminServer(t)
			const roleID = "wildcard-validation"
			const path = "/api/safe/v1/admin/roles/" + roleID + "/permissions"
			if err := db.Create(&model.Role{ID: roleID, Name: roleID, Source: model.RoleSourceCustom}).Error; err != nil {
				t.Fatal(err)
			}
			seedCatalogOps(t, db, "catalog", "view_detail")
			for _, rt := range []struct{ id, parent string }{{"resource", "catalog"}, {"nested_resource", "resource"}} {
				seedCatalogOps(t, db, rt.id, "view_detail", "modify")
				if err := db.Model(&model.ResourceType{}).Where("id = ?", rt.id).Update("parent_type_id", rt.parent).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&model.ResourceParent{ResourceTypeID: "resource", ResourceID: "owned-resource", ParentTypeID: "catalog", ParentID: "owned-catalog"}).Error; err != nil {
				t.Fatal(err)
			}
			if snapshot {
				if err := e.InitializeRegistry(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			err := newAdminWriteServices(e, db).GrantRolePermission(t.Context(), roleID, "resource", "*", "view_detail")
			if !errors.Is(err, adminwrite.ErrSubResourceWildcard) || !errors.Is(err, adminwrite.ErrInvalid) {
				t.Fatalf("service error = %v, want sub-resource validation error", err)
			}
			grant := func(t *testing.T, resourceType, resourceID string, operations []string, status int) {
				t.Helper()
				w := adminReq(t, r, http.MethodPost, path, map[string]any{
					"resource": map[string]string{"type": resourceType, "id": resourceID}, "operations": operations,
				})
				if w.Code != status {
					t.Fatalf("grant %s:%s: %d %s, want %d", resourceType, resourceID, w.Code, w.Body.String(), status)
				}
				if status == http.StatusBadRequest {
					var body struct {
						Code string `json:"error_code"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					if body.Code != "BknSafe.AdminWrite.SubResourceWildcardForbidden" {
						t.Fatalf("error_code = %q", body.Code)
					}
				}
			}
			for _, existing := range []bool{false, true} {
				if existing {
					grant(t, "resource", "owned-resource", []string{"view_detail"}, http.StatusNoContent)
				}
				for _, resourceType := range []string{"resource", "nested_resource"} {
					for _, resourceID := range []string{"*", "prefix-*", "parent/*", "a*b"} {
						t.Run(fmt.Sprintf("existing=%t/%s/%s", existing, resourceType, resourceID), func(t *testing.T) {
							before, err := e.RolePermissions(roleID)
							if err != nil {
								t.Fatal(err)
							}
							var grantsBefore, auditBefore int64
							if err := db.Model(&model.AuthorizationGrant{}).Count(&grantsBefore).Error; err != nil {
								t.Fatal(err)
							}
							if err := db.Model(&model.AuditLog{}).Count(&auditBefore).Error; err != nil {
								t.Fatal(err)
							}
							grant(t, resourceType, resourceID, []string{"view_detail", "modify"}, http.StatusBadRequest)
							after, err := e.RolePermissions(roleID)
							if err != nil || !reflect.DeepEqual(before, after) {
								t.Fatalf("permissions changed: %v -> %v, err=%v", before, after, err)
							}
							var grantsAfter, auditAfter int64
							if err := db.Model(&model.AuthorizationGrant{}).Count(&grantsAfter).Error; err != nil {
								t.Fatal(err)
							}
							if err := db.Model(&model.AuditLog{}).Count(&auditAfter).Error; err != nil {
								t.Fatal(err)
							}
							if grantsBefore != grantsAfter || auditBefore != auditAfter {
								t.Fatal("rejected request persisted grants or audit events")
							}
						})
					}
				}
			}
			grant(t, "resource", "owned-resource", []string{"modify"}, http.StatusNoContent)
			grant(t, "catalog", "*", []string{"view_detail"}, http.StatusNoContent)
			if err := e.AssignRole("wildcard-role-member", roleID); err != nil {
				t.Fatal(err)
			}
			if allowed, err := e.Check("wildcard-role-member", "resource", "owned-resource", "modify"); err != nil || !allowed {
				t.Fatalf("concrete permission: allowed=%t err=%v", allowed, err)
			}
		})
	}
}

func TestRolePermissionPolicyWriteFailureRemainsInternalError(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	const roleID = "policy-write-failure"
	if err := db.Create(&model.Role{ID: roleID, Name: roleID, Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail")
	backendFailure := errors.New("private policy database failure")
	if err := db.Callback().Create().Before("gorm:create").Register("test:policy-write-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "authorization_grant" {
			_ = tx.AddError(backendFailure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:policy-write-failure") })
	err := newAdminWriteServices(e, db).GrantRolePermission(t.Context(), roleID, "catalog", "*", "view_detail")
	if !errors.Is(err, backendFailure) || errors.Is(err, adminwrite.ErrInvalid) {
		t.Fatalf("grant error = %v, want backend failure", err)
	}
	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles/"+roleID+"/permissions", map[string]any{
		"resource": map[string]string{"type": "catalog", "id": "*"}, "operations": []string{"view_detail"},
	})
	var body struct {
		Code string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusInternalServerError || body.Code != httperrors.InternalError || strings.Contains(w.Body.String(), backendFailure.Error()) {
		t.Fatalf("unexpected error response: %d %s", w.Code, w.Body.String())
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
		t.Fatal("failed policy write created an audit event")
	}
}

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
