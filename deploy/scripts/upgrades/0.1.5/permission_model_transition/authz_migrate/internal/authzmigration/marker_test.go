// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/migrationcontract"
)

func TestMarkerRecordsReconciledCoreAndEEAndIsIdempotent(t *testing.T) {
	db := enterpriseTestDB(t, true)
	seedEnterpriseSubjects(t, db)
	seedPolicies(t, db, casbinPolicyRow{Ptype: "p", V0: "user-1", V1: "resource:r-1", V2: "view_detail"})
	seedEERules(t, db, eeRuleRow{ID: "ee-deny", AccessorID: "user-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "deny"})
	if _, err := ApplyCore(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	evidence := []EERuleEvidence{{GrantID: "ee-deny", ExpectedSubjectType: eeSubjectUser, PublishedWriterEvidence: "writer", RuntimeUsageEvidence: "runtime"}}
	dryRun, err := PlanEnterprise(context.Background(), db, EEOptions{RuleEvidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	confirmedAt := time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)
	confirmation := &EEActivationConfirmation{ConfirmedGrantIDs: []string{"ee-deny"}, InventoryDigest: dryRun.InventoryDigest,
		OperatorID: "admin-1", EvidenceRef: "change-123", ConfirmedAt: confirmedAt}
	eeOpts := EEOptions{Now: confirmedAt, RuleEvidence: evidence, Activation: confirmation}
	if _, err := ApplyEnterprise(context.Background(), db, eeOpts); err != nil {
		t.Fatal(err)
	}
	core, enterprise, err := ReconciledReports(context.Background(), db, nil, eeOpts)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := BuildMarker(core, enterprise, confirmation, confirmedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := PersistMarker(context.Background(), db, marker); err != nil {
		t.Fatal(err)
	}
	if err := PersistMarker(context.Background(), db, marker); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&migrationcontract.Marker{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("marker count = %d", count)
	}
	var stored migrationcontract.Marker
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EETableState != EETablePresentWithRows || stored.EEActiveCount != 1 ||
		stored.EEDenyCount != 1 || stored.ActivatedGrantIDs != "[\"ee-deny\"]" ||
		stored.ActivationConfirmedBy != "admin-1" || stored.ActivationEvidenceRef != "change-123" {
		t.Fatalf("stored marker = %+v", stored)
	}
	if stored.AppliedAt.UTC() != confirmedAt {
		t.Fatalf("idempotent persist changed applied_at: %v", stored.AppliedAt)
	}
}

func TestPersistMarkerRejectsASecondDifferentMigrationReceipt(t *testing.T) {
	db := migrationTestDB(t)
	first := migrationcontract.Marker{
		Version: CurrentVersion, EETableState: EETableAbsent, CoreSourceSummary: "{}", ActivatedGrantIDs: "[]",
	}
	first = first.Seal()
	if err := PersistMarker(context.Background(), db, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.CorePolicyCount = 1
	second = second.Seal()
	if err := PersistMarker(context.Background(), db, second); !errors.Is(err, ErrMigrationMarkerRequired) {
		t.Fatalf("different receipt error = %v", err)
	}
}

func TestBuildMarkerRejectsPendingMigrationPlan(t *testing.T) {
	db := migrationTestDB(t)
	seedRoles(t, db)
	seedPolicies(t, db, casbinPolicyRow{Ptype: "p", V0: "user", V1: "resource:r", V2: "view_detail"})
	core, err := PlanCore(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	ee := EEReport{MigrationVersion: CurrentVersion, TableState: EETableAbsent, InventoryDigest: "digest"}
	if _, err := BuildMarker(core, ee, nil, time.Now()); !errors.Is(err, ErrPlanBlocked) {
		t.Fatalf("BuildMarker error = %v", err)
	}
	for _, item := range core.Policies {
		if item.PlannedPolicySource == policySourceCommunityBundle {
			t.Fatal("pending Core plan unexpectedly inferred bundle")
		}
	}
}

func TestBuildMarkerRequiresExactConfirmationForEveryActiveEEGrant(t *testing.T) {
	core := CoreReport{MigrationVersion: CurrentVersion}
	enterprise := EEReport{
		MigrationVersion: CurrentVersion, TableState: EETablePresentWithRows, InventoryDigest: "digest",
		Summary: EESummary{Rows: 1, Published: 1, Active: 1},
		Rules: []EERulePlan{{GrantID: "active-1", CurrentSubjectType: eeSubjectUser, PlannedSubjectType: eeSubjectUser,
			CurrentClassification: EEClassificationPublished, PlannedClassification: EEClassificationPublished,
			CurrentActivation: eeActivationActive, PlannedActivation: eeActivationActive}},
	}
	if _, err := BuildMarker(core, enterprise, nil, time.Now()); !errors.Is(err, ErrPlanBlocked) {
		t.Fatalf("missing confirmation error = %v", err)
	}
	if err := ValidateActiveGrantConfirmation(enterprise, nil); !errors.Is(err, ErrPlanBlocked) {
		t.Fatalf("preflight missing confirmation error = %v", err)
	}
	confirmation := &EEActivationConfirmation{ConfirmedGrantIDs: []string{"different"}, InventoryDigest: "digest",
		OperatorID: "admin", EvidenceRef: "change", ConfirmedAt: time.Now().UTC()}
	if _, err := BuildMarker(core, enterprise, confirmation, time.Now()); !errors.Is(err, ErrPlanBlocked) {
		t.Fatalf("mismatched confirmation error = %v", err)
	}
	confirmation.ConfirmedGrantIDs = []string{"active-1"}
	if _, err := BuildMarker(core, enterprise, confirmation, time.Now()); err != nil {
		t.Fatal(err)
	}
}
