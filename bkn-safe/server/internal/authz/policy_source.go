// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PolicySource identifies the product or lifecycle layer that owns a policy.
// It is persisted in Casbin v4; it is not accepted from an authorization HTTP
// request. Enterprise property policy remains in the EE-owned store and is not
// represented by this Core policy vocabulary.
type PolicySource string

const (
	PolicySourceCommunityBundle  PolicySource = "community_bundle"
	PolicySourceProfessionalRule PolicySource = "professional_rule"
	PolicySourceLegacy           PolicySource = "legacy"
	PolicySourceSystemDerived    PolicySource = "system_derived"
	PolicySourceRolePermission   PolicySource = "role_permission"
)

// ActFullBusinessAccess is the persisted logical Community operation. Its
// reviewed per-resource expansion is implemented by the shared grant index in
// #1427; it must never be materialized into ordinary operation rows.
const ActFullBusinessAccess = "full_business_access"

// AuthoritySource identifies the trusted server path that wrote a policy. It
// controls who may manage a rule; it deliberately has no runtime precedence.
// It is persisted in Casbin v5.
type AuthoritySource string

const (
	AuthoritySourceAdminAuthz    AuthoritySource = "admin_authz"
	AuthoritySourceOwnerDelegate AuthoritySource = "owner_delegate"
	AuthoritySourceSystem        AuthoritySource = "system"
	AuthoritySourceMigration     AuthoritySource = "migration"
)

var (
	// ErrPolicySourceMigrationRequired is returned before Casbin loads when a
	// persisted business policy has not been assigned trusted provenance and a
	// stable grant identity. The offline migration owns that classification;
	// startup must not infer or synthesize it.
	ErrPolicySourceMigrationRequired = errors.New("casbin policy provenance migration required")
)

// PolicyGrant is one independently managed Core grant. GrantID and CreatedBy
// are mandatory for source-aware writers; compatibility writers derive a
// deterministic ID and use their trusted authority as the creator identity.
type PolicyGrant struct {
	GrantID         string
	AccessorID      string
	Object          string
	Operation       string
	Effect          string
	PolicySource    PolicySource
	AuthoritySource AuthoritySource
	CreatedBy       string
}

// PolicyRecord is the durable identity and provenance of one Core grant.
// Active is derived from the live edition at read time and is never persisted.
// Several records may project to the same Casbin p-line.
type PolicyRecord struct {
	GrantID         string
	AccessorID      string
	Object          string
	Operation       string
	Effect          string
	PolicySource    PolicySource
	AuthoritySource AuthoritySource
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Active          bool
}

// PolicyFilter narrows a provenance listing. Zero fields match every grant.
type PolicyFilter struct {
	GrantID         string
	AccessorID      string
	Object          string
	Operation       string
	Effect          string
	PolicySource    PolicySource
	AuthoritySource AuthoritySource
}

// casbinPolicyRow mirrors the adapter table without making the adapter's
// concrete model part of bkn-safe's API.
type casbinPolicyRow struct {
	ID    uint
	Ptype string
	V0    string
	V1    string
	V2    string
	V3    string
	V4    string
	V5    string
}

func (casbinPolicyRow) TableName() string { return "casbin_rule" }

type policyTupleKey struct {
	AccessorID      string
	Object          string
	Operation       string
	Effect          string
	PolicySource    string
	AuthoritySource string
}

func validatePolicySource(source PolicySource) error {
	switch source {
	case PolicySourceCommunityBundle, PolicySourceProfessionalRule, PolicySourceLegacy,
		PolicySourceSystemDerived, PolicySourceRolePermission:
		return nil
	default:
		return fmt.Errorf("unknown policy source %q", source)
	}
}

func validateAuthoritySource(source AuthoritySource) error {
	switch source {
	case AuthoritySourceAdminAuthz, AuthoritySourceOwnerDelegate, AuthoritySourceSystem, AuthoritySourceMigration:
		return nil
	default:
		return fmt.Errorf("unknown authority source %q", source)
	}
}

