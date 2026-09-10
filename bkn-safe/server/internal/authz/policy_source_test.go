// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func useEdition(t *testing.T, initial licverify.Edition) *licverify.Edition {
	t.Helper()
	edition := initial
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Edition: edition}
	}))
	t.Cleanup(entitlement.ResetForTest)
	return &edition
}

func TestNewRejectsUnclassifiedPolicyWithoutMutatingItsSource(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	// Create the adapter-owned table, then emulate a pre-migration business row.
	if _, err := New(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO casbin_rule (ptype, v0, v1, v2, v3) VALUES (?, ?, ?, ?, ?)",
		"p", "legacy-user", "resource:r-1", "view_detail", EffectAllow,
	).Error; err != nil {
		t.Fatal(err)
	}

	_, err = New(db)
	if !errors.Is(err, ErrPolicySourceMigrationRequired) {
		t.Fatalf("New() error = %v, want ErrPolicySourceMigrationRequired", err)
	}
	var classified int64
	if err := db.Table("casbin_rule").Where("v0 = ? AND v4 IS NOT NULL AND v4 <> ''", "legacy-user").Count(&classified).Error; err != nil {
		t.Fatal(err)
	}
	if classified != 0 {
		t.Fatal("startup inferred provenance; offline migration must own classification")
	}
}

func TestNewRejectsPolicyProjectionWithoutStableGrant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"p", "u-1", "resource:r-1", "view_detail", EffectAllow,
		PolicySourceProfessionalRule, AuthoritySourceAdminAuthz,
	).Error; err != nil {
		t.Fatal(err)
	}

	_, err = New(db)
	if !errors.Is(err, ErrPolicySourceMigrationRequired) {
		t.Fatalf("New() error = %v, want ErrPolicySourceMigrationRequired", err)
	}
}

func TestNewRejectsStableGrantWithoutPolicyProjection(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&safemodel.AuthorizationGrant{
		GrantID: "grant-1",
		ProjectionKey: policyProjectionKey("u-1", "resource:r-1", "view_detail", EffectAllow,
			PolicySourceProfessionalRule, AuthoritySourceAdminAuthz),
		AccessorID: "u-1", Object: "resource:r-1", Operation: "view_detail",
		Effect: EffectAllow, PolicySource: string(PolicySourceProfessionalRule),
		AuthoritySource: string(AuthoritySourceAdminAuthz), CreatedBy: "admin-1",
	}).Error; err != nil {
		t.Fatal(err)
	}

	_, err = New(db)
	if !errors.Is(err, ErrPolicySourceMigrationRequired) {
		t.Fatalf("New() error = %v, want ErrPolicySourceMigrationRequired", err)
	}
}

func TestNewDoesNotReportDatabaseFailureAsMigrationRequired(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&safemodel.AuthorizationGrant{}); err != nil {
		t.Fatal(err)
	}

	_, err = New(db)
	if err == nil || errors.Is(err, ErrPolicySourceMigrationRequired) {
		t.Fatalf("New() error = %v, want a database error distinct from migration required", err)
	}
}

