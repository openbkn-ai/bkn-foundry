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
	id := records[0].ID

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
	if err != nil || len(reloadedRecords) != 1 || reloadedRecords[0].ID != id {
		t.Fatalf("stable identity after reload = %+v, %v; want id %d", reloadedRecords, err, id)
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

	var legacyID uint
	for _, record := range afterSave {
		if record.PolicySource == PolicySourceLegacy {
			legacyID = record.ID
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