func validatePolicyProvenance(source PolicySource, authority AuthoritySource) error {
	if err := validatePolicySource(source); err != nil {
		return err
	}
	return validateAuthoritySource(authority)
}

// policySourceActive implements the effective-layer formula. Professional
// configuration is retained but inactive in Community. Every other Core source
// remains active at every tier; Enterprise property narrowing is composed by
// the existing EE extension after this decision.
func policySourceActive(source PolicySource, edition licverify.Edition) bool {
	switch source {
	case PolicySourceProfessionalRule:
		return edition.AtLeast(licverify.EditionProfessional)
	case PolicySourceCommunityBundle, PolicySourceLegacy, PolicySourceSystemDerived, PolicySourceRolePermission:
		return true
	default:
		return false
	}
}

func policySourceOf(row []string) PolicySource {
	if len(row) <= 4 {
		return ""
	}
	return PolicySource(row[4])
}

func authoritySourceOf(row []string) AuthoritySource {
	if len(row) <= 5 {
		return ""
	}
	return AuthoritySource(row[5])
}

func activePolicyRows(rows [][]string) [][]string {
	return activePolicyRowsForEdition(rows, entitlement.Current())
}

func activePolicyRowsForEdition(rows [][]string, edition licverify.Edition) [][]string {
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		if policySourceActive(policySourceOf(row), edition) {
			out = append(out, row)
		}
	}
	return out
}

func validatePolicyGrant(grant PolicyGrant) error {
	if strings.TrimSpace(grant.GrantID) == "" || len(grant.GrantID) > 64 {
		return fmt.Errorf("invalid grant id")
	}
	if strings.TrimSpace(grant.AccessorID) == "" || len(grant.AccessorID) > 64 {
		return fmt.Errorf("invalid grant accessor")
	}
	if strings.TrimSpace(grant.Object) == "" || len(grant.Object) > 255 {
		return fmt.Errorf("invalid grant object")
	}
	if strings.TrimSpace(grant.Operation) == "" || len(grant.Operation) > 64 {
		return fmt.Errorf("invalid grant operation")
	}
	if strings.TrimSpace(grant.CreatedBy) == "" || len(grant.CreatedBy) > 64 {
		return fmt.Errorf("invalid grant creator")
	}
	if grant.Effect != EffectAllow && grant.Effect != EffectDeny {
		return fmt.Errorf("invalid policy effect %q", grant.Effect)
	}
	return validatePolicyProvenance(grant.PolicySource, grant.AuthoritySource)
}

func policyProjectionKey(sub, object, operation, effect string, source PolicySource, authority AuthoritySource) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		sub, object, operation, effect, string(source), string(authority),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func deterministicGrantID(sub, object, operation, effect string, source PolicySource, authority AuthoritySource) string {
	return policyProjectionKey(sub, object, operation, effect, source, authority)
}

func deterministicPolicyGrant(sub, object, operation, effect string, source PolicySource, authority AuthoritySource) PolicyGrant {
	return PolicyGrant{
		GrantID:    deterministicGrantID(sub, object, operation, effect, source, authority),
		AccessorID: sub, Object: object, Operation: operation, Effect: effect,
		PolicySource: source, AuthoritySource: authority, CreatedBy: string(authority),
	}
}

func grantModel(grant PolicyGrant) safemodel.AuthorizationGrant {
	return safemodel.AuthorizationGrant{
		GrantID: grant.GrantID,
		ProjectionKey: policyProjectionKey(grant.AccessorID, grant.Object, grant.Operation, grant.Effect,
			grant.PolicySource, grant.AuthoritySource),
		AccessorID: grant.AccessorID, Object: grant.Object,
		Operation: grant.Operation, Effect: grant.Effect, PolicySource: string(grant.PolicySource),
		AuthoritySource: string(grant.AuthoritySource), CreatedBy: grant.CreatedBy,
	}
}

func policyGrant(row safemodel.AuthorizationGrant) PolicyGrant {
	return PolicyGrant{
		GrantID: row.GrantID, AccessorID: row.AccessorID, Object: row.Object,
		Operation: row.Operation, Effect: row.Effect, PolicySource: PolicySource(row.PolicySource),
		AuthoritySource: AuthoritySource(row.AuthoritySource), CreatedBy: row.CreatedBy,
	}
}

