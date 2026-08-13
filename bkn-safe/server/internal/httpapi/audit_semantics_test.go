// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"testing"
)

func TestAuditActionUsesStableBusinessSemantics(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/api/safe/v1/admin/users", "create"},
		{http.MethodPut, "/api/safe/v1/admin/users/:id", "update"},
		{http.MethodPut, "/api/safe/v1/admin/users/:id/password", "reset_password"},
		{http.MethodDelete, "/api/safe/v1/admin/users/:id", "delete"},
		{http.MethodPost, "/api/safe/v1/admin/departments/:id/members", "add_members"},
		{http.MethodDelete, "/api/safe/v1/admin/departments/:id/members", "remove_members"},
		{http.MethodPost, "/api/safe/v1/admin/role-bindings", "bind_role"},
		{http.MethodDelete, "/api/safe/v1/admin/role-bindings", "unbind_role"},
		{http.MethodPost, "/api/safe/v1/admin/object-grants", "grant"},
		{http.MethodDelete, "/api/safe/v1/admin/object-grants", "revoke"},
		{http.MethodPost, "/api/safe/v1/admin/roles/:id/permissions", "grant_permission"},
		{http.MethodDelete, "/api/safe/v1/admin/roles/:id/permissions", "revoke_permission"},
		{http.MethodPost, "/api/safe/v1/admin/license/import", "import"},
		{http.MethodPost, "/api/safe/v1/admin/license/activate", "activate"},
		{http.MethodDelete, "/api/safe/v1/admin/license", "remove"},
		{http.MethodPut, "/api/safe/v1/me", "update_profile"},
	}
	for _, test := range tests {
		if got := auditAction(test.method, test.path); got != test.want {
			t.Errorf("%s %s: action=%q, want %q", test.method, test.path, got, test.want)
		}
	}
}

func TestAuditDetailTargetPromotesBusinessObjectIdentifiers(t *testing.T) {
	tests := []struct {
		resource string
		detail   string
		wantID   string
	}{
		{"object-grants", `{"accessor_id":"user-a","resource":{"type":"knowledge_network","id":"supplychain_hd0202"}}`, "supplychain_hd0202"},
		{"role-bindings", `{"accessor_id":"user-a","role_id":"normal_user"}`, "user-a"},
	}
	for _, test := range tests {
		if got := auditDetailTargetID(test.resource, test.detail); got != test.wantID {
			t.Errorf("resource %q: target id=%q, want %q", test.resource, got, test.wantID)
		}
	}
}