func TestPolicySourcesFollowLiveEditionWithoutRewritingRows(t *testing.T) {
	edition := useEdition(t, licverify.EditionCommunity)
	e, _ := newTestEnforcerDB(t)

	mustNoErr(t, e.GrantObjectPermission("u-legacy", "resource", "r-1", "view_detail"))
	mustNoErr(t, e.GrantSystemObjectPermission("u-system", "resource", "r-1", "view_detail"))
	mustNoErr(t, e.GrantRolePermission("role-reader", "resource", "r-1", "view_detail"))
	mustNoErr(t, e.AssignRole("u-role", "role-reader"))
	mustNoErr(t, e.GrantCommunityBundle("u-bundle", "resource", "r-1", AuthoritySourceAdminAuthz))
	mustNoErr(t, e.GrantProfessionalObjectPermission(
		"u-professional", "resource", "r-1", "view_detail", EffectAllow, AuthoritySourceAdminAuthz,
	))

	for _, tc := range []struct {
		user, operation string
	}{
		{"u-legacy", "view_detail"},
		{"u-system", "view_detail"},
		{"u-role", "view_detail"},
		{"u-bundle", ActFullBusinessAccess},
	} {
		allowed, err := e.Check(tc.user, "resource", "r-1", tc.operation)
		if err != nil || !allowed {
			t.Fatalf("Community Check(%s,%s) = %v, %v; want true", tc.user, tc.operation, allowed, err)
		}
	}
	assertAllowed := func(want bool) {
		t.Helper()
		allowed, err := e.Check("u-professional", "resource", "r-1", "view_detail")
		if err != nil || allowed != want {
			t.Fatalf("edition %q professional decision = %v, %v; want %v", *edition, allowed, err, want)
		}
		_, effective, err := e.EffectivePermissions("u-professional", PermQuery{ResourceType: "resource"})
		listed := len(effective) == 1 && hasOp(effective[0].Operations, "view_detail")
		if err != nil || listed != want {
			t.Fatalf("edition %q effective permissions = %+v, %v; want listed=%v", *edition, effective, err, want)
		}
		direct, err := e.ListObjectGrants("u-professional", "resource", "r-1")
		listed = len(direct) == 1 && hasOp(direct[0].Operations, "view_detail")
		if err != nil || listed != want {
			t.Fatalf("edition %q object grants = %+v, %v; want listed=%v", *edition, direct, err, want)
		}
	}
	assertAllowed(false)

	records, err := e.PolicyRecords(PolicyFilter{AccessorID: "u-professional"})
	if err != nil || len(records) != 1 {
		t.Fatalf("professional records = %+v, %v; want one retained row", records, err)
	}
	if records[0].Active {
		t.Fatal("Professional row reported active in Community")
	}
	id := records[0].GrantID

	for _, paid := range []licverify.Edition{
		licverify.EditionProfessional, licverify.EditionEnterprise, licverify.EditionIndustry,
	} {
		*edition = paid
		assertAllowed(true)
	}
	*edition = licverify.EditionCommunity
	assertAllowed(false)
	*edition = licverify.EditionProfessional
	assertAllowed(true)

	reloaded, err := New(e.db)
	if err != nil {
		t.Fatal(err)
	}
	reloadedRecords, err := reloaded.PolicyRecords(PolicyFilter{AccessorID: "u-professional"})
	if err != nil || len(reloadedRecords) != 1 || reloadedRecords[0].GrantID != id {
		t.Fatalf("stable identity after reload = %+v, %v; want id %s", reloadedRecords, err, id)
	}
}

func TestSameTupleSourcesAreIndependentAndOwnerSaveIsScoped(t *testing.T) {
	useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	const user = "u-1"

	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r-1", "view_detail"))
	mustNoErr(t, e.GrantProfessionalObjectPermission(
		user, "resource", "r-1", "view_detail", EffectAllow, AuthoritySourceAdminAuthz,
	))
	mustNoErr(t, e.GrantProfessionalObjectPermission(
		user, "resource", "r-1", "view_detail", EffectAllow, AuthoritySourceOwnerDelegate,
	))
	mustNoErr(t, e.GrantSystemObjectPermission(user, "resource", "r-1", "view_detail"))

	before, err := e.PolicyRecords(PolicyFilter{AccessorID: user, Object: "resource:r-1", Operation: "view_detail"})
	if err != nil || len(before) != 4 {
		t.Fatalf("same-tuple records = %+v, %v; want four independently stored sources", before, err)
	}

	// Replacing the owner's slice must leave the administrator, migration and
	// system rows intact (A-48/A-49).
	mustNoErr(t, e.SetProfessionalObjectPermissions(
		user, "resource", "r-1", []string{"modify"}, EffectAllow, AuthoritySourceOwnerDelegate,
	))
	afterSave, err := e.PolicyRecords(PolicyFilter{AccessorID: user, Object: "resource:r-1"})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[AuthoritySource]int{}
	for _, record := range afterSave {
		counts[record.AuthoritySource]++
	}
	if counts[AuthoritySourceAdminAuthz] != 1 || counts[AuthoritySourceMigration] != 1 || counts[AuthoritySourceSystem] != 1 || counts[AuthoritySourceOwnerDelegate] != 1 {
		t.Fatalf("source-scoped replacement changed sibling rows: %+v", afterSave)
	}

	var legacyID string
	for _, record := range afterSave {
		if record.PolicySource == PolicySourceLegacy {
			legacyID = record.GrantID
		}
	}
	removed, err := e.RevokePolicy(legacyID)
	if err != nil || !removed {
		t.Fatalf("RevokePolicy(legacy) = %v, %v; want true", removed, err)
	}
	remaining, err := e.PolicyRecords(PolicyFilter{AccessorID: user, Object: "resource:r-1"})
	if err != nil || len(remaining) != 3 {
		t.Fatalf("remaining records = %+v, %v; want three", remaining, err)
	}
	allowed, err := e.Check(user, "resource", "r-1", "view_detail")
	if err != nil || !allowed {
		t.Fatalf("revoking legacy removed sibling sources: allowed=%v err=%v", allowed, err)
	}
}