func samePolicyGrant(left, right PolicyGrant) bool {
	return left.GrantID == right.GrantID && left.AccessorID == right.AccessorID &&
		left.Object == right.Object && left.Operation == right.Operation && left.Effect == right.Effect &&
		left.PolicySource == right.PolicySource && left.AuthoritySource == right.AuthoritySource &&
		left.CreatedBy == right.CreatedBy
}

func (en *Enforcer) addPolicyGrant(grant PolicyGrant) (bool, error) {
	if err := validatePolicyGrant(grant); err != nil {
		return false, err
	}
	row := grantModel(grant)
	result := en.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		var existing safemodel.AuthorizationGrant
		if err := en.db.First(&existing, "grant_id = ?", grant.GrantID).Error; err != nil {
			return false, err
		}
		if !samePolicyGrant(grant, policyGrant(existing)) {
			return false, fmt.Errorf("grant id %q already belongs to another policy", grant.GrantID)
		}
		return false, nil
	}
	has, err := en.e.HasPolicy(grant.AccessorID, grant.Object, grant.Operation, grant.Effect,
		string(grant.PolicySource), string(grant.AuthoritySource))
	if err != nil {
		return false, err
	}
	if !has {
		if _, err := en.e.AddPolicy(grant.AccessorID, grant.Object, grant.Operation, grant.Effect,
			string(grant.PolicySource), string(grant.AuthoritySource)); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (en *Enforcer) addPolicy(sub, object, operation, effect string, source PolicySource, authority AuthoritySource) error {
	_, err := en.addPolicyGrant(deterministicPolicyGrant(sub, object, operation, effect, source, authority))
	return err
}

// GrantPolicy stores an explicit stable grant identity. It is the source-aware
// entry point for management services; identical tuples with distinct GrantID
// values remain independently revocable.
func (en *Enforcer) GrantPolicy(ctx context.Context, grant PolicyGrant) (bool, error) {
	var created bool
	err := en.Transaction(ctx, func(tx *PolicyTransaction) error {
		var err error
		created, err = tx.enforcer.addPolicyGrant(grant)
		return err
	})
	return created, err
}

func (en *Enforcer) revokePolicyGrant(grantID string) (bool, bool, error) {
	var target safemodel.AuthorizationGrant
	err := en.db.First(&target, "grant_id = ?", grantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	// Lock every sibling in a stable order before deleting one. The existence
	// decision and Casbin projection removal therefore share this transaction.
	var siblings []safemodel.AuthorizationGrant
	if err := en.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("projection_key = ? AND accessor_id = ? AND object = ? AND operation = ? AND effect = ? AND policy_source = ? AND authority_source = ?",
			target.ProjectionKey, target.AccessorID, target.Object, target.Operation, target.Effect,
			target.PolicySource, target.AuthoritySource).
		Order("grant_id").Find(&siblings).Error; err != nil {
		return false, false, err
	}
	found := false
	for _, sibling := range siblings {
		if sibling.GrantID == grantID {
			found = true
			break
		}
	}
	if !found {
		return false, false, nil
	}
	if err := en.db.Delete(&safemodel.AuthorizationGrant{}, "grant_id = ?", grantID).Error; err != nil {
		return false, false, err
	}
	if len(siblings) > 1 {
		return true, false, nil
	}
	removed, err := en.e.RemovePolicy(target.AccessorID, target.Object, target.Operation, target.Effect,
		target.PolicySource, target.AuthoritySource)
	return true, removed, err
}

func applyPolicyFilter(q *gorm.DB, filter PolicyFilter) *gorm.DB {
	if filter.GrantID != "" {
		q = q.Where("grant_id = ?", filter.GrantID)
	}
	if filter.AccessorID != "" {
		q = q.Where("accessor_id = ?", filter.AccessorID)
	}
	if filter.Object != "" {
		q = q.Where("object = ?", filter.Object)
	}
	if filter.Operation != "" {
		q = q.Where("operation = ?", filter.Operation)
	}
	if filter.Effect != "" {
		q = q.Where("effect = ?", filter.Effect)
	}
	if filter.PolicySource != "" {
		q = q.Where("policy_source = ?", filter.PolicySource)
	}
	if filter.AuthoritySource != "" {
		q = q.Where("authority_source = ?", filter.AuthoritySource)
	}
	return q
}

func (en *Enforcer) removePolicyGrants(filter PolicyFilter) (int, error) {
	var rows []safemodel.AuthorizationGrant
	if err := applyPolicyFilter(en.db.Model(&safemodel.AuthorizationGrant{}), filter).
		Order("grant_id").Find(&rows).Error; err != nil {
		return 0, err
	}
	removedProjections := 0
	for _, row := range rows {
		_, projectionRemoved, err := en.revokePolicyGrant(row.GrantID)
		if err != nil {
			return removedProjections, err
		}
		if projectionRemoved {
			removedProjections++
		}
	}
	return removedProjections, nil
}

func (en *Enforcer) removePolicyGrantsByObjectPrefix(prefix string) (int, error) {
	var rows []safemodel.AuthorizationGrant
	if err := en.db.Model(&safemodel.AuthorizationGrant{}).
		Where("object LIKE ?", prefix+"%").
		Order("grant_id").Find(&rows).Error; err != nil {
		return 0, err
	}
	removedProjections := 0
	for _, row := range rows {
		_, projectionRemoved, err := en.revokePolicyGrant(row.GrantID)
		if err != nil {
			return removedProjections, err
		}
		if projectionRemoved {
			removedProjections++
		}
	}
	return removedProjections, nil
}

func (en *Enforcer) replacePolicyGrantSlice(filter PolicyFilter, desired []PolicyGrant) error {
	var existing []safemodel.AuthorizationGrant
	if err := applyPolicyFilter(en.db.Model(&safemodel.AuthorizationGrant{}), filter).
		Order("grant_id").Find(&existing).Error; err != nil {
		return err
	}
	wanted := make(map[string]PolicyGrant, len(desired))
	for _, grant := range desired {
		if err := validatePolicyGrant(grant); err != nil {
			return err
		}
		wanted[grant.GrantID] = grant
	}
	for _, row := range existing {
		grant, keep := wanted[row.GrantID]
		if keep && samePolicyGrant(policyGrant(row), grant) {
			delete(wanted, row.GrantID)
			continue
		}
		if _, _, err := en.revokePolicyGrant(row.GrantID); err != nil {
			return err
		}
	}
	for _, grant := range desired {
		if _, pending := wanted[grant.GrantID]; !pending {
			continue
		}
		if _, err := en.addPolicyGrant(grant); err != nil {
			return err
		}
		delete(wanted, grant.GrantID)
	}
	return nil
}

func (en *Enforcer) removePolicy(sub, object, operation, effect string, source PolicySource, authority AuthoritySource) error {
	_, err := en.removePolicyGrants(PolicyFilter{
		AccessorID: sub, Object: object, Operation: operation, Effect: effect,
		PolicySource: source, AuthoritySource: authority,
	})
	return err
}

func (en *Enforcer) addDefaultGrant(ctx context.Context, sub, object, operation, effect string, source PolicySource, authority AuthoritySource) error {
	return en.Transaction(ctx, func(tx *PolicyTransaction) error {
		return tx.enforcer.addPolicy(sub, object, operation, effect, source, authority)
	})
}

func (en *Enforcer) removeDefaultGrant(ctx context.Context, sub, object, operation, effect string, source PolicySource, authority AuthoritySource) error {
	return en.Transaction(ctx, func(tx *PolicyTransaction) error {
		return tx.enforcer.removePolicy(sub, object, operation, effect, source, authority)
	})
}

func (en *Enforcer) validateGrantProjection() error {
	var policyRows []casbinPolicyRow
	if err := en.db.Table("casbin_rule").Where("ptype = ?", "p").Find(&policyRows).Error; err != nil {
		return err
	}
	var grantRows []safemodel.AuthorizationGrant
	if err := en.db.Find(&grantRows).Error; err != nil {
		return err
	}
	projectionCounts := make(map[policyTupleKey]int, len(policyRows))
	grantCounts := make(map[policyTupleKey]int, len(grantRows))
	key := func(sub, object, operation, effect, source, authority string) policyTupleKey {
		return policyTupleKey{sub, object, operation, effect, source, authority}
	}
	for _, row := range policyRows {
		if err := validatePolicyProvenance(PolicySource(row.V4), AuthoritySource(row.V5)); err != nil {
			return fmt.Errorf("%w: policy id %d: %v", ErrPolicySourceMigrationRequired, row.ID, err)
		}
		projectionCounts[key(row.V0, row.V1, row.V2, row.V3, row.V4, row.V5)]++
	}
	for _, row := range grantRows {
		grant := policyGrant(row)
		if err := validatePolicyGrant(grant); err != nil {
			return fmt.Errorf("%w: grant id %q: %v", ErrPolicySourceMigrationRequired, row.GrantID, err)
		}
		wantProjectionKey := policyProjectionKey(row.AccessorID, row.Object, row.Operation, row.Effect,
			PolicySource(row.PolicySource), AuthoritySource(row.AuthoritySource))
		if row.ProjectionKey != wantProjectionKey {
			return fmt.Errorf("%w: grant id %q has an invalid projection key", ErrPolicySourceMigrationRequired, row.GrantID)
		}
		grantCounts[key(row.AccessorID, row.Object, row.Operation, row.Effect, row.PolicySource, row.AuthoritySource)]++
	}
	for tuple, count := range projectionCounts {
		if count != 1 || grantCounts[tuple] == 0 {
			return fmt.Errorf("%w: casbin projection has %d rows and %d grants",
				ErrPolicySourceMigrationRequired, count, grantCounts[tuple])
		}
	}
	for tuple, count := range grantCounts {
		if count > 0 && projectionCounts[tuple] != 1 {
			return fmt.Errorf("%w: grant tuple has %d grants and %d casbin projections",
				ErrPolicySourceMigrationRequired, count, projectionCounts[tuple])
		}
	}
	return nil
}

// GrantProfessionalObjectPermission writes a trusted Professional rule. The
// caller supplies only which already-authenticated server authority performed
// the write; ordinary HTTP request bodies do not expose this parameter.
func (en *Enforcer) GrantProfessionalObjectPermission(accessorID, resourceType, resourceID, operation, effect string, authority AuthoritySource) error {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceOwnerDelegate {
		return fmt.Errorf("professional rule authority %q is not permitted", authority)
	}
	return en.addDefaultGrant(context.Background(), accessorID, obj(resourceType, resourceID), operation, effect,
		PolicySourceProfessionalRule, authority)
}

// GrantSystemObjectPermission records a lifecycle-derived permission.
func (en *Enforcer) GrantSystemObjectPermission(accessorID, resourceType, resourceID, operation string) error {
	return en.addDefaultGrant(context.Background(), accessorID, obj(resourceType, resourceID), operation, EffectAllow,
		PolicySourceSystemDerived, AuthoritySourceSystem)
}

// GrantCommunityBundle records one logical Community grant on a reviewed
// top-level resource. It never materializes the whitelist into operation rows.
func (en *Enforcer) GrantCommunityBundle(accessorID, resourceType, resourceID string, authority AuthoritySource) error {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceSystem {
		return fmt.Errorf("community bundle authority %q is not permitted", authority)
	}
	if err := validateCommunityBundleTarget(resourceType, resourceID); err != nil {
		return err
	}
	return en.addDefaultGrant(context.Background(), accessorID, obj(resourceType, resourceID), ActFullBusinessAccess,
		EffectAllow, PolicySourceCommunityBundle, authority)
}

// RemoveCommunityBundle removes only the logical bundle owned by one trusted
// authority. Legacy, system-derived and Professional rows on the same resource
// remain untouched.
func (en *Enforcer) RemoveCommunityBundle(accessorID, resourceType, resourceID string, authority AuthoritySource) (bool, error) {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceSystem {
		return false, fmt.Errorf("community bundle authority %q is not permitted", authority)
	}
	if err := validateCommunityBundleTarget(resourceType, resourceID); err != nil {
		return false, err
	}
	removed := 0
	err := en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var err error
		removed, err = tx.enforcer.removePolicyGrants(PolicyFilter{
			AccessorID: accessorID, Object: obj(resourceType, resourceID), Operation: ActFullBusinessAccess,
			Effect: EffectAllow, PolicySource: PolicySourceCommunityBundle, AuthoritySource: authority,
		})
		return err
	})
	return removed > 0, err
}

