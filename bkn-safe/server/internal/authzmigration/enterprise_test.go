// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestEnterpriseAbsentStateDoesNotCreatePrivateTable(t *testing.T) {
	db := enterpriseTestDB(t, false)
	report, err := ApplyEnterprise(context.Background(), db, EEOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.TableState != EETableAbsent || db.Migrator().HasTable(&eeRuleRow{}) {
		t.Fatalf("report = %+v, EE table exists = %v", report, db.Migrator().HasTable(&eeRuleRow{}))
	}

	report, err = PlanEnterprise(context.Background(), db, EEOptions{Assembly: EEAssemblyEvidence{WasAssembled: true, EvidenceRef: "release-ledger"}})
	if !errors.Is(err, ErrPlanBlocked) || !report.Blocked() {
		t.Fatalf("missing former EE table error = %v, report = %+v", err, report)
	}
}

func TestEnterpriseDryRunClassifiesSubjectsAndWritesNothing(t *testing.T) {
	db := enterpriseTestDB(t, true)
	now := time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)
	seedEnterpriseSubjects(t, db)
	seedEERules(t, db,
		eeRuleRow{ID: "allow-user", AccessorID: "user-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow"},
		eeRuleRow{ID: "deny-role", AccessorID: "role-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "deny"},
		eeRuleRow{ID: "department", AccessorID: "department-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow"},
		eeRuleRow{ID: "unknown", AccessorID: "missing", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow"},
		eeRuleRow{ID: "dormant", AccessorID: "user-1", ResourceType: "object_type", ResourceID: "ot-2", Op: "query_data", Effect: "allow"},
	)
	evidence := []EERuleEvidence{
		{GrantID: "allow-user", ExpectedSubjectType: eeSubjectUser, PublishedWriterEvidence: "writer-log-1", RuntimeUsageEvidence: "decision-log-1"},
		{GrantID: "deny-role", ExpectedSubjectType: eeSubjectRole, PublishedWriterEvidence: "writer-log-2", RuntimeUsageEvidence: "decision-log-2"},
	}

	report, err := PlanEnterprise(context.Background(), db, EEOptions{Now: now, RuleEvidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if report.TableState != EETablePresentWithRows || report.Summary.Published != 2 ||
		report.Summary.Dormant != 1 || report.Summary.Invalid != 2 || report.Summary.Deny != 1 {
		t.Fatalf("report = %+v", report)
	}
	assertEEPlan(t, report, "allow-user", eeSubjectUser, EEClassificationPublished, true, "normal")
	assertEEPlan(t, report, "deny-role", eeSubjectRole, EEClassificationPublished, true, "high")
	assertEEPlan(t, report, "department", eeSubjectDepartment, EEClassificationInvalid, false, "normal")
	assertEEPlan(t, report, "unknown", eeSubjectUnknown, EEClassificationInvalid, false, "normal")
	assertEEPlan(t, report, "dormant", eeSubjectUser, EEClassificationDormant, false, "normal")
	for _, rule := range report.Rules {
		if rule.GrantID == "allow-user" && (rule.PublishedWriterEvidence != "writer-log-1" || rule.RuntimeUsageEvidence != "decision-log-1") {
			t.Fatalf("published/runtime evidence missing from dry-run: %+v", rule)
		}
	}

	var rows []eeRuleRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.SubjectType != eeSubjectUnknown || row.Classification != "unclassified" || row.ActivationState != eeActivationInactive {
			t.Fatalf("dry-run mutated row: %+v", row)
		}
	}
}

func TestEnterpriseActivationRequiresReviewedDigestAndIsIdempotent(t *testing.T) {
	db := enterpriseTestDB(t, true)
	now := time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)
	seedEnterpriseSubjects(t, db)
	seedEERules(t, db,
		eeRuleRow{ID: "allow-user", AccessorID: "user-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow"},
		eeRuleRow{ID: "deny-role", AccessorID: "role-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "deny"},
	)
	evidence := []EERuleEvidence{
		{GrantID: "allow-user", ExpectedSubjectType: eeSubjectUser, PublishedWriterEvidence: "writer-log-1", RuntimeUsageEvidence: "decision-log-1"},
		{GrantID: "deny-role", ExpectedSubjectType: eeSubjectRole, PublishedWriterEvidence: "writer-log-2", RuntimeUsageEvidence: "decision-log-2"},
	}
	dryRun, err := PlanEnterprise(context.Background(), db, EEOptions{Now: now, RuleEvidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	confirmation := &EEActivationConfirmation{
		ConfirmedGrantIDs: []string{"allow-user", "deny-role"}, InventoryDigest: "wrong",
		OperatorID: "admin-1", EvidenceRef: "change-123", ConfirmedAt: now,
	}
	if _, err := ApplyEnterprise(context.Background(), db, EEOptions{Now: now, RuleEvidence: evidence, Activation: confirmation}); !errors.Is(err, ErrPlanBlocked) {
		t.Fatalf("wrong digest error = %v", err)
	}
	confirmation.InventoryDigest = dryRun.InventoryDigest
	verified, err := ApplyEnterprise(context.Background(), db, EEOptions{Now: now, RuleEvidence: evidence, Activation: confirmation})
	if err != nil {
		t.Fatal(err)
	}
	if verified.HasChanges() || verified.Summary.Active != 2 || verified.Summary.ToActivate != 0 {
		t.Fatalf("verified report = %+v", verified)
	}
	var auditCount int64
	if err := db.Model(&eeLifecycleAuditRow{}).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("audit count = %d", auditCount)
	}
	if _, err := ApplyEnterprise(context.Background(), db, EEOptions{Now: now, RuleEvidence: evidence, Activation: confirmation}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&eeLifecycleAuditRow{}).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("idempotent activation wrote duplicate audits: %d", auditCount)
	}
}

func TestApplyEnterpriseAddsMissingMigrationColumnsWithoutRewritingBaseTable(t *testing.T) {
	db := enterpriseTestDB(t, false)
	seedEnterpriseSubjects(t, db)
	if err := db.Exec(`CREATE TABLE ee_permobject_rules (
		id TEXT PRIMARY KEY, accessor_id TEXT NOT NULL, resource_type TEXT NOT NULL,
		resource_id TEXT NOT NULL, op TEXT NOT NULL, effect TEXT NOT NULL,
		expires_at DATETIME, granted_by TEXT, reason TEXT, created_at DATETIME, updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ee_permobject_rules
		(id, accessor_id, resource_type, resource_id, op, effect)
		VALUES ('legacy-ee', 'user-1', 'object_type', 'ot-1', 'query_data', 'allow')`).Error; err != nil {
		t.Fatal(err)
	}
	evidence := []EERuleEvidence{{GrantID: "legacy-ee", ExpectedSubjectType: eeSubjectUser,
		PublishedWriterEvidence: "writer", RuntimeUsageEvidence: "runtime"}}
	dryRun, err := PlanEnterprise(context.Background(), db, EEOptions{RuleEvidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	confirmation := &EEActivationConfirmation{ConfirmedGrantIDs: []string{"legacy-ee"}, InventoryDigest: dryRun.InventoryDigest,
		OperatorID: "admin", EvidenceRef: "change", ConfirmedAt: time.Now().UTC()}
	if _, err := ApplyEnterprise(context.Background(), db, EEOptions{RuleEvidence: evidence, Activation: confirmation}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"SubjectType", "Classification", "ActivationState", "ActivationRef"} {
		if !db.Migrator().HasColumn(&eeRuleRow{}, field) {
			t.Fatalf("missing additive migration column %s", field)
		}
	}
	if !db.Migrator().HasTable(&eeLifecycleAuditRow{}) {
		t.Fatal("missing EE lifecycle audit table")
	}
}

func TestEnterpriseRoleWithoutRealMemberCannotActivate(t *testing.T) {
	db := enterpriseTestDB(t, true)
	seedEnterpriseSubjects(t, db)
	if err := db.Where("ptype = ?", "g").Delete(&casbinPolicyRow{}).Error; err != nil {
		t.Fatal(err)
	}
	seedEERules(t, db, eeRuleRow{ID: "role-rule", AccessorID: "role-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow"})
	evidence := []EERuleEvidence{{GrantID: "role-rule", ExpectedSubjectType: eeSubjectRole, PublishedWriterEvidence: "writer", RuntimeUsageEvidence: "runtime"}}
	report, err := PlanEnterprise(context.Background(), db, EEOptions{RuleEvidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	assertEEPlan(t, report, "role-rule", eeSubjectRole, EEClassificationPublished, false, "normal")
	confirmation := &EEActivationConfirmation{ConfirmedGrantIDs: []string{"role-rule"}, InventoryDigest: report.InventoryDigest,
		OperatorID: "admin", EvidenceRef: "change", ConfirmedAt: time.Now().UTC()}
	if _, err := ApplyEnterprise(context.Background(), db, EEOptions{RuleEvidence: evidence, Activation: confirmation}); !errors.Is(err, ErrPlanBlocked) {
		t.Fatalf("role without membership activation error = %v", err)
	}
}

func TestEnterpriseRejectsEvidenceForUnknownRule(t *testing.T) {
	db := enterpriseTestDB(t, true)
	seedEnterpriseSubjects(t, db)
	seedEERules(t, db, eeRuleRow{ID: "known", AccessorID: "user-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow"})
	evidence := []EERuleEvidence{{GrantID: "missing", ExpectedSubjectType: eeSubjectUser, PublishedWriterEvidence: "writer", RuntimeUsageEvidence: "runtime"}}

	report, err := PlanEnterprise(context.Background(), db, EEOptions{RuleEvidence: evidence})
	if !errors.Is(err, ErrPlanBlocked) || !report.Blocked() {
		t.Fatalf("unknown evidence error = %v, report = %+v", err, report)
	}
}

func TestEnterpriseRejectsRuleEvidenceWhenTableIsAbsentOrEmpty(t *testing.T) {
	evidence := []EERuleEvidence{{GrantID: "missing", ExpectedSubjectType: eeSubjectUser, PublishedWriterEvidence: "writer", RuntimeUsageEvidence: "runtime"}}
	for _, withEE := range []bool{false, true} {
		db := enterpriseTestDB(t, withEE)
		report, err := PlanEnterprise(context.Background(), db, EEOptions{RuleEvidence: evidence})
		if !errors.Is(err, ErrPlanBlocked) || !report.Blocked() {
			t.Fatalf("withEE=%v: error = %v, report = %+v", withEE, err, report)
		}
	}
}

func TestEnterpriseKeepsClassificationEvidenceAndAcceptsExpiredActiveRule(t *testing.T) {
	db := enterpriseTestDB(t, true)
	seedEnterpriseSubjects(t, db)
	expiresAt := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	activatedAt := expiresAt.Add(-time.Hour)
	seedEERules(t, db,
		eeRuleRow{ID: "dormant", AccessorID: "user-1", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow",
			SubjectType: eeSubjectUser, Classification: EEClassificationDormant, ClassificationEvidence: "historical-review"},
		eeRuleRow{ID: "active", AccessorID: "user-1", ResourceType: "object_type", ResourceID: "ot-2", Op: "query_data", Effect: "allow",
			SubjectType: eeSubjectUser, Classification: EEClassificationPublished, ClassificationEvidence: "writer=x;runtime=y",
			ActivationState: eeActivationActive, ActivatedAt: &activatedAt, ActivatedBy: "admin", ActivationRef: "change-1", ExpiresAt: &expiresAt},
	)

	before, err := PlanEnterprise(context.Background(), db, EEOptions{Now: expiresAt.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	after, err := PlanEnterprise(context.Background(), db, EEOptions{Now: expiresAt.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if before.InventoryDigest != after.InventoryDigest {
		t.Fatalf("inventory digest changed only because activation eligibility changed: %s != %s", before.InventoryDigest, after.InventoryDigest)
	}
	for _, rule := range after.Rules {
		switch rule.GrantID {
		case "dormant":
			if rule.ClassificationEvidence != "historical-review" {
				t.Fatalf("classification evidence was lost: %+v", rule)
			}
		case "active":
			if rule.ActivationEligible || rule.PlannedActivation != eeActivationActive {
				t.Fatalf("expired active rule was not preserved: %+v", rule)
			}
		}
	}
	confirmation := &EEActivationConfirmation{ConfirmedGrantIDs: []string{"active"}, InventoryDigest: after.InventoryDigest,
		OperatorID: "admin", EvidenceRef: "change-1", ConfirmedAt: expiresAt.Add(time.Minute)}
	if _, err := ApplyEnterprise(context.Background(), db, EEOptions{Now: expiresAt.Add(time.Minute), Activation: confirmation}); err != nil {
		t.Fatal(err)
	}
}

func TestEnterpriseRejectsDisabledAndAmbiguousSubjects(t *testing.T) {
	db := enterpriseTestDB(t, true)
	seedEnterpriseSubjects(t, db)
	if err := db.Create(&safemodel.User{ID: "disabled", Account: "disabled", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&safemodel.User{ID: "role-1", Account: "ambiguous", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	seedEERules(t, db,
		eeRuleRow{ID: "disabled-rule", AccessorID: "disabled", ResourceType: "object_type", ResourceID: "ot-1", Op: "query_data", Effect: "allow"},
		eeRuleRow{ID: "ambiguous-rule", AccessorID: "role-1", ResourceType: "object_type", ResourceID: "ot-2", Op: "query_data", Effect: "allow"},
	)
	report, err := PlanEnterprise(context.Background(), db, EEOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertEEPlan(t, report, "disabled-rule", eeSubjectUser, EEClassificationInvalid, false, "normal")
	assertEEPlan(t, report, "ambiguous-rule", eeSubjectUnknown, EEClassificationInvalid, false, "normal")
}

func enterpriseTestDB(t *testing.T, withEE bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []any{&safemodel.User{}, &safemodel.Role{}, &safemodel.Department{}, &casbinPolicyRow{}}
	if withEE {
		models = append(models, &eeRuleRow{})
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedEnterpriseSubjects(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&safemodel.User{ID: "user-1", Account: "user-1", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&safemodel.Role{ID: "role-1", Name: "role-1", Source: safemodel.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&safemodel.Department{ID: "department-1", Name: "department-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&casbinPolicyRow{Ptype: "g", V0: "user-1", V1: "role-1"}).Error; err != nil {
		t.Fatal(err)
	}
}

func seedEERules(t *testing.T, db *gorm.DB, rules ...eeRuleRow) {
	t.Helper()
	for i := range rules {
		if rules[i].SubjectType == "" {
			rules[i].SubjectType = eeSubjectUnknown
		}
		if rules[i].Classification == "" {
			rules[i].Classification = "unclassified"
		}
		if rules[i].ActivationState == "" {
			rules[i].ActivationState = eeActivationInactive
		}
		if err := db.Create(&rules[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertEEPlan(t *testing.T, report EEReport, id, subjectType, classification string, eligible bool, priority string) {
	t.Helper()
	for _, rule := range report.Rules {
		if rule.GrantID == id {
			if rule.PlannedSubjectType != subjectType || rule.PlannedClassification != classification ||
				rule.ActivationEligible != eligible || rule.ReviewPriority != priority {
				t.Fatalf("EE rule %s = %+v", id, rule)
			}
			return
		}
	}
	t.Fatalf("missing EE rule %s", id)
}
