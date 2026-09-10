// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

func TestValidateRequirementsRejectsAuthoringMistakes(t *testing.T) {
	cases := []struct {
		name    string
		catalog catalog
		wantErr string
	}{
		{
			name: "requires an operation the type does not declare",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", Operations: []catalogOperation{
					{ID: "resource_manage", Requires: []string{"view_detail"}},
				}},
			}},
			wantErr: "does not declare",
		},
		{
			name: "self requirement",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", Operations: []catalogOperation{
					{ID: "resource_manage", Requires: []string{"resource_manage"}},
				}},
			}},
			wantErr: "requires itself",
		},
		{
			name: "multi-level requirement",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", Operations: []catalogOperation{
					{ID: "a", Requires: []string{"b"}},
					{ID: "b", Requires: []string{"c"}},
					{ID: "c"},
				}},
			}},
			wantErr: "itself declares requires",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRequirements(tc.catalog)
			if err == nil {
				t.Fatalf("validateRequirements accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateRequirementsAcceptsMultipleDirectPrerequisites(t *testing.T) {
	c := catalog{ResourceTypes: []catalogResourceType{
		{ID: "release", Operations: []catalogOperation{
			{ID: "view"},
			{ID: "approve"},
			{ID: "publish", Requires: []string{"view", "approve"}},
		}},
	}}
	if err := validateRequirements(c); err != nil {
		t.Fatalf("valid direct requirements were rejected: %v", err)
	}
}

// TestShippedCatalogBindsResourceManageToViewDetail is the regression guard on
// the product rule (#1121): managing the tables in a catalog is unreachable
// without the right to open the catalog, because every management route loads
// its target first and that load is a view_detail judgement. Dropping the
// missing requirement would let the console hand out a grant whose every route answers
// 403 while naming a permission the operator never meant to withhold.
func TestShippedCatalogBindsResourceManageToViewDetail(t *testing.T) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		t.Fatalf("parse catalog.json: %v", err)
	}
	if err := validateRequirements(c); err != nil {
		t.Fatalf("shipped catalog.json declares an invalid requirement: %v", err)
	}
	for _, rt := range c.ResourceTypes {
		if rt.ID != "catalog" {
			continue
		}
		for _, op := range rt.Operations {
			if op.ID != "resource_manage" {
				continue
			}
			if len(op.Requires) != 1 || op.Requires[0] != "view_detail" {
				t.Fatalf("catalog.resource_manage requires %v, want [view_detail]", op.Requires)
			}
			return
		}
		t.Fatal("catalog type no longer declares resource_manage")
	}
	t.Fatal("catalog type missing from catalog.json")
}

// TestSeedPersistsImplications proves the declaration survives the seed, since
// the grant paths read it from the operations table rather than from the file.
func TestSeedPersistsImplications(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var row model.Operation
	if err := db.First(&row, "resource_type_id = ? AND id = ?", "catalog", "resource_manage").Error; err != nil {
		t.Fatalf("load operation: %v", err)
	}
	if row.RequiredOperationIDs != "view_detail" {
		t.Fatalf("required operation ids = %q, want %q", row.RequiredOperationIDs, "view_detail")
	}
}

// TestBackfillRepairsGrantsWrittenBeforeTheRule is the reason the write-time
// rule alone is not a fix: a grant made before it existed carries only
// resource_manage, reaches nothing, and nobody tells the administrator to
// re-save it.
func TestBackfillRepairsGrantsWrittenBeforeTheRule(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	t.Cleanup(entitlement.ResetForTest)
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The shape an operator could produce before #1121: management without the
	// visibility every management route needs.
	if err := e.GrantProfessionalObjectPermission(
		"u-1", "catalog", "c1", "resource_manage", authz.EffectAllow, authz.AuthoritySourceAdminAuthz,
	); err != nil {
		t.Fatal(err)
	}
	// An unrelated grant on the same type must come through untouched.
	if err := e.GrantProfessionalObjectPermission(
		"u-2", "catalog", "c2", "query_data", authz.EffectAllow, authz.AuthoritySourceAdminAuthz,
	); err != nil {
		t.Fatal(err)
	}

	if err := Apply(db, e); err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	if ok, _ := e.Check("u-1", "catalog", "c1", "view_detail"); !ok {
		t.Fatal("backfill did not repair the pre-existing grant")
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "resource_manage"); !ok {
		t.Fatal("backfill dropped the operation it was repairing")
	}
	if ok, _ := e.Check("u-2", "catalog", "c2", "view_detail"); ok {
		t.Fatal("backfill widened a grant that requires nothing")
	}

	// Idempotent: a start with nothing left to repair changes nothing.
	if err := Apply(db, e); err != nil {
		t.Fatalf("third apply: %v", err)
	}
	grants, err := e.ListObjectGrants("u-1", "catalog", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || len(grants[0].Operations) != 2 {
		t.Fatalf("grant after repeated apply: %+v", grants)
	}

	// The repair is a permission change nobody asked for at the console, so it
	// has to be findable there — one row per repaired grant, under the same
	// resource/action an operator-issued object grant carries, and not repeated
	// on the idempotent starts that followed.
	entries, total, err := audit.New(db).List(t.Context(), audit.Filter{
		Resource: "object-grants", Action: "grant",
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("audit rows for the backfill = %d, want 1", total)
	}
	got := entries[0]
	if got.ActorID != "system:seed" || got.TargetID != "c1" {
		t.Fatalf("audit row = %+v", got)
	}
	if !strings.Contains(got.Detail, "view_detail") || !strings.Contains(got.Detail, "resource_manage") {
		t.Fatalf("audit detail does not name the operations: %s", got.Detail)
	}
}

// TestBackfillAuditLabelsMatchTheSurfaceThatShowsTheGrant pins the vocabulary
// per subject kind. An audit row naming a surface where the grant is absent is
// worse than no row: it sends the auditor looking in a list that by
// construction will never contain it. GET /object-grants excludes role
// subjects, the public accessor and type-wide "type:*" rows alike.
func TestBackfillAuditLabelsMatchTheSurfaceThatShowsTheGrant(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	t.Cleanup(entitlement.ResetForTest)
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := db.Create(&model.Role{ID: "role-x", Name: "自定义角色", Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}

	// One row of each shape, all holding the management verb without the
	// visibility it requires.
	if err := e.GrantProfessionalObjectPermission(
		"u-1", "catalog", "c1", "resource_manage", authz.EffectAllow, authz.AuthoritySourceAdminAuthz,
	); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission("role-x", "catalog", "*", "resource_manage"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantSystemObjectPermission(authz.PublicAccessorID, "catalog", "c2", "resource_manage"); err != nil {
		t.Fatal(err)
	}

	if err := Apply(db, e); err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	trail := audit.New(db)
	for _, tc := range []struct {
		name             string
		resource, action string
		targetID         string
	}{
		{"user object grant", "object-grants", "grant", "c1"},
		{"role permission", "roles", "grant_permission", "role-x"},
		{"public accessor is not an object grant", "policies", "grant", "c2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, total, err := trail.List(t.Context(), audit.Filter{
				Resource: tc.resource, Action: tc.action, TargetID: tc.targetID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if total != 1 {
				t.Fatalf("rows = %d, want 1", total)
			}
			if entries[0].ActorID != "system:seed" {
				t.Fatalf("actor = %q", entries[0].ActorID)
			}
		})
	}
}