// SetProfessionalObjectPermissions replaces only one trusted source slice. An
// owner save cannot delete an administrator rule, and neither can touch a
// bundle, legacy, system-derived or role row.
func (en *Enforcer) SetProfessionalObjectPermissions(accessorID, resourceType, resourceID string, operations []string, effect string, authority AuthoritySource) error {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceOwnerDelegate {
		return fmt.Errorf("professional rule authority %q is not permitted", authority)
	}
	if effect != EffectAllow && effect != EffectDeny {
		return fmt.Errorf("invalid policy effect %q", effect)
	}
	return en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		filter := PolicyFilter{
			AccessorID: accessorID, Object: obj(resourceType, resourceID), Effect: effect,
			PolicySource: PolicySourceProfessionalRule, AuthoritySource: authority,
		}
		desired := make([]PolicyGrant, 0, len(operations))
		for _, operation := range operations {
			desired = append(desired, deterministicPolicyGrant(accessorID, obj(resourceType, resourceID), operation,
				effect, PolicySourceProfessionalRule, authority))
		}
		return tx.enforcer.replacePolicyGrantSlice(filter, desired)
	})
}

// RemoveProfessionalObjectPermissions removes only the slice managed by one
// trusted authority. In particular, an owner revoke cannot erase an
// administrator's deny or allow for the same tuple.
func (en *Enforcer) RemoveProfessionalObjectPermissions(accessorID, resourceType, resourceID, effect string, authority AuthoritySource) (int, error) {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceOwnerDelegate {
		return 0, fmt.Errorf("professional rule authority %q is not permitted", authority)
	}
	if effect != EffectAllow && effect != EffectDeny {
		return 0, fmt.Errorf("invalid policy effect %q", effect)
	}
	removed := 0
	err := en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var err error
		removed, err = tx.enforcer.removePolicyGrants(PolicyFilter{
			AccessorID: accessorID, Object: obj(resourceType, resourceID), Effect: effect,
			PolicySource: PolicySourceProfessionalRule, AuthoritySource: authority,
		})
		return err
	})
	return removed, err
}