func TestSourceSliceReplayPreservesStableGrantIdentity(t *testing.T) {
	useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	mustNoErr(t, e.SetProfessionalObjectPermissions(
		"u-1", "resource", "r-1", []string{"view_detail", "modify"}, EffectAllow, AuthoritySourceAdminAuthz,
	))
	before, err := e.PolicyRecords(PolicyFilter{
		AccessorID: "u-1", Object: "resource:r-1", PolicySource: PolicySourceProfessionalRule,
		AuthoritySource: AuthoritySourceAdminAuthz,
	})
	if err != nil || len(before) != 2 {
		t.Fatalf("initial source slice = %+v, %v; want two", before, err)
	}
	mustNoErr(t, e.SetProfessionalObjectPermissions(
		"u-1", "resource", "r-1", []string{"modify", "view_detail"}, EffectAllow, AuthoritySourceAdminAuthz,
	))
	after, err := e.PolicyRecords(PolicyFilter{
		AccessorID: "u-1", Object: "resource:r-1", PolicySource: PolicySourceProfessionalRule,
		AuthoritySource: AuthoritySourceAdminAuthz,
	})
	if err != nil || len(after) != 2 {
		t.Fatalf("replayed source slice = %+v, %v; want two", after, err)
	}
	for i := range before {
		if before[i].GrantID != after[i].GrantID || !before[i].CreatedAt.Equal(after[i].CreatedAt) {
			t.Fatalf("source replay replaced stable grant: before=%+v after=%+v", before, after)
		}
	}
}

func TestIdenticalTupleGrantIDsRemainIndependentUntilFinalRevoke(t *testing.T) {
	useEdition(t, licverify.EditionProfessional)
	e, db := newTestEnforcerDB(t)
	base := PolicyGrant{
		AccessorID: "u-1", Object: "resource:r-1", Operation: "view_detail", Effect: EffectAllow,
		PolicySource: PolicySourceProfessionalRule, AuthoritySource: AuthoritySourceAdminAuthz,
		CreatedBy: "admin-1",
	}
	first := base
	first.GrantID = "grant-1"
	second := base
	second.GrantID = "grant-2"

	created, err := e.GrantPolicy(t.Context(), first)
	if err != nil || !created {
		t.Fatalf("GrantPolicy(first) = %v, %v; want created", created, err)
	}
	created, err = e.GrantPolicy(t.Context(), first)
	if err != nil || created {
		t.Fatalf("GrantPolicy(first replay) = %v, %v; want idempotent no-op", created, err)
	}
	created, err = e.GrantPolicy(t.Context(), second)
	if err != nil || !created {
		t.Fatalf("GrantPolicy(second) = %v, %v; want created", created, err)
	}
	records, err := e.PolicyRecords(PolicyFilter{AccessorID: base.AccessorID, Object: base.Object})
	if err != nil || len(records) != 2 {
		t.Fatalf("independent grants = %+v, %v; want two", records, err)
	}
	var projections int64
	if err := db.Table("casbin_rule").Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ? AND v4 = ? AND v5 = ?",
		"p", base.AccessorID, base.Object, base.Operation, base.Effect,
		base.PolicySource, base.AuthoritySource,
	).Count(&projections).Error; err != nil || projections != 1 {
		t.Fatalf("shared projection count = %d, %v; want one", projections, err)
	}

	removed, err := e.RevokePolicy(first.GrantID)
	if err != nil || !removed {
		t.Fatalf("RevokePolicy(first) = %v, %v; want true", removed, err)
	}
	allowed, err := e.Check(base.AccessorID, "resource", "r-1", base.Operation)
	if err != nil || !allowed {
		t.Fatalf("sibling grant did not preserve access: allowed=%v err=%v", allowed, err)
	}
	if err := db.Table("casbin_rule").Where("ptype = ? AND v0 = ? AND v1 = ?", "p", base.AccessorID, base.Object).
		Count(&projections).Error; err != nil || projections != 1 {
		t.Fatalf("projection after first revoke = %d, %v; want one", projections, err)
	}

	removed, err = e.RevokePolicy(second.GrantID)
	if err != nil || !removed {
		t.Fatalf("RevokePolicy(second) = %v, %v; want true", removed, err)
	}
	allowed, err = e.Check(base.AccessorID, "resource", "r-1", base.Operation)
	if err != nil || allowed {
		t.Fatalf("final revoke did not remove access: allowed=%v err=%v", allowed, err)
	}
	if err := db.Table("casbin_rule").Where("ptype = ? AND v0 = ? AND v1 = ?", "p", base.AccessorID, base.Object).
		Count(&projections).Error; err != nil || projections != 0 {
		t.Fatalf("projection after final revoke = %d, %v; want zero", projections, err)
	}
}

