// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type casbinPolicyRow struct {
	ID    uint   `gorm:"primaryKey;autoIncrement"`
	Ptype string `gorm:"size:100"`
	V0    string `gorm:"size:100"`
	V1    string `gorm:"size:100"`
	V2    string `gorm:"size:100"`
	V3    string `gorm:"size:100"`
	V4    string `gorm:"size:100"`
	V5    string `gorm:"size:100"`
}

func (casbinPolicyRow) TableName() string { return "casbin_rule" }

type policyTuple struct {
	accessor, object, operation, effect, policySource, authoritySource string
}

var bknChildResourceTypes = map[string]struct{}{
	"concept_group": {}, "object_type": {}, "relation_type": {},
	"action_type": {}, "metric": {}, "risk_type": {},
}

var bknResourceTypes = map[string]struct{}{
	"knowledge_network": {}, "concept_group": {}, "object_type": {},
	"relation_type": {}, "action_type": {}, "metric": {}, "risk_type": {},
}

// PlanCore performs only SELECT and schema-introspection operations. In
// particular it never calls AutoMigrate, even when the new grant table does not
// exist yet, so a dry-run is safe against an upgrade backup.
func PlanCore(ctx context.Context, db *gorm.DB, evidence []LifecycleEvidence) (CoreReport, error) {
	report := CoreReport{MigrationVersion: CurrentVersion, Policies: []CorePolicyPlan{}}
	if db == nil {
		return report, fmt.Errorf("plan core authorization migration: nil database")
	}

	rows, err := loadPolicyRows(ctx, db)
	if err != nil {
		return report, err
	}
	roles, err := loadRoles(ctx, db, len(rows) > 0)
	if err != nil {
		return report, err
	}
	grants, err := loadGrantRows(ctx, db)
	if err != nil {
		return report, err
	}

	evidenceByTuple, evidenceErrs := validateLifecycleEvidence(evidence)
	report.Anomalies = append(report.Anomalies, evidenceErrs...)
	finalTuples := make(map[policyTuple]int)
	removedTuples := make(map[policyTuple]struct{})
	usedEvidence := make(map[policyTuple]struct{})

	for _, row := range rows {
		plan := planPolicy(row, roles, evidenceByTuple)
		if plan.Operation == "execute_action" {
			plan.Anomalies = append(plan.Anomalies,
				"operation execute_action is not a persisted permission operation; migration must stop")
		}
		if len(plan.Anomalies) > 0 {
			report.Anomalies = append(report.Anomalies,
				fmt.Sprintf("casbin policy %d: %s", row.ID, strings.Join(plan.Anomalies, "; ")))
		}
		current := tupleFromPlan(plan, false)
		if plan.Action == ActionRemove {
			removedTuples[current] = struct{}{}
		} else {
			final := tupleFromPlan(plan, true)
			finalTuples[final]++
			if _, ok := evidenceByTuple[final]; ok && plan.Effect == authz.EffectAllow {
				usedEvidence[final] = struct{}{}
			}
		}
		report.Policies = append(report.Policies, plan)
	}

	for key, fact := range evidenceByTuple {
		if _, used := usedEvidence[key]; used {
			continue
		}
		plan := CorePolicyPlan{
			AccessorID: fact.AccessorID, Object: fact.Object, Operation: fact.Operation,
			Effect: authz.EffectAllow, PlannedPolicySource: string(authz.PolicySourceSystemDerived),
			PlannedAuthoritySource: string(authz.AuthoritySourceSystem), Action: ActionInsert,
			Reason:      "authoritative lifecycle evidence requires a missing system-derived grant",
			EvidenceRef: fact.EvidenceRef,
		}
		finalTuples[key]++
		report.Policies = append(report.Policies, plan)
	}
	duplicateProjections := make([]string, 0)
	for key, count := range finalTuples {
		if count > 1 {
			duplicateProjections = append(duplicateProjections,
				fmt.Sprintf("%s/%s/%s/%s/%s/%s (%d rows)", key.accessor, key.object,
					key.operation, key.effect, key.policySource, key.authoritySource, count))
		}
	}
	sort.Strings(duplicateProjections)
	for _, projection := range duplicateProjections {
		report.Anomalies = append(report.Anomalies, "duplicate final Casbin projection "+projection)
	}

	grantIDsByTuple := make(map[policyTuple][]string)
	seenGrantIDs := make(map[string]struct{}, len(grants))
	for _, grant := range grants {
		key := tupleFromGrant(grant)
		problems := validateGrant(grant)
		if _, duplicate := seenGrantIDs[grant.GrantID]; duplicate {
			problems = append(problems, "duplicate grant_id")
		}
		seenGrantIDs[grant.GrantID] = struct{}{}
		if _, removed := removedTuples[key]; !removed && finalTuples[key] == 0 {
			problems = append(problems, "grant has no matching final Casbin projection")
		}
		if len(problems) > 0 {
			report.Anomalies = append(report.Anomalies,
				fmt.Sprintf("authorization grant %q: %s", grant.GrantID, strings.Join(problems, "; ")))
		}
		grantIDsByTuple[key] = append(grantIDsByTuple[key], grant.GrantID)
	}

	for i := range report.Policies {
		plan := &report.Policies[i]
		if plan.Action == ActionRemove {
			plan.ExistingGrantIDs = sortedStrings(grantIDsByTuple[tupleFromPlan(*plan, false)])
			continue
		}
		key := tupleFromPlan(*plan, true)
		plan.ExistingGrantIDs = sortedStrings(grantIDsByTuple[key])
		if len(plan.ExistingGrantIDs) == 0 {
			plan.CreateGrantIDs = []string{deterministicGrantID(key)}
		}
	}

	sort.Slice(report.Policies, func(i, j int) bool {
		left, right := report.Policies[i], report.Policies[j]
		if left.PolicyID != right.PolicyID {
			if left.PolicyID == 0 {
				return false
			}
			if right.PolicyID == 0 {
				return true
			}
			return left.PolicyID < right.PolicyID
		}
		return strings.Join([]string{left.AccessorID, left.Object, left.Operation}, "\x00") <
			strings.Join([]string{right.AccessorID, right.Object, right.Operation}, "\x00")
	})
	report.Summary = summarize(report.Policies)
	if report.Blocked() {
		return report, fmt.Errorf("%w: %s", ErrPlanBlocked, strings.Join(report.Anomalies, "; "))
	}
	return report, nil
}

