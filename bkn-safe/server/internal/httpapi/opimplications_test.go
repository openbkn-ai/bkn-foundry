// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// seedCatalogOpImplies registers one operation that carries same-type
// implications, the shape catalog.json gives resource_manage.
func seedCatalogOpImplies(t *testing.T, db *gorm.DB, resourceType, op string, implies string) {
	t.Helper()
	row := model.Operation{
		ResourceTypeID: resourceType, ID: op, Name: op,
		ImpliedOperationIDs: implies,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed op %s/%s: %v", resourceType, op, err)
	}
}

func TestObjectGrantResourceManageImpliesViewDetail(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(),
		&model.User{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
	seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")

	// Granting only resource_manage must still leave the grantee able to open the
	// catalog: every management route loads its target first, and that load is a
	// view_detail judgement, so resource_manage on its own reaches nothing.
	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"resource_manage"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("grant: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	for _, op := range []string{"resource_manage", "view_detail"} {
		if ok, _ := e.Check("u-1", "catalog", "c1", op); !ok {
			t.Fatalf("%s not granted", op)
		}
	}

	// The expansion is reported back, so an operator reading the grant sees what
	// the accessor actually holds rather than what was typed.
	entries := listObjectGrants(t, r, "?accessor_id=u-1")
	if len(entries) != 1 || len(entries[0].Operations) != 2 {
		t.Fatalf("unexpected list: %+v", entries)
	}

	// Replace semantics make it self-healing: a console that clears view_detail
	// while leaving resource_manage ticked sends a set the expansion puts back.
	w = adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"resource_manage"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("re-grant: want 204, got %d", w.Code)
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "view_detail"); !ok {
		t.Fatal("re-grant dropped the implied op")
	}

	// The implication runs one way only: view_detail must not drag the
	// management verb in behind it.
	w = adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"view_detail"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("narrow: want 204, got %d", w.Code)
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "resource_manage"); ok {
		t.Fatal("view_detail granted the implying op")
	}
}

func TestImpliedOpsAndImpliedBy(t *testing.T) {
	_, _, db, _ := newAdminServer(t)
	seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
	seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")
	seedCatalogOps(t, db, "connector_type", "view_detail", "modify")

	got, err := impliedOps(db, "catalog", []string{"resource_manage"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "resource_manage" || got[1] != "view_detail" {
		t.Fatalf("forward closure: %+v", got)
	}

	got, err = impliedBy(db, "catalog", []string{"view_detail"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "view_detail" || got[1] != "resource_manage" {
		t.Fatalf("reverse closure: %+v", got)
	}

	// A type that declares no implications is returned untouched, which is every
	// type but catalog today.
	got, err = impliedOps(db, "connector_type", []string{"modify"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "modify" {
		t.Fatalf("no-implication type: %+v", got)
	}
}
