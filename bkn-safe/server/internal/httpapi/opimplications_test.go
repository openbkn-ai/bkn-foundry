// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
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

// TestRolePermissionImplicationDirections pins the role write path, which the
// object-grant surface cannot stand in for: role grants and revokes arrive one
// operation at a time, so each direction has to be decided on its own rather
// than falling out of a whole-set replace.
func TestRolePermissionImplicationDirections(t *testing.T) {
	_, e, db, _ := newAdminServer(t)
	seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
	seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")
	if err := db.Create(&model.Role{ID: "r-1", Name: "custom", Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}
	svc := newAdminWriteServices(e, db)
	ctx := t.Context()

	if err := svc.GrantRolePermission(ctx, "r-1", "catalog", "c1", "resource_manage"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	for _, op := range []string{"resource_manage", "view_detail"} {
		if ok, _ := e.Check("r-1", "catalog", "c1", op); !ok {
			t.Fatalf("%s not granted to the role", op)
		}
	}

	// Revoking the management verb leaves view_detail standing. It is a
	// permission in its own right — "may open this catalog but no longer manage
	// the tables in it" is the end state an operator asking for exactly this
	// revoke wants, and it is also what the object-grant surface produces when a
	// console re-saves the same set without resource_manage.
	if err := svc.RevokeRolePermission(ctx, "r-1", "catalog", "c1", "resource_manage"); err != nil {
		t.Fatalf("revoke resource_manage: %v", err)
	}
	if ok, _ := e.Check("r-1", "catalog", "c1", "resource_manage"); ok {
		t.Fatal("resource_manage survived its own revoke")
	}
	if ok, _ := e.Check("r-1", "catalog", "c1", "view_detail"); !ok {
		t.Fatal("revoking resource_manage took view_detail with it")
	}

	// Revoking view_detail while resource_manage is held does NOT remove it: the
	// operation is still implied, and dropping it would leave a verb whose every
	// route answers 403. This is what the whole-set object-grant surface already
	// does for the same edit, and it makes the outcome independent of the order
	// a console sends its pair of requests in — see the sequence below.
	if err := svc.GrantRolePermission(ctx, "r-1", "catalog", "c1", "resource_manage"); err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if err := svc.RevokeRolePermission(ctx, "r-1", "catalog", "c1", "view_detail"); err != nil {
		t.Fatalf("revoke view_detail: %v", err)
	}
	for _, op := range []string{"view_detail", "resource_manage"} {
		if ok, _ := e.Check("r-1", "catalog", "c1", op); !ok {
			t.Fatalf("%s was dropped by a revoke that had to be refused", op)
		}
	}

	// Revoking the implying verb first is honoured immediately, and view_detail
	// can then be revoked too, because nothing implies it any more.
	if err := svc.RevokeRolePermission(ctx, "r-1", "catalog", "c1", "resource_manage"); err != nil {
		t.Fatalf("revoke resource_manage: %v", err)
	}
	if err := svc.RevokeRolePermission(ctx, "r-1", "catalog", "c1", "view_detail"); err != nil {
		t.Fatalf("revoke view_detail after: %v", err)
	}
	for _, op := range []string{"view_detail", "resource_manage"} {
		if ok, _ := e.Check("r-1", "catalog", "c1", op); ok {
			t.Fatalf("%s survived an unblocked revoke", op)
		}
	}
}

// TestRolePermissionSaveOrderDoesNotChangeTheResult is the case review raised:
// a console saving a permission diff as "grant resource_manage" plus "revoke
// view_detail" must not end up with neither, silently discarding the grant it
// just made. Both orders must land on the same held set.
func TestRolePermissionSaveOrderDoesNotChangeTheResult(t *testing.T) {
	for _, tc := range []struct {
		name        string
		revokeFirst bool
	}{
		{"grant then revoke", false},
		{"revoke then grant", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, db, _ := newAdminServer(t)
			seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
			seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")
			if err := db.Create(&model.Role{ID: "r-2", Name: "custom", Source: model.RoleSourceCustom}).Error; err != nil {
				t.Fatal(err)
			}
			svc := newAdminWriteServices(e, db)
			ctx := t.Context()

			grant := func() error {
				return svc.GrantRolePermission(ctx, "r-2", "catalog", "c1", "resource_manage")
			}
			revoke := func() error {
				return svc.RevokeRolePermission(ctx, "r-2", "catalog", "c1", "view_detail")
			}
			steps := []func() error{grant, revoke}
			if tc.revokeFirst {
				steps = []func() error{revoke, grant}
			}
			for i, step := range steps {
				if err := step(); err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
			}
			for _, op := range []string{"resource_manage", "view_detail"} {
				if ok, _ := e.Check("r-2", "catalog", "c1", op); !ok {
					t.Fatalf("%s missing after %s", op, tc.name)
				}
			}
		})
	}
}

// TestObjectGrantAuditNamesTheImpliedOperation: the audit Detail snapshots the
// request body, so an implied operation would otherwise land on the accessor
// with nothing in the trail saying where it came from.
func TestObjectGrantAuditNamesTheImpliedOperation(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(),
		&model.User{ID: "u-9", Account: "carol", Name: "Carol", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
	seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")

	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-9",
		"resource":    map[string]any{"type": "catalog", "id": "c7"},
		"operations":  []string{"resource_manage"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("grant: want 204, got %d (%s)", w.Code, w.Body.String())
	}

	entries, total, err := audit.New(db).List(t.Context(), audit.Filter{
		Resource: "object-grants", Action: "grant", TargetID: "c7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("audit rows = %d, want 1", total)
	}
	if !strings.Contains(entries[0].Detail, "implied_operations") ||
		!strings.Contains(entries[0].Detail, "view_detail") {
		t.Fatalf("audit detail does not name the implied operation: %s", entries[0].Detail)
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

// TestInternalPolicyGrantExpandsImplications covers the third write face. It is
// the one a service calls directly, and it was missed on the first pass: found
// by driving a real deployment, where a grant written through it came back
// carrying resource_manage alone.
func TestInternalPolicyGrantExpandsImplications(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
	seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")

	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", map[string]any{
		"accessor_id": "svc-created",
		"resource":    map[string]any{"type": "catalog", "id": "c9"},
		"operations":  []string{"resource_manage"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("grant: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	for _, op := range []string{"resource_manage", "view_detail"} {
		if ok, _ := e.Check("svc-created", "catalog", "c9", op); !ok {
			t.Fatalf("%s not granted", op)
		}
	}
}

// TestRevokeSetIsOrderIndependent is the case the second review round raised:
// judging one operation at a time against the LIVE state makes a
// multi-operation revoke depend on the order the caller listed them, so
// ["view_detail", "resource_manage"] left view_detail behind while the reverse
// order dropped both. The verdict is computed against what the role is left
// with, so both orders now drop both.
func TestRevokeSetIsOrderIndependent(t *testing.T) {
	for _, ops := range [][]string{
		{"view_detail", "resource_manage"},
		{"resource_manage", "view_detail"},
	} {
		t.Run(strings.Join(ops, ","), func(t *testing.T) {
			_, e, db, _ := newAdminServer(t)
			seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
			seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")
			if err := db.Create(&model.Role{ID: "r-3", Name: "custom", Source: model.RoleSourceCustom}).Error; err != nil {
				t.Fatal(err)
			}
			svc := newAdminWriteServices(e, db)
			revoker := svc.(adminwrite.RoleSetRevoker)
			ctx := t.Context()
			if err := svc.GrantRolePermission(ctx, "r-3", "catalog", "c1", "resource_manage"); err != nil {
				t.Fatal(err)
			}
			if err := revoker.RevokeRolePermissions(ctx, "r-3", "catalog", "c1", ops); err != nil {
				t.Fatalf("revoke %v: %v", ops, err)
			}
			for _, op := range []string{"view_detail", "resource_manage"} {
				if ok, _ := e.Check("r-3", "catalog", "c1", op); ok {
					t.Fatalf("%s survived revoke of %v", op, ops)
				}
			}
		})
	}
}

// TestRevokeSetKeepsAnOperationTheRemainderImplies is the other half: dropping
// only view_detail while resource_manage is kept must leave view_detail in
// place, or the role is left holding a verb that answers 403 everywhere.
func TestRevokeSetKeepsAnOperationTheRemainderImplies(t *testing.T) {
	_, e, db, _ := newAdminServer(t)
	seedCatalogOps(t, db, "catalog", "view_detail", "query_data")
	seedCatalogOpImplies(t, db, "catalog", "resource_manage", "view_detail")
	if err := db.Create(&model.Role{ID: "r-4", Name: "custom", Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}
	svc := newAdminWriteServices(e, db)
	revoker := svc.(adminwrite.RoleSetRevoker)
	ctx := t.Context()
	if err := svc.GrantRolePermission(ctx, "r-4", "catalog", "c1", "resource_manage"); err != nil {
		t.Fatal(err)
	}
	if err := revoker.RevokeRolePermissions(ctx, "r-4", "catalog", "c1", []string{"view_detail"}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"view_detail", "resource_manage"} {
		if ok, _ := e.Check("r-4", "catalog", "c1", op); !ok {
			t.Fatalf("%s dropped by a revoke the invariant had to refuse", op)
		}
	}
}