// ApplyCore executes a previously expressible Core plan in one data
// transaction. Schema additions happen first because MySQL-family DDL commits
// implicitly; the maintenance-window backup remains the rollback mechanism.
// A second plan proves that the resulting data is complete and idempotent.
func ApplyCore(ctx context.Context, db *gorm.DB, evidence []LifecycleEvidence) (CoreReport, error) {
	plan, err := PlanCore(ctx, db, evidence)
	if err != nil {
		return plan, err
	}
	if err := db.WithContext(ctx).AutoMigrate(&casbinPolicyRow{}, &safemodel.AuthorizationGrant{}); err != nil {
		return plan, fmt.Errorf("prepare authorization migration schema: %w", err)
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range plan.Policies {
			current := tupleFromPlan(item, false)
			switch item.Action {
			case ActionRemove:
				if item.PolicyID != 0 {
					if err := tx.Delete(&casbinPolicyRow{}, item.PolicyID).Error; err != nil {
						return err
					}
				}
				if err := deleteGrantTuple(tx, current); err != nil {
					return err
				}
			case ActionClassify:
				if err := tx.Model(&casbinPolicyRow{}).Where("id = ?", item.PolicyID).Updates(map[string]any{
					"v3": item.Effect, "v4": item.PlannedPolicySource, "v5": item.PlannedAuthoritySource,
				}).Error; err != nil {
					return err
				}
			case ActionInsert:
				row := casbinPolicyRow{Ptype: "p", V0: item.AccessorID, V1: item.Object,
					V2: item.Operation, V3: item.Effect, V4: item.PlannedPolicySource,
					V5: item.PlannedAuthoritySource}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			case ActionKeep:
				// No projection update.
			default:
				return fmt.Errorf("unsupported migration action %q", item.Action)
			}
			for _, grantID := range item.CreateGrantIDs {
				if err := tx.Create(grantModel(grantID, tupleFromPlan(item, true))).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return plan, fmt.Errorf("apply core authorization migration: %w", err)
	}

	verified, err := PlanCore(ctx, db, evidence)
	if err != nil {
		return verified, fmt.Errorf("verify core authorization migration: %w", err)
	}
	if verified.HasChanges() {
		return verified, fmt.Errorf("verify core authorization migration: %w: plan is not idempotent", ErrPlanBlocked)
	}
	return verified, nil
}

func loadPolicyRows(ctx context.Context, db *gorm.DB) ([]casbinPolicyRow, error) {
	if !db.Migrator().HasTable(&casbinPolicyRow{}) {
		return nil, nil
	}
	var rows []casbinPolicyRow
	if err := db.WithContext(ctx).Where("ptype = ?", "p").Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("inventory Casbin policies: %w", err)
	}
	return rows, nil
}

func loadRoles(ctx context.Context, db *gorm.DB, required bool) (map[string]safemodel.Role, error) {
	roles := make(map[string]safemodel.Role)
	if !db.Migrator().HasTable(&safemodel.Role{}) {
		if required {
			return nil, fmt.Errorf("inventory roles: authoritative roles table is missing")
		}
		return roles, nil
	}
	var rows []safemodel.Role
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("inventory roles: %w", err)
	}
	for _, role := range rows {
		roles[role.ID] = role
	}
	return roles, nil
}