// PolicyRecords lists persisted policy configuration, including inactive
// Professional rows and legacy rows. It never rewrites configuration.
func (en *Enforcer) PolicyRecords(filter PolicyFilter) ([]PolicyRecord, error) {
	var rows []safemodel.AuthorizationGrant
	if err := applyPolicyFilter(en.db.Model(&safemodel.AuthorizationGrant{}), filter).
		Order("created_at, grant_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	edition := entitlement.Current()
	out := make([]PolicyRecord, 0, len(rows))
	for _, row := range rows {
		source := PolicySource(row.PolicySource)
		out = append(out, PolicyRecord{
			GrantID: row.GrantID, AccessorID: row.AccessorID, Object: row.Object, Operation: row.Operation,
			Effect: row.Effect, PolicySource: source, AuthoritySource: AuthoritySource(row.AuthoritySource),
			CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			Active: policySourceActive(source, edition),
		})
	}
	return out, nil
}

// RevokePolicy removes exactly one stable policy identity. It is the only
// mutation supported for legacy rows and cannot remove a sibling source with
// the same subject/resource/operation tuple.
func (en *Enforcer) RevokePolicy(grantID string) (bool, error) {
	var removed bool
	err := en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var err error
		removed, _, err = tx.enforcer.revokePolicyGrant(grantID)
		return err
	})
	return removed, err
}
