// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	eeSubjectUnknown    = "unknown"
	eeSubjectUser       = "user"
	eeSubjectRole       = "role"
	eeSubjectDepartment = "department"

	eeActivationInactive = "inactive"
	eeActivationActive   = "active"
	eeActivationRevoked  = "revoked"
)

// eeRuleRow mirrors the published EE compatibility table only inside this
// one-time migration package. It was frozen against openbkn-ee commit
// 3d2c106 (included in main 6332d14b); Core runtime never reads or writes it.
type eeRuleRow struct {
	ID                     string `gorm:"primaryKey;size:64"`
	AccessorID             string `gorm:"size:128;not null"`
	SubjectType            string `gorm:"size:16;not null;default:unknown"`
	ResourceType           string `gorm:"size:64;not null"`
	ResourceID             string `gorm:"size:128;not null"`
	Op                     string `gorm:"size:64;not null"`
	Effect                 string `gorm:"size:16;not null"`
	ExpiresAt              *time.Time
	GrantedBy              string `gorm:"size:128"`
	Reason                 string `gorm:"size:512"`
	Classification         string `gorm:"size:32;not null;default:unclassified"`
	ClassificationEvidence string `gorm:"size:512"`
	ActivationState        string `gorm:"size:16;not null;default:inactive"`
	ActivatedAt            *time.Time
	ActivatedBy            string `gorm:"size:128"`
	ActivationRef          string `gorm:"size:256"`
	RevokedAt              *time.Time
	RevokedBy              string `gorm:"size:128"`
	RevokeReason           string `gorm:"size:512"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (eeRuleRow) TableName() string { return "ee_permobject_rules" }

type eeLifecycleAuditRow struct {
	ID          string `gorm:"primaryKey;size:64"`
	RuleID      string `gorm:"size:64;not null;index"`
	Operation   string `gorm:"size:16;not null"`
	BeforeState string `gorm:"size:16;not null"`
	AfterState  string `gorm:"size:16;not null"`
	OperatorID  string `gorm:"size:128;not null"`
	EvidenceRef string `gorm:"size:256"`
	Reason      string `gorm:"size:512"`
	CreatedAt   time.Time
}

func (eeLifecycleAuditRow) TableName() string { return "ee_permobject_rule_lifecycle_audits" }

type subjectFacts struct {
	users       map[string]userRow
	roles       map[string]roleRow
	departments map[string]struct{}
	roleMembers map[string]int
}

// PlanEnterprise inventories and classifies EE rows without creating or
// altering an EE table. Invalid and dormant rows are reported, not promoted.
func PlanEnterprise(ctx context.Context, db *gorm.DB, opts EEOptions) (EEReport, error) {
	report := EEReport{
		MigrationVersion: CurrentVersion, Assembly: opts.Assembly,
		TableState: EETableAbsent, Rules: []EERulePlan{},
	}
	if db == nil {
		return report, fmt.Errorf("plan Enterprise authorization migration: nil database")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if !db.Migrator().HasTable(&eeRuleRow{}) {
		if opts.Assembly.WasAssembled {
			report.Anomalies = append(report.Anomalies,
				"EE assembly history exists but ee_permobject_rules is absent")
			return report, fmt.Errorf("%w: %s", ErrPlanBlocked, report.Anomalies[0])
		}
		if len(opts.RuleEvidence) > 0 {
			report.Anomalies = append(report.Anomalies, "EE rule evidence was supplied but ee_permobject_rules is absent")
			return report, fmt.Errorf("%w: %s", ErrPlanBlocked, report.Anomalies[0])
		}
		report.InventoryDigest = digestEEInventory(report)
		return validateActivationConfirmation(report, opts.Activation)
	}

	rows, err := loadEERows(ctx, db)
	if err != nil {
		return report, err
	}
	if len(rows) == 0 {
		report.TableState = EETablePresentEmpty
		evidence, evidenceProblems := indexEERuleEvidence(opts.RuleEvidence)
		report.Anomalies = append(report.Anomalies, evidenceProblems...)
		unknownIDs := make([]string, 0, len(evidence))
		for id := range evidence {
			unknownIDs = append(unknownIDs, id)
		}
		sort.Strings(unknownIDs)
		for _, id := range unknownIDs {
			report.Anomalies = append(report.Anomalies, "EE rule evidence refers to unknown grant_id "+id)
		}
		report.InventoryDigest = digestEEInventory(report)
		if report.Blocked() {
			return report, fmt.Errorf("%w: %s", ErrPlanBlocked, strings.Join(report.Anomalies, "; "))
		}
		return validateActivationConfirmation(report, opts.Activation)
	}
	report.TableState = EETablePresentWithRows
	facts, err := loadSubjectFacts(ctx, db)
	if err != nil {
		return report, err
	}
	evidence, evidenceProblems := indexEERuleEvidence(opts.RuleEvidence)
	report.Anomalies = append(report.Anomalies, evidenceProblems...)

	seenIDs := make(map[string]struct{}, len(rows))
	usedEvidence := make(map[string]struct{}, len(evidence))
	for _, row := range rows {
		ruleEvidence, hasEvidence := evidence[row.ID]
		if hasEvidence {
			usedEvidence[row.ID] = struct{}{}
		}
		plan := classifyEERow(row, facts, ruleEvidence, opts.Now)
		if _, duplicate := seenIDs[row.ID]; duplicate {
			plan.Anomalies = append(plan.Anomalies, "duplicate grant_id")
		}
		seenIDs[row.ID] = struct{}{}
		if row.ActivationState == eeActivationActive && plan.PlannedClassification != EEClassificationPublished {
			plan.Anomalies = append(plan.Anomalies, "an active row fails current classification and must fail closed")
			report.Anomalies = append(report.Anomalies,
				fmt.Sprintf("EE rule %q is active but invalid: %s", row.ID, strings.Join(plan.Anomalies, "; ")))
		}
		report.Rules = append(report.Rules, plan)
	}
	unusedEvidence := make([]string, 0)
	for id := range evidence {
		if _, used := usedEvidence[id]; !used {
			unusedEvidence = append(unusedEvidence, id)
		}
	}
	sort.Strings(unusedEvidence)
	for _, id := range unusedEvidence {
		report.Anomalies = append(report.Anomalies, "EE rule evidence refers to unknown grant_id "+id)
	}
	sort.Slice(report.Rules, func(i, j int) bool { return report.Rules[i].GrantID < report.Rules[j].GrantID })
	report.InventoryDigest = digestEEInventory(report)

	if report.Blocked() {
		return report, fmt.Errorf("%w: %s", ErrPlanBlocked, strings.Join(report.Anomalies, "; "))
	}
	report, err = validateActivationConfirmation(report, opts.Activation)
	if err != nil {
		return report, err
	}
	report.Summary = summarizeEE(report.Rules)
	return report, nil
}

// ApplyEnterprise persists classification for an existing EE table and
// activates only the explicitly confirmed rows. An absent Community table is
// returned unchanged and is never created.
func ApplyEnterprise(ctx context.Context, db *gorm.DB, opts EEOptions) (EEReport, error) {
	plan, err := PlanEnterprise(ctx, db, opts)
	if err != nil {
		return plan, err
	}
	if plan.TableState == EETableAbsent {
		return plan, nil
	}
	if err := prepareEnterpriseMigrationSchema(db.WithContext(ctx)); err != nil {
		return plan, fmt.Errorf("prepare Enterprise authorization migration schema: %w", err)
	}
	byID := make(map[string]EERulePlan, len(plan.Rules))
	for _, item := range plan.Rules {
		byID[item.GrantID] = item
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range plan.Rules {
			updates := map[string]any{
				"subject_type": item.PlannedSubjectType, "classification": item.PlannedClassification,
				"classification_evidence": item.ClassificationEvidence,
			}
			if item.CurrentActivation == "" {
				updates["activation_state"] = eeActivationInactive
			}
			if item.PlannedActivation == eeActivationActive && item.CurrentActivation != eeActivationActive {
				confirmation := opts.Activation
				updates["activation_state"] = eeActivationActive
				updates["activated_at"] = confirmation.ConfirmedAt
				updates["activated_by"] = confirmation.OperatorID
				updates["activation_ref"] = confirmation.EvidenceRef
			}
			if err := tx.Model(&eeRuleRow{}).Where("id = ?", item.GrantID).Updates(updates).Error; err != nil {
				return err
			}
			if item.PlannedActivation == eeActivationActive && item.CurrentActivation != eeActivationActive {
				if err := tx.Create(&eeLifecycleAuditRow{
					ID: uuid.NewString(), RuleID: item.GrantID, Operation: "activate",
					BeforeState: normalizedEEActivation(item.CurrentActivation), AfterState: eeActivationActive,
					OperatorID: opts.Activation.OperatorID, EvidenceRef: opts.Activation.EvidenceRef,
					Reason: item.ClassificationEvidence, CreatedAt: opts.Activation.ConfirmedAt,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return plan, fmt.Errorf("apply Enterprise authorization migration: %w", err)
	}

	verified, err := PlanEnterprise(ctx, db, opts)
	if err != nil {
		return verified, fmt.Errorf("verify Enterprise authorization migration: %w", err)
	}
	for _, item := range verified.Rules {
		previous := byID[item.GrantID]
		if previous.PlannedActivation != item.PlannedActivation {
			return verified, fmt.Errorf("verify Enterprise authorization migration: %w: activation drift for %s", ErrPlanBlocked, item.GrantID)
		}
	}
	if verified.HasChanges() {
		return verified, fmt.Errorf("verify Enterprise authorization migration: %w: plan is not idempotent", ErrPlanBlocked)
	}
	return verified, nil
}

// prepareEnterpriseMigrationSchema is deliberately narrower than
// AutoMigrate. The EE table already has an authoritative private model, so the
// one-time Core tool may add missing migration columns but must never rewrite
// the type, length, index, or nullability of an existing private column.
func prepareEnterpriseMigrationSchema(db *gorm.DB) error {
	for _, field := range []string{
		"SubjectType", "Classification", "ClassificationEvidence", "ActivationState",
		"ActivatedAt", "ActivatedBy", "ActivationRef", "RevokedAt", "RevokedBy", "RevokeReason",
	} {
		if !db.Migrator().HasColumn(&eeRuleRow{}, field) {
			if err := db.Migrator().AddColumn(&eeRuleRow{}, field); err != nil {
				return fmt.Errorf("add ee_permobject_rules.%s: %w", field, err)
			}
		}
	}
	if !db.Migrator().HasTable(&eeLifecycleAuditRow{}) {
		if err := db.Migrator().CreateTable(&eeLifecycleAuditRow{}); err != nil {
			return fmt.Errorf("create EE lifecycle audit table: %w", err)
		}
		return nil
	}
	for _, field := range []string{
		"ID", "RuleID", "Operation", "BeforeState", "AfterState", "OperatorID", "EvidenceRef", "Reason", "CreatedAt",
	} {
		if !db.Migrator().HasColumn(&eeLifecycleAuditRow{}, field) {
			return fmt.Errorf("%w: existing EE lifecycle audit table is missing column %s", ErrPlanBlocked, field)
		}
	}
	return nil
}

func loadEERows(ctx context.Context, db *gorm.DB) ([]eeRuleRow, error) {
	columns, err := db.Migrator().ColumnTypes(&eeRuleRow{})
	if err != nil {
		return nil, fmt.Errorf("inspect ee_permobject_rules columns: %w", err)
	}
	available := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		available[strings.ToLower(column.Name())] = struct{}{}
	}
	required := []string{"id", "accessor_id", "resource_type", "resource_id", "op", "effect"}
	for _, name := range required {
		if _, ok := available[name]; !ok {
			return nil, fmt.Errorf("%w: ee_permobject_rules is missing required column %s", ErrPlanBlocked, name)
		}
	}
	selects := make([]string, 0, 21)
	for _, item := range []struct{ name, fallback string }{
		{"id", "''"}, {"accessor_id", "''"}, {"subject_type", "'unknown'"},
		{"resource_type", "''"}, {"resource_id", "''"}, {"op", "''"}, {"effect", "''"},
		{"expires_at", "NULL"}, {"granted_by", "''"}, {"reason", "''"},
		{"classification", "'unclassified'"}, {"classification_evidence", "''"},
		{"activation_state", "'inactive'"}, {"activated_at", "NULL"}, {"activated_by", "''"},
		{"activation_ref", "''"}, {"revoked_at", "NULL"}, {"revoked_by", "''"},
		{"revoke_reason", "''"}, {"created_at", "NULL"}, {"updated_at", "NULL"},
	} {
		if _, ok := available[item.name]; ok {
			selects = append(selects, item.name)
		} else {
			selects = append(selects, item.fallback+" AS "+item.name)
		}
	}
	var rows []eeRuleRow
	if err := db.WithContext(ctx).Table("ee_permobject_rules").Select(strings.Join(selects, ", ")).Order("id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("inventory ee_permobject_rules: %w", err)
	}
	return rows, nil
}

func loadSubjectFacts(ctx context.Context, db *gorm.DB) (subjectFacts, error) {
	facts := subjectFacts{
		users: make(map[string]userRow), roles: make(map[string]roleRow),
		departments: make(map[string]struct{}), roleMembers: make(map[string]int),
	}
	for _, table := range []any{&userRow{}, &roleRow{}, &departmentRow{}, &casbinPolicyRow{}} {
		if !db.Migrator().HasTable(table) {
			return facts, fmt.Errorf("%w: authoritative subject directory or role membership table is missing", ErrPlanBlocked)
		}
	}
	var users []userRow
	if err := db.WithContext(ctx).Find(&users).Error; err != nil {
		return facts, err
	}
	for _, user := range users {
		facts.users[user.ID] = user
	}
	var roles []roleRow
	if err := db.WithContext(ctx).Find(&roles).Error; err != nil {
		return facts, err
	}
	for _, role := range roles {
		facts.roles[role.ID] = role
	}
	var departments []departmentRow
	if err := db.WithContext(ctx).Find(&departments).Error; err != nil {
		return facts, err
	}
	for _, department := range departments {
		facts.departments[department.ID] = struct{}{}
	}
	var memberships []casbinPolicyRow
	if err := db.WithContext(ctx).Where("ptype = ?", "g").Find(&memberships).Error; err != nil {
		return facts, err
	}
	parentsBySubject := make(map[string][]string)
	for _, membership := range memberships {
		if _, roleExists := facts.roles[membership.V1]; roleExists {
			parentsBySubject[membership.V0] = append(parentsBySubject[membership.V0], membership.V1)
		}
	}
	queue := make([]string, 0, len(facts.users))
	seenSubjects := make(map[string]struct{}, len(facts.users)+len(facts.roles))
	for id, user := range facts.users {
		if user.Enabled {
			queue = append(queue, id)
			seenSubjects[id] = struct{}{}
		}
	}
	for len(queue) > 0 {
		subject := queue[0]
		queue = queue[1:]
		for _, parentRole := range parentsBySubject[subject] {
			facts.roleMembers[parentRole]++
			if _, seen := seenSubjects[parentRole]; !seen {
				seenSubjects[parentRole] = struct{}{}
				queue = append(queue, parentRole)
			}
		}
	}
	return facts, nil
}

func classifyEERow(row eeRuleRow, facts subjectFacts, evidence EERuleEvidence, now time.Time) EERulePlan {
	currentSubject := row.SubjectType
	if currentSubject == "" {
		currentSubject = eeSubjectUnknown
	}
	currentClassification := row.Classification
	if currentClassification == "" || currentClassification == "unclassified" {
		currentClassification = "unclassified"
	}
	currentActivation := normalizedEEActivation(row.ActivationState)
	plan := EERulePlan{
		GrantID: row.ID, AccessorID: row.AccessorID, ResourceType: row.ResourceType,
		ResourceID: row.ResourceID, Operation: row.Op, Effect: row.Effect, ExpiresAt: row.ExpiresAt,
		CurrentSubjectType: currentSubject, CurrentClassification: currentClassification,
		CurrentActivation: currentActivation, PlannedActivation: currentActivation,
		PublishedWriterEvidence: evidence.PublishedWriterEvidence,
		RuntimeUsageEvidence:    evidence.RuntimeUsageEvidence,
		ClassificationEvidence:  row.ClassificationEvidence, ReviewPriority: "normal",
	}
	if row.Effect == "deny" {
		plan.ReviewPriority = "high"
	}

	subjectType, subjectReason, subjectValid := authoritativeSubject(row.AccessorID, facts)
	plan.PlannedSubjectType = subjectType
	rowValid := strings.TrimSpace(row.ID) != "" && len(row.ID) <= 64 &&
		strings.TrimSpace(row.AccessorID) != "" && strings.TrimSpace(row.ResourceType) != "" &&
		strings.TrimSpace(row.ResourceID) != "" && strings.TrimSpace(row.Op) != "" &&
		(row.Effect == "allow" || row.Effect == "deny")
	if currentSubject != eeSubjectUnknown && currentSubject != subjectType {
		rowValid = false
		subjectValid = false
		subjectReason = "persisted subject_type conflicts with the authoritative directory"
	}

	persistedPublished := row.Classification == EEClassificationPublished &&
		strings.TrimSpace(row.ClassificationEvidence) != "" && strings.TrimSpace(row.ActivatedBy) != "" &&
		strings.TrimSpace(row.ActivationRef) != "" && row.ActivatedAt != nil
	evidenceComplete := strings.TrimSpace(evidence.GrantID) != "" &&
		strings.TrimSpace(evidence.PublishedWriterEvidence) != "" && strings.TrimSpace(evidence.RuntimeUsageEvidence) != ""
	evidenceMatches := evidenceComplete && evidence.ExpectedSubjectType == subjectType

	switch {
	case !rowValid || !subjectValid:
		plan.PlannedClassification = EEClassificationInvalid
		plan.Reason = subjectReason
	case persistedPublished:
		plan.PlannedClassification = EEClassificationPublished
		plan.ClassificationEvidence = row.ClassificationEvidence
		plan.Reason = "existing activation carries complete published-path evidence"
	case evidenceMatches:
		plan.PlannedClassification = EEClassificationPublished
		plan.ClassificationEvidence = "writer=" + evidence.PublishedWriterEvidence + ";runtime=" + evidence.RuntimeUsageEvidence
		plan.Reason = "published writer and previous runtime participation are both proven"
	default:
		plan.PlannedClassification = EEClassificationDormant
		plan.Reason = "published writer and previous runtime participation are not both proven"
		if evidenceComplete && evidence.ExpectedSubjectType != subjectType {
			plan.PlannedClassification = EEClassificationInvalid
			plan.Reason = "evidence subject type conflicts with the authoritative directory"
		}
	}

	plan.ActivationEligible = plan.PlannedClassification == EEClassificationPublished &&
		(subjectType == eeSubjectUser || subjectType == eeSubjectRole) && currentActivation != eeActivationRevoked
	if subjectType == eeSubjectRole && facts.roleMembers[row.AccessorID] == 0 {
		plan.ActivationEligible = false
		plan.Reason += "; role has no trusted real membership for activation smoke testing"
	}
	if row.ExpiresAt != nil && !row.ExpiresAt.After(now) {
		plan.ActivationEligible = false
		plan.Reason += "; rule is expired"
	}
	if currentActivation != eeActivationActive && currentActivation != eeActivationRevoked {
		plan.PlannedActivation = eeActivationInactive
	}
	return plan
}

func authoritativeSubject(id string, facts subjectFacts) (string, string, bool) {
	user, isUser := facts.users[id]
	_, isRole := facts.roles[id]
	_, isDepartment := facts.departments[id]
	matches := 0
	if isUser {
		matches++
	}
	if isRole {
		matches++
	}
	if isDepartment {
		matches++
	}
	if matches > 1 {
		return eeSubjectUnknown, "subject id matches multiple authoritative subject types", false
	}
	if isDepartment {
		return eeSubjectDepartment, "department subjects are outside the current Enterprise runtime", false
	}
	if isRole {
		return eeSubjectRole, "authoritative role", true
	}
	if isUser {
		if !user.Enabled {
			return eeSubjectUser, "authoritative user is disabled", false
		}
		return eeSubjectUser, "authoritative enabled user", true
	}
	return eeSubjectUnknown, "subject does not exist in the authoritative user or role directory", false
}

func indexEERuleEvidence(items []EERuleEvidence) (map[string]EERuleEvidence, []string) {
	result := make(map[string]EERuleEvidence, len(items))
	var problems []string
	for i, item := range items {
		if strings.TrimSpace(item.GrantID) == "" ||
			(item.ExpectedSubjectType != eeSubjectUser && item.ExpectedSubjectType != eeSubjectRole) ||
			strings.TrimSpace(item.PublishedWriterEvidence) == "" || strings.TrimSpace(item.RuntimeUsageEvidence) == "" {
			problems = append(problems, fmt.Sprintf("EE rule evidence %d is incomplete", i))
			continue
		}
		if _, duplicate := result[item.GrantID]; duplicate {
			problems = append(problems, fmt.Sprintf("EE rule evidence %d duplicates grant_id %s", i, item.GrantID))
			continue
		}
		result[item.GrantID] = item
	}
	return result, problems
}

func validateActivationConfirmation(report EEReport, confirmation *EEActivationConfirmation) (EEReport, error) {
	if confirmation == nil || len(confirmation.ConfirmedGrantIDs) == 0 {
		report.Summary = summarizeEE(report.Rules)
		return report, nil
	}
	if confirmation.InventoryDigest != report.InventoryDigest || strings.TrimSpace(confirmation.OperatorID) == "" ||
		strings.TrimSpace(confirmation.EvidenceRef) == "" || confirmation.ConfirmedAt.IsZero() {
		report.Anomalies = append(report.Anomalies, "administrator activation confirmation is incomplete or refers to a different dry-run inventory")
		return report, fmt.Errorf("%w: invalid administrator activation confirmation", ErrPlanBlocked)
	}
	byID := make(map[string]*EERulePlan, len(report.Rules))
	for i := range report.Rules {
		byID[report.Rules[i].GrantID] = &report.Rules[i]
	}
	seen := make(map[string]struct{}, len(confirmation.ConfirmedGrantIDs))
	for _, id := range confirmation.ConfirmedGrantIDs {
		if _, duplicate := seen[id]; duplicate {
			report.Anomalies = append(report.Anomalies, "activation confirmation repeats grant_id "+id)
			continue
		}
		seen[id] = struct{}{}
		rule := byID[id]
		if rule == nil || (!rule.ActivationEligible && rule.CurrentActivation != eeActivationActive) {
			report.Anomalies = append(report.Anomalies, "activation confirmation includes ineligible or unknown grant_id "+id)
			continue
		}
		rule.PlannedActivation = eeActivationActive
	}
	if report.Blocked() {
		return report, fmt.Errorf("%w: %s", ErrPlanBlocked, strings.Join(report.Anomalies, "; "))
	}
	report.Summary = summarizeEE(report.Rules)
	return report, nil
}

func digestEEInventory(report EEReport) string {
	type digestRule struct {
		GrantID, AccessorID, ResourceType, ResourceID, Operation, Effect string
		ExpiresAt                                                        *time.Time
		SubjectType, Classification, ClassificationEvidence              string
	}
	payload := struct {
		Version, TableState, AssemblyEvidence string
		AssemblyHistory                       bool
		Rules                                 []digestRule
	}{Version: report.MigrationVersion, TableState: report.TableState,
		AssemblyHistory: report.Assembly.WasAssembled, AssemblyEvidence: report.Assembly.EvidenceRef}
	for _, rule := range report.Rules {
		payload.Rules = append(payload.Rules, digestRule{
			rule.GrantID, rule.AccessorID, rule.ResourceType, rule.ResourceID, rule.Operation,
			rule.Effect, rule.ExpiresAt, rule.PlannedSubjectType, rule.PlannedClassification,
			rule.ClassificationEvidence,
		})
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func summarizeEE(rules []EERulePlan) EESummary {
	summary := EESummary{Rows: len(rules)}
	for _, rule := range rules {
		switch rule.PlannedClassification {
		case EEClassificationPublished:
			summary.Published++
		case EEClassificationDormant:
			summary.Dormant++
		case EEClassificationInvalid:
			summary.Invalid++
		}
		if rule.CurrentActivation == eeActivationActive {
			summary.Active++
		}
		if rule.CurrentActivation != eeActivationActive && rule.PlannedActivation == eeActivationActive {
			summary.ToActivate++
		}
		if rule.Effect == "deny" {
			summary.Deny++
		}
	}
	return summary
}

func normalizedEEActivation(value string) string {
	if value == "" {
		return eeActivationInactive
	}
	return value
}