func loadGrantRows(ctx context.Context, db *gorm.DB) ([]safemodel.AuthorizationGrant, error) {
	if !db.Migrator().HasTable(&safemodel.AuthorizationGrant{}) {
		return nil, nil
	}
	var rows []safemodel.AuthorizationGrant
	if err := db.WithContext(ctx).Order("grant_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("inventory authorization grants: %w", err)
	}
	return rows, nil
}

func planPolicy(row casbinPolicyRow, roles map[string]safemodel.Role, evidence map[policyTuple]LifecycleEvidence) CorePolicyPlan {
	effect := row.V3
	if effect == "" {
		effect = authz.EffectAllow
	}
	plan := CorePolicyPlan{
		PolicyID: row.ID, AccessorID: row.V0, Object: row.V1, Operation: row.V2, Effect: effect,
		CurrentPolicySource: row.V4, CurrentAuthoritySource: row.V5,
		PlannedPolicySource: row.V4, PlannedAuthoritySource: row.V5, Action: ActionKeep,
		Reason: "trusted provenance is already present",
	}
	_, resourceID, hasObjectType := strings.Cut(row.V1, ":")
	if strings.TrimSpace(row.V0) == "" || strings.TrimSpace(row.V2) == "" ||
		!hasObjectType || objectType(row.V1) == "" || strings.TrimSpace(resourceID) == "" {
		plan.Anomalies = append(plan.Anomalies, "policy has an incomplete accessor, object, or operation identity")
	}
	if effect != authz.EffectAllow && effect != authz.EffectDeny {
		plan.Anomalies = append(plan.Anomalies, "policy has invalid effect "+effect)
	}
	resourceType := objectType(row.V1)
	if shouldRemove(resourceType, row.V2) {
		plan.Action = ActionRemove
		plan.PlannedPolicySource, plan.PlannedAuthoritySource = "", ""
		plan.Reason = "approved transitional BKN operation has no published business authorization point"
		return plan
	}

	if row.V4 == "" && row.V5 == "" {
		key := policyTuple{row.V0, row.V1, row.V2, effect,
			string(authz.PolicySourceSystemDerived), string(authz.AuthoritySourceSystem)}
		if fact, ok := evidence[key]; ok && effect == authz.EffectAllow {
			plan.PlannedPolicySource = string(authz.PolicySourceSystemDerived)
			plan.PlannedAuthoritySource = string(authz.AuthoritySourceSystem)
			plan.Reason = "matched authoritative resource-lifecycle evidence"
			plan.EvidenceRef = fact.EvidenceRef
		} else if role, ok := roles[row.V0]; ok {
			plan.PlannedPolicySource = string(authz.PolicySourceRolePermission)
			if role.BuiltIn() {
				plan.PlannedAuthoritySource = string(authz.AuthoritySourceSystem)
			} else {
				plan.PlannedAuthoritySource = string(authz.AuthoritySourceAdminAuthz)
			}
			plan.Reason = "subject is an authoritative built-in or custom role"
		} else {
			plan.PlannedPolicySource = string(authz.PolicySourceLegacy)
			plan.PlannedAuthoritySource = string(authz.AuthoritySourceMigration)
			plan.Reason = "historical Core policy has no provable newer source"
		}
		plan.Action = ActionClassify
		return plan
	}
	if row.V4 == "" || row.V5 == "" {
		plan.Anomalies = append(plan.Anomalies, "policy provenance is only partially populated")
		return plan
	}
	if !validPolicySource(row.V4) {
		plan.Anomalies = append(plan.Anomalies, "unknown policy_source "+row.V4)
	}
	if !validAuthoritySource(row.V5) {
		plan.Anomalies = append(plan.Anomalies, "unknown authority_source "+row.V5)
	}
	if row.V4 == string(authz.PolicySourceRolePermission) {
		if _, ok := roles[row.V0]; !ok {
			plan.Anomalies = append(plan.Anomalies, "role_permission subject does not exist in the authoritative role directory")
		}
	}
	return plan
}

