// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestPlanCoreIsReadOnlyAndClassifiesWithoutInferringBundle(t *testing.T) {
	db := migrationTestDB(t)
	seedRoles(t, db)
	seedPolicies(t, db,
		casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "resource:r-1", V2: "view_detail"},
		casbinPolicyRow{Ptype: "p", V0: "custom-role", V1: "knowledge_network:kn-1", V2: "modify", V3: authz.EffectAllow},
		casbinPolicyRow{Ptype: "p", V0: "built-in-role", V1: "knowledge_network:kn-1", V2: "query_data", V3: authz.EffectAllow},
		casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "object_type:ot-1", V2: "authorize", V3: authz.EffectAllow},
		casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "knowledge_network:kn-1", V2: "task_manage", V3: authz.EffectAllow},
		casbinPolicyRow{Ptype: "p", V0: "creator-1", V1: "action_type:at-1", V2: "execute", V3: authz.EffectAllow},
	)

	report, err := PlanCore(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Classified != 4 || report.Summary.Removed != 2 || report.Summary.Inserted != 0 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	assertPlannedSource(t, report, "user-1", "resource:r-1", "view_detail", string(authz.PolicySourceLegacy), string(authz.AuthoritySourceMigration))
	assertPlannedSource(t, report, "custom-role", "knowledge_network:kn-1", "modify", string(authz.PolicySourceRolePermission), string(authz.AuthoritySourceAdminAuthz))
	assertPlannedSource(t, report, "built-in-role", "knowledge_network:kn-1", "query_data", string(authz.PolicySourceRolePermission), string(authz.AuthoritySourceSystem))
	assertPlannedSource(t, report, "creator-1", "action_type:at-1", "execute", string(authz.PolicySourceLegacy), string(authz.AuthoritySourceMigration))
	for _, item := range report.Policies {
		if item.PlannedPolicySource == string(authz.PolicySourceCommunityBundle) || item.Operation == authz.ActFullBusinessAccess {
			t.Fatalf("migration inferred a Community bundle: %+v", item)
		}
	}

	var classified int64
	if err := db.Model(&casbinPolicyRow{}).Where("v4 <> '' OR v5 <> '' OR v3 <> ''").Count(&classified).Error; err != nil {
		t.Fatal(err)
	}
	// Five seeded rows already had v3=allow; dry-run must not fill the one empty
	// effect or either provenance column.
	if classified != 5 {
		t.Fatalf("dry-run mutated Casbin rows, classified count = %d", classified)
	}
	if db.Migrator().HasTable(&safemodel.AuthorizationGrant{}) {
		t.Fatal("dry-run created authorization_grant")
	}
}

func TestApplyCoreIsIdempotentAndPreservesIndependentGrantSources(t *testing.T) {
	db := migrationTestDB(t)
	seedRoles(t, db)
	seedPolicies(t, db,
		casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "resource:r-1", V2: "view_detail"},
		casbinPolicyRow{Ptype: "p", V0: "custom-role", V1: "knowledge_network:kn-1", V2: "modify", V3: authz.EffectAllow},
		casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "object_type:ot-1", V2: "authorize", V3: authz.EffectAllow},
		casbinPolicyRow{Ptype: "p", V0: "user-2", V1: "resource:r-2", V2: "query_data", V3: authz.EffectAllow,
			V4: string(authz.PolicySourceProfessionalRule), V5: string(authz.AuthoritySourceAdminAuthz)},
	)
	if err := db.AutoMigrate(&safemodel.AuthorizationGrant{}); err != nil {
		t.Fatal(err)
	}
	key := policyTuple{"user-2", "resource:r-2", "query_data", authz.EffectAllow,
		string(authz.PolicySourceProfessionalRule), string(authz.AuthoritySourceAdminAuthz)}
	for _, id := range []string{"grant-source-a", "grant-source-b"} {
		if err := db.Create(grantModel(id, key)).Error; err != nil {
			t.Fatal(err)
		}
	}

	verified, err := ApplyCore(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if verified.HasChanges() {
		t.Fatalf("post-apply plan still has changes: %+v", verified.Summary)
	}
	var grants []safemodel.AuthorizationGrant
	if err := db.Order("grant_id").Find(&grants).Error; err != nil {
		t.Fatal(err)
	}
	if len(grants) != 4 {
		t.Fatalf("grant count = %d, want two migrated plus two independent existing grants", len(grants))
	}
	var policyCount int64
	if err := db.Model(&casbinPolicyRow{}).Where("ptype = ?", "p").Count(&policyCount).Error; err != nil {
		t.Fatal(err)
	}
	if policyCount != 3 {
		t.Fatalf("policy count = %d, want transitional child authorize removed", policyCount)
	}

	firstIDs := grantIDs(grants)
	verified, err = ApplyCore(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if verified.HasChanges() {
		t.Fatalf("repeat apply has changes: %+v", verified.Summary)
	}
	grants = nil
	if err := db.Order("grant_id").Find(&grants).Error; err != nil {
		t.Fatal(err)
	}
	if got := grantIDs(grants); !equalStrings(got, firstIDs) {
		t.Fatalf("grant ids changed on repeat: %v -> %v", firstIDs, got)
	}
}

func TestPlanCoreBlocksExecuteActionWithoutWriting(t *testing.T) {
	db := migrationTestDB(t)
	seedRoles(t, db)
	seedPolicies(t, db, casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "action_type:at-1", V2: "execute_action", V3: authz.EffectAllow})

	report, err := PlanCore(context.Background(), db, nil)
	if !errors.Is(err, ErrPlanBlocked) || !report.Blocked() {
		t.Fatalf("PlanCore error = %v, report = %+v", err, report)
	}
	var row casbinPolicyRow
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.V4 != "" || row.V5 != "" {
		t.Fatalf("blocked dry-run mutated row: %+v", row)
	}
}