func TestGrantIDCannotBeReusedForAnotherPolicy(t *testing.T) {
	useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	grant := PolicyGrant{
		GrantID: "grant-1", AccessorID: "u-1", Object: "resource:r-1", Operation: "view_detail",
		Effect: EffectAllow, PolicySource: PolicySourceProfessionalRule,
		AuthoritySource: AuthoritySourceAdminAuthz, CreatedBy: "admin-1",
	}
	created, err := e.GrantPolicy(t.Context(), grant)
	if err != nil || !created {
		t.Fatalf("GrantPolicy() = %v, %v; want created", created, err)
	}
	grant.Object = "resource:r-2"
	created, err = e.GrantPolicy(t.Context(), grant)
	if err == nil || created {
		t.Fatalf("conflicting GrantPolicy() = %v, %v; want rejected", created, err)
	}
	allowed, checkErr := e.Check("u-1", "resource", "r-1", "view_detail")
	if checkErr != nil || !allowed {
		t.Fatalf("conflict changed original grant: allowed=%v err=%v", allowed, checkErr)
	}
}

func TestRenameOperationRekeysDerivedGrantAndPreservesExplicitGrantID(t *testing.T) {
	useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission("u-derived", "resource", "r-1", "old_operation"))
	explicit := PolicyGrant{
		GrantID: "explicit-grant", AccessorID: "u-explicit", Object: "resource:r-1", Operation: "old_operation",
		Effect: EffectAllow, PolicySource: PolicySourceProfessionalRule,
		AuthoritySource: AuthoritySourceAdminAuthz, CreatedBy: "admin-1",
	}
	created, err := e.GrantPolicy(t.Context(), explicit)
	if err != nil || !created {
		t.Fatalf("GrantPolicy(explicit) = %v, %v; want created", created, err)
	}

	moved, err := e.RenameOperation("resource", "old_operation", "new_operation")
	if err != nil || moved != 2 {
		t.Fatalf("RenameOperation() = %d, %v; want two", moved, err)
	}
	derived, err := e.PolicyRecords(PolicyFilter{AccessorID: "u-derived"})
	wantDerivedID := deterministicGrantID("u-derived", "resource:r-1", "new_operation", EffectAllow,
		PolicySourceLegacy, AuthoritySourceMigration)
	if err != nil || len(derived) != 1 || derived[0].GrantID != wantDerivedID || derived[0].Operation != "new_operation" {
		t.Fatalf("renamed derived grant = %+v, %v; want id %q", derived, err, wantDerivedID)
	}
	explicitRows, err := e.PolicyRecords(PolicyFilter{AccessorID: "u-explicit"})
	if err != nil || len(explicitRows) != 1 || explicitRows[0].GrantID != explicit.GrantID ||
		explicitRows[0].Operation != "new_operation" {
		t.Fatalf("renamed explicit grant = %+v, %v; want stable explicit id", explicitRows, err)
	}

	// A stale caller may still submit the old spelling. It must create its old
	// deterministic identity instead of colliding with the renamed grant.
	mustNoErr(t, e.GrantObjectPermission("u-derived", "resource", "r-1", "old_operation"))
	rows, err := e.PolicyRecords(PolicyFilter{AccessorID: "u-derived"})
	if err != nil || len(rows) != 2 {
		t.Fatalf("old spelling replay after rename = %+v, %v; want two independent grants", rows, err)
	}
}

func TestProfessionalDenyIsInactiveOnDowngradeAndRestoredOnRecovery(t *testing.T) {
	edition := useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission("u-1", "resource", "r-1", "view_detail"))
	mustNoErr(t, e.GrantProfessionalObjectPermission(
		"u-1", "resource", "r-1", "view_detail", EffectDeny, AuthoritySourceAdminAuthz,
	))

	allowed, err := e.Check("u-1", "resource", "r-1", "view_detail")
	if err != nil || allowed {
		t.Fatalf("Professional decision = %v, %v; want deny", allowed, err)
	}
	*edition = licverify.EditionCommunity
	allowed, err = e.Check("u-1", "resource", "r-1", "view_detail")
	if err != nil || !allowed {
		t.Fatalf("Community decision = %v, %v; want legacy allow", allowed, err)
	}
	*edition = licverify.EditionIndustry
	allowed, err = e.Check("u-1", "resource", "r-1", "view_detail")
	if err != nil || allowed {
		t.Fatalf("Industry decision = %v, %v; want restored Professional deny", allowed, err)
	}
}

func TestLegacyPolicyCannotBeExpandedInPlace(t *testing.T) {
	useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission("u-1", "catalog", "c-1", "resource_manage"))

	added, err := e.BackfillImpliedOperation("catalog", "resource_manage", "view_detail")
	if err != nil || len(added) != 0 {
		t.Fatalf("legacy backfill = %+v, %v; want no in-place expansion", added, err)
	}
	records, err := e.PolicyRecords(PolicyFilter{AccessorID: "u-1", Object: "catalog:c-1"})
	if err != nil || len(records) != 1 || records[0].Operation != "resource_manage" {
		t.Fatalf("legacy records changed in place: %+v, %v", records, err)
	}
}