func validateLifecycleEvidence(items []LifecycleEvidence) (map[policyTuple]LifecycleEvidence, []string) {
	result := make(map[policyTuple]LifecycleEvidence, len(items))
	var anomalies []string
	for i, item := range items {
		validContract := item.Kind == "knowledge_network_owner" && objectType(item.Object) == "knowledge_network" && item.Operation == "authorize"
		validContract = validContract || item.Kind == "action_type_creator" && objectType(item.Object) == "action_type" && item.Operation == "execute"
		if strings.TrimSpace(item.AccessorID) == "" || strings.TrimSpace(item.EvidenceRef) == "" ||
			strings.HasSuffix(item.Object, ":*") || !validContract {
			anomalies = append(anomalies, fmt.Sprintf("lifecycle evidence %d is incomplete or outside the approved owner/creator contracts", i))
			continue
		}
		key := policyTuple{item.AccessorID, item.Object, item.Operation, authz.EffectAllow,
			string(authz.PolicySourceSystemDerived), string(authz.AuthoritySourceSystem)}
		if previous, found := result[key]; found && previous.EvidenceRef != item.EvidenceRef {
			anomalies = append(anomalies, fmt.Sprintf("lifecycle evidence %d conflicts with another fact for %s", i, item.Object))
			continue
		}
		result[key] = item
	}
	return result, anomalies
}

func shouldRemove(resourceType, operation string) bool {
	if operation == "authorize" {
		_, child := bknChildResourceTypes[resourceType]
		return child
	}
	if operation == "task_manage" {
		_, bkn := bknResourceTypes[resourceType]
		return bkn
	}
	return false
}

func objectType(object string) string {
	typ, _, found := strings.Cut(object, ":")
	if !found {
		return ""
	}
	return typ
}

