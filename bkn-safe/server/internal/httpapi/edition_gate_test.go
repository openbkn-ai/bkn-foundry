// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/authzresource"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

// TestPaidTypeGrantsAreRefusedBelowItsEdition covers the write side of #2512:
// below the type's edition every grant route refuses it, revocation still
// works, and the licence is read per request. The type is gated at Enterprise
// so the Professional admin routes stay mounted around it.
func TestPaidTypeGrantsAreRefusedBelowItsEdition(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	edition := licverify.EditionProfessional
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Licensed: true, Edition: edition}
	}))
	authzresource.ResetForTest()
	t.Cleanup(authzresource.ResetForTest)
	authzresource.RequireEdition("paid_report", "paid_report", licverify.EditionEnterprise)
	seedCatalogOps(t, db, "paid_report", "view_detail")
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: "u-1", Account: "alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles", gin.H{"id": "role-paid", "name": "paid"}); w.Code != http.StatusCreated {
		t.Fatalf("create role: %d %s", w.Code, w.Body.String())
	}
	resource := gin.H{"type": "paid_report", "id": "rep-1"}

	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", gin.H{
		"accessor_id": "u-1", "resource": resource, "operations": []string{"view_detail"},
	}); w.Code != http.StatusNotFound {
		t.Fatalf("service policy write = %d %s, want 404", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", gin.H{
		"accessor_id": "u-1", "resource": resource, "operations": []string{"view_detail"},
	}); w.Code != http.StatusNotFound {
		t.Fatalf("object grant = %d %s, want 404", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants/preview", gin.H{
		"accessor_id": "u-1", "resource": resource, "bundle": "full_business_access",
	}); w.Code != http.StatusNotFound {
		t.Fatalf("object grant preview = %d %s, want 404", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles/role-paid/permissions", gin.H{
		"resource": gin.H{"type": "paid_report", "id": "*"}, "operations": []string{"view_detail"},
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("role grant = %d %s, want 400", w.Code, w.Body.String())
	}
	if records, err := e.RolePermissions("role-paid"); err != nil || len(records) != 0 {
		t.Fatalf("role grant persisted: %+v, %v", records, err)
	}

	// A grant kept from a licensed period is inert, and still removable.
	if err := e.GrantObjectPermission("u-1", "paid_report", "rep-2", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.Check("u-1", "paid_report", "rep-2", "view_detail"); ok {
		t.Fatal("a kept grant took effect below the edition")
	}
	if w := do(t, r, http.MethodDelete, "/api/safe/v1/authz/policies", gin.H{
		"resource": gin.H{"type": "paid_report", "id": "rep-2"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s, want 204", w.Code, w.Body.String())
	}
	edition = licverify.EditionEnterprise
	if ok, _ := e.Check("u-1", "paid_report", "rep-2", "view_detail"); ok {
		t.Fatal("the revoked grant survived")
	}

	// At the edition the same writes go through.
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", gin.H{
		"accessor_id": "u-1", "resource": resource, "operations": []string{"view_detail"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("licensed policy write = %d %s, want 204", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-1", "paid_report", "rep-1", "view_detail"); !ok {
		t.Fatal("the licensed grant did not take effect")
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles/role-paid/permissions", gin.H{
		"resource": gin.H{"type": "paid_report", "id": "*"}, "operations": []string{"view_detail"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("licensed role grant = %d %s, want 204", w.Code, w.Body.String())
	}
}

// The workbench's catalog leaves a paid type out below its edition, and its
// instances are not listed, the way an absent module reads.
func TestPaidTypeIsLeftOutOfTheCatalogBelowItsEdition(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	edition := licverify.EditionProfessional
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Licensed: true, Edition: edition}
	}))
	authzresource.ResetForTest()
	t.Cleanup(authzresource.ResetForTest)
	authzresource.RequireEdition("paid_report", "paid_report", licverify.EditionEnterprise)
	seedCatalogOps(t, db, "paid_report", "view_detail")
	seedCatalogOps(t, db, "free_report", "view_detail")
	if err := e.InitializeRegistry(t.Context()); err != nil {
		t.Fatal(err)
	}

	catalog := do(t, r, http.MethodGet, "/api/safe/v1/authz/registry", nil)
	if catalog.Code != http.StatusOK || strings.Contains(catalog.Body.String(), `"paid_report"`) ||
		!strings.Contains(catalog.Body.String(), `"free_report"`) {
		t.Fatalf("catalog below the edition = %d %s", catalog.Code, catalog.Body.String())
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=paid_report", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("instance listing = %d %s, want 400", w.Code, w.Body.String())
	}

	edition = licverify.EditionEnterprise
	catalog = do(t, r, http.MethodGet, "/api/safe/v1/authz/registry", nil)
	if !strings.Contains(catalog.Body.String(), `"paid_report"`) {
		t.Fatalf("catalog at the edition left the type out: %s", catalog.Body.String())
	}
}