func TestPlanCoreBlocksDuplicateFinalProjection(t *testing.T) {
	db := migrationTestDB(t)
	seedRoles(t, db)
	duplicate := casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "knowledge_network:kn-1",
		V2: "view_detail", V3: "allow", V4: "legacy", V5: "migration"}
	if err := db.Create(&duplicate).Error; err != nil {
		t.Fatal(err)
	}
	duplicate.ID = 0
	if err := db.Create(&duplicate).Error; err != nil {
		t.Fatal(err)
	}

	report, err := PlanCore(context.Background(), db, nil)
	if !errors.Is(err, ErrPlanBlocked) || !report.Blocked() {
		t.Fatalf("duplicate projection error = %v, report = %+v", err, report)
	}
}

func TestPlanCoreBlocksMalformedHistoricalPolicy(t *testing.T) {
	db := migrationTestDB(t)
	seedRoles(t, db)
	seedPolicies(t, db, casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "malformed-object", V2: "view_detail", V3: "maybe"})

	report, err := PlanCore(context.Background(), db, nil)
	if !errors.Is(err, ErrPlanBlocked) || !report.Blocked() {
		t.Fatalf("malformed policy error = %v, report = %+v", err, report)
	}
}

func TestLifecycleEvidenceIsTheOnlySystemDerivedInference(t *testing.T) {
	db := migrationTestDB(t)
	seedRoles(t, db)
	seedPolicies(t, db,
		casbinPolicyRow{Ptype: "p", V0: "owner-1", V1: "knowledge_network:kn-1", V2: "authorize", V3: authz.EffectAllow},
	)
	evidence := []LifecycleEvidence{
		{Kind: "knowledge_network_owner", AccessorID: "owner-1", Object: "knowledge_network:kn-1", Operation: "authorize", EvidenceRef: "bkn:t_knowledge_network:kn-1"},
		{Kind: "action_type_creator", AccessorID: "creator-1", Object: "action_type:at-1", Operation: "execute", EvidenceRef: "bkn:t_action_type:at-1"},
	}

	report, err := PlanCore(context.Background(), db, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Classified != 1 || report.Summary.Inserted != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	assertPlannedSource(t, report, "owner-1", "knowledge_network:kn-1", "authorize", string(authz.PolicySourceSystemDerived), string(authz.AuthoritySourceSystem))
	assertPlannedSource(t, report, "creator-1", "action_type:at-1", "execute", string(authz.PolicySourceSystemDerived), string(authz.AuthoritySourceSystem))

	if _, err := ApplyCore(context.Background(), db, evidence); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&casbinPolicyRow{}).Where("v4 = ?", authz.PolicySourceSystemDerived).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("system-derived policy count = %d", count)
	}
}

func migrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&casbinPolicyRow{}, &safemodel.Role{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedRoles(t *testing.T, db *gorm.DB) {
	t.Helper()
	roles := []safemodel.Role{
		{ID: "built-in-role", Name: "built in", Source: safemodel.RoleSourceSystem},
		{ID: "custom-role", Name: "custom", Source: safemodel.RoleSourceCustom},
	}
	if err := db.Create(&roles).Error; err != nil {
		t.Fatal(err)
	}
}

func seedPolicies(t *testing.T, db *gorm.DB, rows ...casbinPolicyRow) {
	t.Helper()
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertPlannedSource(t *testing.T, report CoreReport, accessor, object, operation, source, authority string) {
	t.Helper()
	for _, item := range report.Policies {
		if item.AccessorID == accessor && item.Object == object && item.Operation == operation {
			if item.PlannedPolicySource != source || item.PlannedAuthoritySource != authority {
				t.Fatalf("planned source for %s/%s/%s = %s/%s, want %s/%s", accessor, object, operation,
					item.PlannedPolicySource, item.PlannedAuthoritySource, source, authority)
			}
			return
		}
	}
	t.Fatalf("missing plan for %s/%s/%s", accessor, object, operation)
}

func grantIDs(grants []safemodel.AuthorizationGrant) []string {
	ids := make([]string, 0, len(grants))
	for _, grant := range grants {
		ids = append(ids, grant.GrantID)
	}
	return ids
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