func tupleFromPlan(plan CorePolicyPlan, planned bool) policyTuple {
	source, authority := plan.CurrentPolicySource, plan.CurrentAuthoritySource
	if planned {
		source, authority = plan.PlannedPolicySource, plan.PlannedAuthoritySource
	}
	return policyTuple{plan.AccessorID, plan.Object, plan.Operation, plan.Effect, source, authority}
}

func tupleFromGrant(grant safemodel.AuthorizationGrant) policyTuple {
	return policyTuple{grant.AccessorID, grant.Object, grant.Operation, grant.Effect,
		grant.PolicySource, grant.AuthoritySource}
}

func validateGrant(grant safemodel.AuthorizationGrant) []string {
	var problems []string
	if strings.TrimSpace(grant.GrantID) == "" || len(grant.GrantID) > 64 {
		problems = append(problems, "invalid stable grant_id")
	}
	if strings.TrimSpace(grant.AccessorID) == "" || strings.TrimSpace(grant.Object) == "" ||
		strings.TrimSpace(grant.Operation) == "" || strings.TrimSpace(grant.CreatedBy) == "" {
		problems = append(problems, "incomplete grant identity or audit metadata")
	}
	if grant.Effect != authz.EffectAllow && grant.Effect != authz.EffectDeny {
		problems = append(problems, "invalid effect")
	}
	if !validPolicySource(grant.PolicySource) || !validAuthoritySource(grant.AuthoritySource) {
		problems = append(problems, "invalid provenance")
	}
	if grant.ProjectionKey != projectionKey(tupleFromGrant(grant)) {
		problems = append(problems, "invalid projection_key")
	}
	return problems
}

func validPolicySource(value string) bool {
	switch authz.PolicySource(value) {
	case authz.PolicySourceCommunityBundle, authz.PolicySourceProfessionalRule,
		authz.PolicySourceLegacy, authz.PolicySourceSystemDerived, authz.PolicySourceRolePermission:
		return true
	default:
		return false
	}
}

func validAuthoritySource(value string) bool {
	switch authz.AuthoritySource(value) {
	case authz.AuthoritySourceAdminAuthz, authz.AuthoritySourceOwnerDelegate,
		authz.AuthoritySourceSystem, authz.AuthoritySourceMigration:
		return true
	default:
		return false
	}
}

func projectionKey(key policyTuple) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{key.accessor, key.object, key.operation,
		key.effect, key.policySource, key.authoritySource}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func deterministicGrantID(key policyTuple) string { return projectionKey(key) }

func grantModel(id string, key policyTuple) *safemodel.AuthorizationGrant {
	return &safemodel.AuthorizationGrant{
		GrantID: id, ProjectionKey: projectionKey(key), AccessorID: key.accessor,
		Object: key.object, Operation: key.operation, Effect: key.effect,
		PolicySource: key.policySource, AuthoritySource: key.authoritySource,
		CreatedBy: key.authoritySource,
	}
}

func deleteGrantTuple(db *gorm.DB, key policyTuple) error {
	return db.Where("accessor_id = ? AND object = ? AND operation = ? AND effect = ? AND policy_source = ? AND authority_source = ?",
		key.accessor, key.object, key.operation, key.effect, key.policySource, key.authoritySource).
		Delete(&safemodel.AuthorizationGrant{}).Error
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func summarize(items []CorePolicyPlan) CoreSummary {
	summary := CoreSummary{Policies: len(items)}
	for _, item := range items {
		summary.GrantsExisting += len(item.ExistingGrantIDs)
		summary.GrantsToCreate += len(item.CreateGrantIDs)
		switch item.Action {
		case ActionKeep:
			summary.Kept++
		case ActionClassify:
			summary.Classified++
		case ActionInsert:
			summary.Inserted++
		case ActionRemove:
			summary.Removed++
		}
	}
	return summary
}
