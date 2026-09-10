// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"
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
	// persisted business policy has not been assigned trusted provenance. The
	// offline migration owns that classification; startup must not infer it.
	ErrPolicySourceMigrationRequired = errors.New("casbin policy provenance migration required")
)

// PolicyRecord is the durable identity and provenance of one Casbin p-line.
// ID is the adapter's stable primary key. Active is derived from the live
// edition at read time and is never persisted.
type PolicyRecord struct {
	ID              uint
	AccessorID      string
	Object          string
	Operation       string
	Effect          string
	PolicySource    PolicySource
	AuthoritySource AuthoritySource
	Active          bool
}

// PolicyFilter narrows a provenance listing. Zero fields match every p-line.
type PolicyFilter struct {
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

func (en *Enforcer) addPolicy(sub, object, operation, effect string, source PolicySource, authority AuthoritySource) error {
	if effect != EffectAllow && effect != EffectDeny {
		return fmt.Errorf("invalid policy effect %q", effect)
	}
	if err := validatePolicyProvenance(source, authority); err != nil {
		return err
	}
	_, err := en.e.AddPolicy(sub, object, operation, effect, string(source), string(authority))
	return err
}

func (en *Enforcer) removePolicy(sub, object, operation, effect string, source PolicySource, authority AuthoritySource) error {
	if err := validatePolicyProvenance(source, authority); err != nil {
		return err
	}
	_, err := en.e.RemovePolicy(sub, object, operation, effect, string(source), string(authority))
	return err
}

// GrantProfessionalObjectPermission writes a trusted Professional rule. The
// caller supplies only which already-authenticated server authority performed
// the write; ordinary HTTP request bodies do not expose this parameter.
func (en *Enforcer) GrantProfessionalObjectPermission(accessorID, resourceType, resourceID, operation, effect string, authority AuthoritySource) error {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceOwnerDelegate {
		return fmt.Errorf("professional rule authority %q is not permitted", authority)
	}
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	return en.addPolicy(accessorID, obj(resourceType, resourceID), operation, effect, PolicySourceProfessionalRule, authority)
}

// GrantSystemObjectPermission records a lifecycle-derived permission.
func (en *Enforcer) GrantSystemObjectPermission(accessorID, resourceType, resourceID, operation string) error {
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	return en.addPolicy(accessorID, obj(resourceType, resourceID), operation, EffectAllow, PolicySourceSystemDerived, AuthoritySourceSystem)
}

// GrantCommunityBundle records one logical Community grant on a reviewed
// top-level resource. It never materializes the whitelist into operation rows.
func (en *Enforcer) GrantCommunityBundle(accessorID, resourceType, resourceID string, authority AuthoritySource) error {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceOwnerDelegate {
		return fmt.Errorf("community bundle authority %q is not permitted", authority)
	}
	if err := validateCommunityBundleTarget(resourceType, resourceID); err != nil {
		return err
	}
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	return en.addPolicy(accessorID, obj(resourceType, resourceID), ActFullBusinessAccess, EffectAllow, PolicySourceCommunityBundle, authority)
}

// RemoveCommunityBundle removes only the logical bundle owned by one trusted
// authority. Legacy, system-derived and Professional rows on the same resource
// remain untouched.
func (en *Enforcer) RemoveCommunityBundle(accessorID, resourceType, resourceID string, authority AuthoritySource) (bool, error) {
	if authority != AuthoritySourceAdminAuthz && authority != AuthoritySourceOwnerDelegate {
		return false, fmt.Errorf("community bundle authority %q is not permitted", authority)
	}
	if err := validateCommunityBundleTarget(resourceType, resourceID); err != nil {
		return false, err
	}
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	return en.e.RemovePolicy(accessorID, obj(resourceType, resourceID), ActFullBusinessAccess,
		EffectAllow, string(PolicySourceCommunityBundle), string(authority))
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
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	if _, err := en.e.RemoveFilteredPolicy(0, accessorID, obj(resourceType, resourceID), "", effect,
		string(PolicySourceProfessionalRule), string(authority)); err != nil {
		return err
	}
	for _, operation := range operations {
		if err := en.addPolicy(accessorID, obj(resourceType, resourceID), operation, effect, PolicySourceProfessionalRule, authority); err != nil {
			return err
		}
	}
	return nil
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
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	rows, err := en.e.GetFilteredPolicy(0, accessorID, obj(resourceType, resourceID), "", effect,
		string(PolicySourceProfessionalRule), string(authority))
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	if _, err := en.e.RemoveFilteredPolicy(0, accessorID, obj(resourceType, resourceID), "", effect,
		string(PolicySourceProfessionalRule), string(authority)); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// PolicyRecords lists persisted policy configuration, including inactive
// Professional rows and legacy rows. It never rewrites configuration.
func (en *Enforcer) PolicyRecords(filter PolicyFilter) ([]PolicyRecord, error) {
	q := en.db.Table("casbin_rule").Where("ptype = ?", "p")
	if filter.AccessorID != "" {
		q = q.Where("v0 = ?", filter.AccessorID)
	}
	if filter.Object != "" {
		q = q.Where("v1 = ?", filter.Object)
	}
	if filter.Operation != "" {
		q = q.Where("v2 = ?", filter.Operation)
	}
	if filter.Effect != "" {
		q = q.Where("v3 = ?", filter.Effect)
	}
	if filter.PolicySource != "" {
		q = q.Where("v4 = ?", filter.PolicySource)
	}
	if filter.AuthoritySource != "" {
		q = q.Where("v5 = ?", filter.AuthoritySource)
	}
	var rows []casbinPolicyRow
	if err := q.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	edition := entitlement.Current()
	out := make([]PolicyRecord, 0, len(rows))
	for _, row := range rows {
		source := PolicySource(row.V4)
		out = append(out, PolicyRecord{
			ID: row.ID, AccessorID: row.V0, Object: row.V1, Operation: row.V2,
			Effect: row.V3, PolicySource: source, AuthoritySource: AuthoritySource(row.V5),
			Active: policySourceActive(source, edition),
		})
	}
	return out, nil
}

// RevokePolicy removes exactly one stable policy identity. It is the only
// mutation supported for legacy rows and cannot remove a sibling source with
// the same subject/resource/operation tuple.
func (en *Enforcer) RevokePolicy(id uint) (bool, error) {
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	var row casbinPolicyRow
	err := en.db.Table("casbin_rule").Where("id = ? AND ptype = ?", id, "p").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	removed, err := en.e.RemovePolicy(row.V0, row.V1, row.V2, row.V3, row.V4, row.V5)
	return removed, err
}
