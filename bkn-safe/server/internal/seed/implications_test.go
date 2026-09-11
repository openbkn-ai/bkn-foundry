// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"encoding/json"
	"reflect"
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

func TestShippedConnectorTypeDeclaresViewRequirements(t *testing.T) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		t.Fatalf("parse catalog.json: %v", err)
	}
	if err := validateRequirements(c); err != nil {
		t.Fatalf("shipped catalog.json declares an invalid requirement: %v", err)
	}

	operations := map[string][]string{}
	for _, resourceType := range c.ResourceTypes {
		if resourceType.ID != "connector_type" {
			continue
		}
		for _, operation := range resourceType.Operations {
			operations[operation.ID] = operation.Requires
		}
	}
	for _, operation := range []string{"modify", "delete", "authorize"} {
		if got := operations[operation]; len(got) != 1 || got[0] != "view_detail" {
			t.Errorf("connector_type/%s requires %v, want [view_detail]", operation, got)
		}
	}
	for _, operation := range []string{"create", "view_detail"} {
		if got := operations[operation]; len(got) != 0 {
			t.Errorf("connector_type/%s unexpectedly requires %v", operation, got)
		}
	}
	if _, ok := operations["task_manage"]; ok {
		t.Error("connector_type unexpectedly declares task_manage without a task lifecycle entry")
	}
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

func TestConnectorTypeRequirementsApplyToChecksAndLists(t *testing.T) {
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

	const user = "connector-operator"
	if err := e.GrantProfessionalObjectPermission(
		user, "connector_type", "remote-api", "modify", authz.EffectAllow, authz.AuthoritySourceAdminAuthz,
	); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantProfessionalObjectPermission(
		user, "connector_type", "remote-api", "view_detail", authz.EffectAllow, authz.AuthoritySourceAdminAuthz,
	); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantProfessionalObjectPermission(
		user, "connector_type", "remote-api", "view_detail", authz.EffectDeny, authz.AuthoritySourceAdminAuthz,
	); err != nil {
		t.Fatal(err)
	}

	decision, err := e.OperationDecision(t.Context(), user, "connector_type", "remote-api", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != authz.DecisionDeny || decision.Basis != authz.BasisRequires ||
		decision.DeniedRequirement != "view_detail" {
		t.Fatalf("modify decision = %+v; want requires deny on view_detail", decision)
	}
	ids, err := e.AccessibleResources(user, "connector_type", "modify")
	if err != nil || len(ids) != 0 {
		t.Fatalf("AccessibleResources(modify) = %v, %v; want none", ids, err)
	}
	filtered, err := e.FilterResourceOps(user,
		[]authz.ResourceRef{{Type: "connector_type", ID: "remote-api"}}, nil, []string{"modify"})
	if err != nil || len(filtered) != 1 || len(filtered[0].Operations) != 0 ||
		len(filtered[0].Decisions) != 1 || filtered[0].Decisions[0].Basis != authz.BasisRequires {
		t.Fatalf("FilterResourceOps(modify) = %+v, %v", filtered, err)
	}

	// Legacy grants are immutable snapshots, so startup deliberately does not
	// add view_detail beside a type-wide modify grant. Effective evaluation must
	// still combine that grant with view_detail held on a concrete connector.
	const legacyUser = "legacy-connector-operator"
	if err := e.GrantObjectPermission(legacyUser, "connector_type", "*", "modify"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantObjectPermission(legacyUser, "connector_type", "remote-api", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := e.Check(legacyUser, "connector_type", "remote-api", "modify"); err != nil || !allowed {
		t.Fatalf("legacy Check(remote-api, modify) = %v, %v; want true", allowed, err)
	}
	filtered, err = e.FilterResourceOps(legacyUser,
		[]authz.ResourceRef{{Type: "connector_type", ID: "remote-api"}},
		[]string{"view_detail"}, []string{"view_detail", "modify"})
	if err != nil || len(filtered) != 1 ||
		!reflect.DeepEqual(filtered[0].Operations, []string{"view_detail", "modify"}) {
		t.Fatalf("legacy FilterResourceOps(remote-api) = %+v, %v; want view_detail and modify", filtered, err)
	}
}

func TestSeedPrunesWithdrawnConnectorTaskManageGrants(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	if err := db.Create(&model.Operation{
		ResourceTypeID: "connector_type", ID: "task_manage", Name: "任务管理",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.GrantObjectPermission("legacy-connector-operator", "connector_type", "remote-api", "task_manage"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantObjectPermission("decoy-operator", "connectorXtype", "remote-api", "task_manage"); err != nil {
		t.Fatal(err)
	}

	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	var operationCount int64
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "connector_type", "task_manage").
		Count(&operationCount).Error; err != nil {
		t.Fatal(err)
	}
	if operationCount != 0 {
		t.Fatalf("connector_type/task_manage operation count = %d; want 0", operationCount)
	}
	records, err := e.PolicyRecords(authz.PolicyFilter{
		AccessorID: "legacy-connector-operator",
		Operation:  "task_manage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("withdrawn connector task_manage grants survived: %+v", records)
	}
	allowed, err := e.Check("legacy-connector-operator", "connector_type", "remote-api", "task_manage")
	if err != nil || allowed {
		t.Fatalf("withdrawn connector task_manage check = %v, %v; want false", allowed, err)
	}
	allowed, err = e.Check("decoy-operator", "connectorXtype", "remote-api", "task_manage")
	if err != nil || !allowed {
		t.Fatalf("neighbor resource type grant was removed: allowed=%v err=%v", allowed, err)
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
