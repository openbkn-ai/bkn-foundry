// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package proxygrant owns the provenance ledger and Casbin materialization for
// permissions held by managed BKN proxy accounts.
package proxygrant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

const (
	SourceTypeKNProxyBinding = model.ProxyGrantSourceTypeKNBinding
	SourceTypeManual         = model.ProxyGrantSourceTypeManual
	SourceTypeAdmin          = model.ProxyGrantSourceTypeAdmin
	StatusActive             = model.ProxyGrantSourceStatusActive
	StatusRevoked            = model.ProxyGrantSourceStatusRevoked
)

var (
	ErrInvalidRequest = errors.New("invalid proxy grant request")
	ErrForbidden      = errors.New("proxy grant is forbidden")
	ErrNotFound       = errors.New("proxy grant source not found")
	ErrProxyInactive  = errors.New("managed proxy is not active")
	ErrSourceRequired = errors.New("proxy grant source is required by another active source")
)

// SourceSpec is one published-model binding's need for one concrete operation.
type SourceSpec struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Operation    string `json:"operation"`
	SourceType   string `json:"source_type"`
	SourceID     string `json:"source_id"`
	KNID         string `json:"kn_id"`
	BindingType  string `json:"binding_type"`
	BindingID    string `json:"binding_id"`
}

type GrantRequest struct {
	ProxyAccountID string     `json:"proxy_account_id"`
	GrantorID      string     `json:"grantor_id"`
	Source         SourceSpec `json:"source"`
}

type RevokeRequest struct {
	GrantorID string `json:"grantor_id"`
}

type SyncRequest struct {
	ProxyAccountID string       `json:"proxy_account_id"`
	GrantorID      string       `json:"grantor_id"`
	Sources        []SourceSpec `json:"sources"`
}

type BatchCheckRequest struct {
	ProxyAccountID string       `json:"proxy_account_id"`
	GrantorID      string       `json:"grantor_id"`
	Sources        []SourceSpec `json:"sources"`
}

type ReconcileRequest struct {
	ProxyAccountID string `json:"proxy_account_id"`
	RequestedBy    string `json:"requested_by"`
}

type CheckResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type BatchCheckResult struct {
	DeniedSources   []SourceSpec     `json:"denied_sources"`
	ResolvedSources []ResolvedSource `json:"resolved_sources"`
}

// ResolvedSource records the effective delegator that made a preflight source
// valid. Callers use it only for a subsequent least-privilege dependency read;
// the downstream service still rechecks the exact operation before returning
// data.
type ResolvedSource struct {
	SourceSpec
	GrantedBy string `json:"granted_by"`
}

type SyncResult struct {
	Added       int                      `json:"added"`
	Transferred int                      `json:"transferred"`
	Revoked     int                      `json:"revoked"`
	Unchanged   int                      `json:"unchanged"`
	Sources     []model.ProxyGrantSource `json:"sources"`
}

type ReconcileResult struct {
	PoliciesRestored  int `json:"policies_restored"`
	PoliciesRemoved   int `json:"policies_removed"`
	MarkersCreated    int `json:"markers_created"`
	MarkersRemoved    int `json:"markers_removed"`
	UntrackedPolicies int `json:"untracked_policies"`
	InvalidSources    int `json:"invalid_sources"`
}

type Service struct {
	db       *gorm.DB
	enforcer *authz.Enforcer
}

func New(db *gorm.DB, enforcer *authz.Enforcer) *Service {
	return &Service{db: db, enforcer: enforcer}
}

// Grant adds or reactivates one source. Replaying a currently valid source tuple
// is a successful no-op; an invalid KN-binding delegator may be replaced by the
// current actor. The source, materialization marker, audit row and Casbin policy
// share the adapter's database transaction.
func (s *Service) Grant(ctx context.Context, req GrantRequest) (*model.ProxyGrantSource, bool, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	spec, err := normalizeSpec(req.Source)
	if err != nil || req.ProxyAccountID == "" || req.GrantorID == "" ||
		len(req.ProxyAccountID) > 64 || len(req.GrantorID) > 64 {
		return nil, false, ErrInvalidRequest
	}
	req.Source = spec
	normalized, _, err := s.normalizeRequiredSources(ctx, []SourceSpec{spec})
	if err != nil {
		return nil, false, err
	}
	targetKey := keyForSpec(spec)

	var result model.ProxyGrantSource
	var changed bool
	err = s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		if err := validateProxy(tx.DB(), req.ProxyAccountID, spec.KNID, true); err != nil {
			return err
		}
		for _, candidate := range normalized {
			if err := validatePreflightAuthority(tx, req.ProxyAccountID, req.GrantorID, candidate); err != nil {
				return err
			}
		}
		for _, candidate := range normalized {
			row, created, err := grantInTransaction(tx, req.ProxyAccountID, req.GrantorID,
				candidate, keyForSpec(candidate) != targetKey)
			if err != nil {
				return err
			}
			rowChanged := created
			reason := "created"
			if !created {
				reason = "idempotent replay"
				if candidate.SourceType == SourceTypeKNProxyBinding {
					valid, validErr := sourceCurrentlyValid(tx, row)
					if validErr != nil {
						return validErr
					}
					if !valid {
						if err := tx.DB().Model(&row).Update("granted_by", req.GrantorID).Error; err != nil {
							return err
						}
						row.GrantedBy = req.GrantorID
						rowChanged = true
						reason = "invalid delegator replaced by grant actor"
					}
				}
			}
			if keyForModel(row) == targetKey {
				result = row
				// Preserve the public replay contract: repairing a missing derived
				// prerequisite does not turn an existing target source from 200 into
				// 201 Created.
				changed = rowChanged
			}
			if err := recordAudit(tx.DB(), "grant", "allow", reason,
				req.GrantorID, req.ProxyAccountID, candidate); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, authz.ErrPolicyReloadAfterCommit) {
			s.recordDenied(ctx, "grant", req.GrantorID, req.ProxyAccountID, spec, err)
		}
		return nil, false, err
	}
	return &result, changed, nil
}

// Revoke retires one source row. The corresponding direct policy is removed
// only when this was the final active source and the ledger owns that policy.
func (s *Service) Revoke(ctx context.Context, id string, req RevokeRequest) (*model.ProxyGrantSource, bool, error) {
	id = strings.TrimSpace(id)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	if id == "" || req.GrantorID == "" || len(id) > 64 || len(req.GrantorID) > 64 {
		return nil, false, ErrInvalidRequest
	}
	var result model.ProxyGrantSource
	var changed bool
	err := s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		// Resolve the immutable proxy id first, then lock the proxy mapping before
		// locking the source. Grant, sync, revoke and reconcile all use this order,
		// so separate bkn-safe replicas serialize mutations for the same proxy.
		var identified model.ProxyGrantSource
		if err := tx.DB().Select("proxy_account_id").First(&identified, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if _, err := loadProxy(tx.DB(), identified.ProxyAccountID); err != nil {
			return err
		}
		if err := validateGrantorIdentity(tx.DB(), req.GrantorID); err != nil {
			return err
		}
		row, revoked, required, err := s.revokeWithRequirements(ctx, tx, id)
		if err != nil {
			return err
		}
		result, changed = row, revoked
		spec := specFromModel(row)
		reason := "revoked"
		if !revoked {
			if row.LifecycleStatus == StatusActive {
				reason = "retained because another active source requires it"
			} else {
				reason = "idempotent replay"
			}
		}
		if err := recordAudit(tx.DB(), "revoke", "allow", reason,
			req.GrantorID, row.ProxyAccountID, spec); err != nil {
			return err
		}
		for _, dependency := range required {
			if err := recordAudit(tx.DB(), "revoke_required", "allow", "required by revoked source",
				req.GrantorID, row.ProxyAccountID, specFromModel(dependency)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, authz.ErrPolicyReloadAfterCommit) {
			var source model.ProxyGrantSource
			_ = s.db.WithContext(ctx).First(&source, "id = ?", id).Error
			s.recordDenied(ctx, "revoke", req.GrantorID, source.ProxyAccountID, specFromModel(source), err)
		}
		return nil, false, err
	}
	return &result, changed, nil
}

// Check verifies whether the named actor may create or take over a source
// without changing policy state. A retained KN binding remains valid through
// its recorded delegator, so another KN editor need not hold that downstream
// operation unless a new source or delegator transfer is required. Denials are
// returned as a normal decision payload and persisted in the audit log.
func (s *Service) Check(ctx context.Context, req GrantRequest) (CheckResult, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	spec, err := normalizeSpec(req.Source)
	if err != nil || req.ProxyAccountID == "" || req.GrantorID == "" ||
		len(req.ProxyAccountID) > 64 || len(req.GrantorID) > 64 {
		return CheckResult{}, ErrInvalidRequest
	}
	normalized, _, err := s.normalizeRequiredSources(ctx, []SourceSpec{spec})
	if err != nil {
		return CheckResult{}, err
	}
	result := CheckResult{Allowed: true}
	err = s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		decision := "allow"
		reason := "actor holds the authority required by the source type"
		decisionErr := validateProxy(tx.DB(), req.ProxyAccountID, spec.KNID, true)
		for _, candidate := range normalized {
			if decisionErr != nil {
				break
			}
			decisionErr = validatePreflightAuthority(tx, req.ProxyAccountID, req.GrantorID, candidate)
		}
		if decisionErr != nil {
			if !errors.Is(decisionErr, ErrForbidden) && !errors.Is(decisionErr, ErrProxyInactive) &&
				!errors.Is(decisionErr, ErrNotFound) {
				return decisionErr
			}
			decision = "deny"
			reason = decisionErr.Error()
			result = CheckResult{Allowed: false, Reason: reason}
		}
		if auditErr := recordAudit(tx.DB(), "check", decision, reason, req.GrantorID, req.ProxyAccountID, spec); auditErr != nil {
			return auditErr
		}
		return nil
	})
	if err != nil {
		return CheckResult{}, err
	}
	return result, nil
}

// CheckMany validates a complete KN binding source set in one request. Existing
// source and delegator state is loaded in batches; only sources that need a new
// or replacement delegator are checked against the current actor.
func (s *Service) CheckMany(ctx context.Context, req BatchCheckRequest) (BatchCheckResult, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	if req.ProxyAccountID == "" || req.GrantorID == "" ||
		len(req.ProxyAccountID) > 64 || len(req.GrantorID) > 64 {
		return BatchCheckResult{}, ErrInvalidRequest
	}
	sources := make([]SourceSpec, 0, len(req.Sources))
	desired := make(map[sourceKey]SourceSpec, len(req.Sources))
	for _, raw := range req.Sources {
		spec, err := normalizeSpec(raw)
		if err != nil || spec.SourceType != SourceTypeKNProxyBinding {
			return BatchCheckResult{}, ErrInvalidRequest
		}
		key := keyForSpec(spec)
		if previous, exists := desired[key]; exists {
			if !sameBinding(previous, spec) {
				return BatchCheckResult{}, ErrInvalidRequest
			}
			continue
		}
		desired[key] = spec
		sources = append(sources, spec)
	}
	explicitSources := sources
	sources, requiredBySource, err := s.normalizeRequiredSources(ctx, explicitSources)
	if err != nil {
		return BatchCheckResult{}, err
	}

	result := BatchCheckResult{DeniedSources: []SourceSpec{}, ResolvedSources: []ResolvedSource{}}
	err = s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		mapping, mappingErr := loadProxy(tx.DB(), req.ProxyAccountID)
		if mappingErr != nil && !errors.Is(mappingErr, ErrProxyInactive) &&
			!errors.Is(mappingErr, ErrNotFound) && !errors.Is(mappingErr, ErrForbidden) {
			return mappingErr
		}
		if mappingErr == nil && mapping.LifecycleStatus != managedproxy.StatusActive {
			mappingErr = ErrProxyInactive
		}

		var rows []model.ProxyGrantSource
		if mappingErr == nil {
			if err := tx.DB().Where("proxy_account_id = ? AND source_type = ?",
				req.ProxyAccountID, SourceTypeKNProxyBinding).
				Order("source_id, resource_type, resource_id, operation").Find(&rows).Error; err != nil {
				return err
			}
		}
		current := make(map[sourceKey]model.ProxyGrantSource, len(rows))
		for _, row := range rows {
			current[keyForModel(row)] = row
		}
		validCurrent := map[string]bool{}
		if mappingErr == nil {
			var err error
			validCurrent, err = tx.CurrentProxySourceIDs(req.ProxyAccountID)
			if err != nil {
				return err
			}
		}
		reusableDelegators := make(map[permissionKey]string)
		for _, row := range rows {
			if row.KNID != mapping.ManagedResourceID || row.LifecycleStatus != StatusActive ||
				!validCurrent[row.ID] || strings.TrimSpace(row.GrantedBy) == "" {
				continue
			}
			permission := permissionForModel(row)
			if _, exists := reusableDelegators[permission]; !exists {
				reusableDelegators[permission] = row.GrantedBy
			}
		}

		needsActor := make([]SourceSpec, 0, len(sources))
		allowedBy := make(map[sourceKey]string, len(sources))
		for _, spec := range sources {
			key := keyForSpec(spec)
			if mappingErr != nil || spec.KNID != mapping.ManagedResourceID {
				continue
			}
			if row, exists := current[key]; exists {
				if !sameBinding(specFromModel(row), spec) {
					return ErrInvalidRequest
				}
				if row.LifecycleStatus == StatusActive && validCurrent[row.ID] {
					allowedBy[key] = row.GrantedBy
					continue
				}
			}
			if grantorID, exists := reusableDelegators[permissionForSpec(req.ProxyAccountID, spec)]; exists {
				allowedBy[key] = grantorID
				continue
			}
			needsActor = append(needsActor, spec)
		}

		actorEligible := len(needsActor) > 0
		if actorEligible {
			if err := validateGrantorIdentity(tx.DB(), req.GrantorID); err != nil {
				if !errors.Is(err, ErrForbidden) {
					return err
				}
				actorEligible = false
			}
		}
		if actorEligible {
			if err := validateRegisteredOperations(tx.DB(), needsActor); err != nil {
				return err
			}
			resourceSet := map[authz.ResourceRef]bool{}
			operationSet := map[string]bool{}
			for _, spec := range needsActor {
				resourceSet[authz.ResourceRef{Type: spec.ResourceType, ID: spec.ResourceID}] = true
				operationSet[spec.Operation] = true
			}
			resources := make([]authz.ResourceRef, 0, len(resourceSet))
			for resource := range resourceSet {
				resources = append(resources, resource)
			}
			operations := make([]string, 0, len(operationSet))
			for operation := range operationSet {
				operations = append(operations, operation)
			}
			filtered, err := tx.FilterResourceOpsRaw(req.GrantorID, resources, operations)
			if err != nil {
				return err
			}
			actorPermissions := map[permissionKey]bool{}
			for _, resource := range filtered {
				for _, operation := range resource.Operations {
					actorPermissions[permissionKey{
						ResourceType: resource.Type,
						ResourceID:   resource.ID,
						Operation:    operation,
					}] = true
				}
			}
			for _, spec := range needsActor {
				if actorPermissions[permissionKey{
					ResourceType: spec.ResourceType,
					ResourceID:   spec.ResourceID,
					Operation:    spec.Operation,
				}] {
					allowedBy[keyForSpec(spec)] = req.GrantorID
				}
			}
		}

		audits := make([]model.ProxyGrantAuditLog, 0, len(explicitSources))
		for _, spec := range explicitSources {
			decision := "allow"
			reason := "actor or retained delegator holds the required operation"
			key := keyForSpec(spec)
			_, isAllowed := allowedBy[key]
			for _, required := range requiredBySource[key] {
				_, requirementAllowed := allowedBy[required]
				isAllowed = isAllowed && requirementAllowed
			}
			if !isAllowed {
				decision = "deny"
				reason = ErrForbidden.Error()
				result.DeniedSources = append(result.DeniedSources, spec)
			}
			audit, err := newAudit("check", decision, reason, req.GrantorID, req.ProxyAccountID, spec)
			if err != nil {
				return err
			}
			audits = append(audits, audit)
		}
		for _, source := range sources {
			if grantorID, ok := allowedBy[keyForSpec(source)]; ok {
				result.ResolvedSources = append(result.ResolvedSources, ResolvedSource{
					SourceSpec: source,
					GrantedBy:  grantorID,
				})
			}
		}
		if len(audits) > 0 {
			return tx.DB().Create(&audits).Error
		}
		return nil
	})
	if err != nil {
		return BatchCheckResult{}, err
	}
	return result, nil
}

// Sync replaces the active KN binding source set for one proxy with the latest
// published-model set. All additions and required delegator transfers are
// authorized before any row changes, so one unauthorized target rejects the
// complete synchronization.
func (s *Service) Sync(ctx context.Context, req SyncRequest) (SyncResult, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	if req.ProxyAccountID == "" || req.GrantorID == "" ||
		len(req.ProxyAccountID) > 64 || len(req.GrantorID) > 64 {
		return SyncResult{}, ErrInvalidRequest
	}
	explicit := make([]SourceSpec, 0, len(req.Sources))
	desired := make(map[sourceKey]SourceSpec, len(req.Sources))
	for _, raw := range req.Sources {
		spec, err := normalizeSpec(raw)
		if err != nil {
			return SyncResult{}, err
		}
		if spec.SourceType != SourceTypeKNProxyBinding {
			return SyncResult{}, ErrInvalidRequest
		}
		key := keyForSpec(spec)
		if previous, exists := desired[key]; exists && !sameBinding(previous, spec) {
			return SyncResult{}, ErrInvalidRequest
		}
		if _, exists := desired[key]; !exists {
			explicit = append(explicit, spec)
		}
		desired[key] = spec
	}
	explicitKeys := make(map[sourceKey]bool, len(desired))
	for key := range desired {
		explicitKeys[key] = true
	}
	normalized, _, err := s.normalizeRequiredSources(ctx, explicit)
	if err != nil {
		return SyncResult{}, err
	}
	desired = make(map[sourceKey]SourceSpec, len(normalized))
	for _, spec := range normalized {
		desired[keyForSpec(spec)] = spec
	}

	var result SyncResult
	err = s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		mapping, err := loadProxy(tx.DB(), req.ProxyAccountID)
		if err != nil {
			return err
		}
		if err := validateGrantorIdentity(tx.DB(), req.GrantorID); err != nil {
			return err
		}
		for _, spec := range desired {
			if spec.KNID != mapping.ManagedResourceID {
				return ErrForbidden
			}
		}
		if len(desired) > 0 && mapping.LifecycleStatus != managedproxy.StatusActive {
			return ErrProxyInactive
		}

		var rows []model.ProxyGrantSource
		if err := tx.DB().Where("proxy_account_id = ? AND source_type = ?", req.ProxyAccountID, SourceTypeKNProxyBinding).
			Order("source_id, resource_type, resource_id, operation").Find(&rows).Error; err != nil {
			return err
		}
		current := make(map[sourceKey]model.ProxyGrantSource, len(rows))
		for _, row := range rows {
			current[keyForModel(row)] = row
		}

		validCurrent, err := tx.CurrentProxySourceIDs(req.ProxyAccountID)
		if err != nil {
			return err
		}
		reusableDelegators := make(map[permissionKey]string)
		for _, row := range rows {
			if row.KNID != mapping.ManagedResourceID || row.LifecycleStatus != StatusActive ||
				!validCurrent[row.ID] || strings.TrimSpace(row.GrantedBy) == "" {
				continue
			}
			permission := permissionForModel(row)
			if _, exists := reusableDelegators[permission]; !exists {
				reusableDelegators[permission] = row.GrantedBy
			}
		}

		// Preflight every addition and every invalid historical delegator before
		// applying any mutation. An exact retained source wins, followed by an
		// active source for the same concrete permission in this managed network;
		// only a genuinely new permission requires authority from the sync actor.
		resolvedDelegators := make(map[sourceKey]string, len(desired))
		for key, spec := range desired {
			if row, ok := current[key]; ok && row.LifecycleStatus == StatusActive {
				if !sameBinding(specFromModel(row), spec) {
					return ErrInvalidRequest
				}
				if validCurrent[row.ID] {
					resolvedDelegators[key] = row.GrantedBy
					continue
				}
			}
			if grantorID, exists := reusableDelegators[permissionForSpec(req.ProxyAccountID, spec)]; exists {
				resolvedDelegators[key] = grantorID
				continue
			}
			if err := validateDelegatorOperation(tx, req.GrantorID, spec); err != nil {
				return err
			}
			resolvedDelegators[key] = req.GrantorID
		}

		for key, spec := range desired {
			if row, ok := current[key]; ok && row.LifecycleStatus == StatusActive {
				requirementDerived := !explicitKeys[key]
				if !validCurrent[row.ID] {
					grantorID := resolvedDelegators[key]
					if err := tx.DB().Model(&row).Updates(map[string]any{
						"granted_by": grantorID, "requirement_derived": requirementDerived,
					}).Error; err != nil {
						return err
					}
					row.GrantedBy = grantorID
					row.RequirementDerived = requirementDerived
					result.Transferred++
					if err := recordAudit(tx.DB(), "sync_transfer", "allow", "invalid delegator replaced by an effective delegator",
						req.GrantorID, req.ProxyAccountID, spec); err != nil {
						return err
					}
				} else {
					if row.RequirementDerived != requirementDerived {
						if err := tx.DB().Model(&row).Update("requirement_derived", requirementDerived).Error; err != nil {
							return err
						}
						row.RequirementDerived = requirementDerived
					}
					result.Unchanged++
				}
				if err := ensureMaterialized(tx, req.ProxyAccountID, spec); err != nil {
					return err
				}
				continue
			}
			row, changed, err := grantInTransaction(tx, req.ProxyAccountID, resolvedDelegators[key],
				spec, !explicitKeys[key])
			if err != nil {
				return err
			}
			requirementDerived := !explicitKeys[key]
			if row.RequirementDerived != requirementDerived {
				if err := tx.DB().Model(&row).Update("requirement_derived", requirementDerived).Error; err != nil {
					return err
				}
				row.RequirementDerived = requirementDerived
			}
			if changed {
				result.Added++
			}
			if err := recordAudit(tx.DB(), "sync_grant", "allow", "synchronized", req.GrantorID, req.ProxyAccountID, specFromModel(row)); err != nil {
				return err
			}
		}
		for key, row := range current {
			if row.LifecycleStatus != StatusActive {
				continue
			}
			if _, keep := desired[key]; keep {
				continue
			}
			revoked, changed, err := revokeByID(tx, row.ID)
			if err != nil {
				return err
			}
			if changed {
				result.Revoked++
			}
			if err := recordAudit(tx.DB(), "sync_revoke", "allow", "absent from full source set", req.GrantorID, req.ProxyAccountID, specFromModel(revoked)); err != nil {
				return err
			}
		}
		return tx.DB().Where("proxy_account_id = ? AND source_type = ? AND lifecycle_status = ?",
			req.ProxyAccountID, SourceTypeKNProxyBinding, StatusActive).
			Order("source_id, resource_type, resource_id, operation").Find(&result.Sources).Error
	})
	if err != nil {
		if !errors.Is(err, authz.ErrPolicyReloadAfterCommit) {
			if len(desired) == 0 {
				s.recordDenied(ctx, "sync", req.GrantorID, req.ProxyAccountID, SourceSpec{}, err)
			}
			for _, spec := range desired {
				s.recordDenied(ctx, "sync", req.GrantorID, req.ProxyAccountID, spec, err)
			}
		}
		return SyncResult{}, err
	}
	return result, nil
}

// Reconcile repairs missing policy rows from active source records and removes
// stale policy rows that are still explicitly owned by a materialization marker.
// Untracked rows are reported but preserved because they may be legacy/manual.
func (s *Service) Reconcile(ctx context.Context, req ReconcileRequest) (ReconcileResult, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.RequestedBy = strings.TrimSpace(req.RequestedBy)
	if req.RequestedBy == "" || len(req.ProxyAccountID) > 64 || len(req.RequestedBy) > 64 {
		return ReconcileResult{}, ErrInvalidRequest
	}
	var result ReconcileResult
	err := s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		proxyIDs, err := reconcileProxyIDs(tx.DB(), req.ProxyAccountID)
		if err != nil {
			return err
		}
		for _, proxyID := range proxyIDs {
			if _, err := loadProxy(tx.DB(), proxyID); err != nil {
				return err
			}
			if err := reconcileProxy(tx, proxyID, req.RequestedBy, &result); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

type sourceKey struct {
	ResourceType string
	ResourceID   string
	Operation    string
	SourceType   string
	SourceID     string
}

type permissionKey struct {
	ProxyAccountID string
	ResourceType   string
	ResourceID     string
	Operation      string
}

// normalizeRequiredSources expands each explicitly requested source with one
// source for every direct operation prerequisite. Keeping the same source
// identity and binding makes provenance validation, Sync replacement and
// Reconcile operate on the normalized permissions exactly like explicit ones.
// The returned reverse index lets CheckMany report a denied explicit source
// when any source that would be added for its prerequisites is unavailable.
func (s *Service) normalizeRequiredSources(ctx context.Context,
	explicit []SourceSpec) ([]SourceSpec, map[sourceKey][]sourceKey, error) {
	operationsByType := make(map[string][]string)
	for _, spec := range explicit {
		operationsByType[spec.ResourceType] = append(operationsByType[spec.ResourceType], spec.Operation)
	}
	requirementsByType := make(map[string]map[string][]string, len(operationsByType))
	for resourceType, operations := range operationsByType {
		requirements, err := s.enforcer.DirectRequirements(ctx, resourceType, operations)
		if err != nil {
			return nil, nil, err
		}
		requirementsByType[resourceType] = requirements
	}

	normalized := make([]SourceSpec, 0, len(explicit))
	byKey := make(map[sourceKey]SourceSpec, len(explicit))
	appendUnique := func(spec SourceSpec) error {
		key := keyForSpec(spec)
		if previous, exists := byKey[key]; exists {
			if !sameBinding(previous, spec) {
				return ErrInvalidRequest
			}
			return nil
		}
		byKey[key] = spec
		normalized = append(normalized, spec)
		return nil
	}
	for _, spec := range explicit {
		if err := appendUnique(spec); err != nil {
			return nil, nil, err
		}
	}

	requiredBySource := make(map[sourceKey][]sourceKey, len(explicit))
	for _, spec := range explicit {
		target := keyForSpec(spec)
		for _, operation := range requirementsByType[spec.ResourceType][spec.Operation] {
			required := spec
			required.Operation = operation
			if err := appendUnique(required); err != nil {
				return nil, nil, err
			}
			requiredBySource[target] = append(requiredBySource[target], keyForSpec(required))
		}
	}
	return normalized, requiredBySource, nil
}

func normalizeSpec(spec SourceSpec) (SourceSpec, error) {
	spec.ResourceType = strings.TrimSpace(spec.ResourceType)
	spec.ResourceID = strings.TrimSpace(spec.ResourceID)
	spec.Operation = strings.TrimSpace(spec.Operation)
	spec.SourceType = strings.TrimSpace(spec.SourceType)
	spec.SourceID = strings.TrimSpace(spec.SourceID)
	spec.KNID = strings.TrimSpace(spec.KNID)
	spec.BindingType = strings.TrimSpace(spec.BindingType)
	spec.BindingID = strings.TrimSpace(spec.BindingID)
	if spec.SourceType == "" {
		spec.SourceType = SourceTypeKNProxyBinding
	}
	validSourceType := spec.SourceType == SourceTypeKNProxyBinding || spec.SourceType == SourceTypeManual || spec.SourceType == SourceTypeAdmin
	if !validSourceType || spec.SourceID == "" || spec.KNID == "" ||
		spec.BindingType == "" || spec.BindingID == "" || spec.ResourceID == "" ||
		strings.Contains(spec.ResourceType, ":") || strings.Contains(spec.ResourceType, "*") ||
		strings.Contains(spec.ResourceID, "*") || strings.Contains(spec.Operation, "*") ||
		len(spec.ResourceType) > 64 || len(spec.ResourceID) > 128 || len(spec.Operation) > 64 ||
		len(spec.SourceID) > 128 || len(spec.KNID) > 128 || len(spec.BindingType) > 64 || len(spec.BindingID) > 128 {
		return SourceSpec{}, ErrInvalidRequest
	}
	allowed := map[string]map[string]bool{
		"resource": {"view_detail": true, "query_data": true},
		"tool_box": {"execute": true},
		"mcp":      {"execute": true},
	}
	if !allowed[spec.ResourceType][spec.Operation] {
		return SourceSpec{}, ErrInvalidRequest
	}
	return spec, nil
}

func validateProxy(db *gorm.DB, proxyID, knID string, requireActive bool) error {
	mapping, err := loadProxy(db, proxyID)
	if err != nil {
		return err
	}
	if mapping.ManagedResourceID != knID {
		return ErrForbidden
	}
	if requireActive && mapping.LifecycleStatus != managedproxy.StatusActive {
		return ErrProxyInactive
	}
	return nil
}

func loadProxy(db *gorm.DB, proxyID string) (model.ManagedProxyAccount, error) {
	var mapping model.ManagedProxyAccount
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&mapping, "proxy_account_id = ?", proxyID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return mapping, ErrNotFound
		}
		return mapping, err
	}
	var user model.User
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", proxyID).Error; err != nil {
		return mapping, err
	}
	if mapping.ManagedBy != managedproxy.ManagerBKN || mapping.ManagedResourceType != managedproxy.ResourceKnowledgeNetwork ||
		user.AccountType != model.AccountTypeApp || user.PasswordHash != "" {
		return mapping, managedproxy.ErrInconsistentAccount
	}
	active := mapping.LifecycleStatus == managedproxy.StatusActive
	validStatus := active || mapping.LifecycleStatus == managedproxy.StatusDisabling ||
		mapping.LifecycleStatus == managedproxy.StatusArchived
	if !validStatus || user.Enabled != active {
		return mapping, managedproxy.ErrInconsistentAccount
	}
	return mapping, nil
}

func validateSourceAuthority(tx *authz.PolicyTransaction, actorID string, spec SourceSpec) error {
	if spec.SourceType == SourceTypeKNProxyBinding {
		return validateDelegatorOperation(tx, actorID, spec)
	}
	return validateAdministrativeAuthority(tx, actorID, spec)
}

func validatePreflightAuthority(tx *authz.PolicyTransaction, proxyID, actorID string, spec SourceSpec) error {
	if spec.SourceType != SourceTypeKNProxyBinding {
		return validateSourceAuthority(tx, actorID, spec)
	}
	var row model.ProxyGrantSource
	err := tx.DB().Where(
		"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ? AND source_type = ? AND source_id = ?",
		proxyID, spec.ResourceType, spec.ResourceID, spec.Operation, spec.SourceType, spec.SourceID,
	).First(&row).Error
	if err == nil {
		if !sameBinding(specFromModel(row), spec) {
			return ErrInvalidRequest
		}
		if row.LifecycleStatus == StatusActive {
			valid, validErr := sourceCurrentlyValid(tx, row)
			if validErr != nil {
				return validErr
			}
			if valid {
				return nil
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return validateDelegatorOperation(tx, actorID, spec)
}

func validateDelegatorOperation(tx *authz.PolicyTransaction, delegatorID string, spec SourceSpec) error {
	if err := validateGrantorIdentity(tx.DB(), delegatorID); err != nil {
		return err
	}
	if err := validateRegisteredOperation(tx.DB(), spec); err != nil {
		return err
	}
	operation, err := tx.Check(delegatorID, spec.ResourceType, spec.ResourceID, spec.Operation)
	if err != nil {
		return err
	}
	if !operation {
		return ErrForbidden
	}
	return nil
}

func validateAdministrativeAuthority(tx *authz.PolicyTransaction, grantorID string, spec SourceSpec) error {
	if err := validateGrantorIdentity(tx.DB(), grantorID); err != nil {
		return err
	}
	if err := validateRegisteredOperation(tx.DB(), spec); err != nil {
		return err
	}
	authorize, err := tx.Check(grantorID, spec.ResourceType, spec.ResourceID, "authorize")
	if err != nil {
		return err
	}
	operation, err := tx.Check(grantorID, spec.ResourceType, spec.ResourceID, spec.Operation)
	if err != nil {
		return err
	}
	if !authorize || !operation {
		return ErrForbidden
	}
	return nil
}

func validateRegisteredOperation(db *gorm.DB, spec SourceSpec) error {
	var registered int64
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", spec.ResourceType, spec.Operation).Count(&registered).Error; err != nil {
		return err
	}
	if registered == 0 {
		return ErrInvalidRequest
	}
	return nil
}

func validateRegisteredOperations(db *gorm.DB, specs []SourceSpec) error {
	resourceTypes := map[string]bool{}
	operationIDs := map[string]bool{}
	for _, spec := range specs {
		resourceTypes[spec.ResourceType] = true
		operationIDs[spec.Operation] = true
	}
	resourceTypeValues := make([]string, 0, len(resourceTypes))
	for value := range resourceTypes {
		resourceTypeValues = append(resourceTypeValues, value)
	}
	operationValues := make([]string, 0, len(operationIDs))
	for value := range operationIDs {
		operationValues = append(operationValues, value)
	}
	var registered []model.Operation
	if err := db.Where("resource_type_id IN ? AND id IN ?", resourceTypeValues, operationValues).
		Find(&registered).Error; err != nil {
		return err
	}
	available := map[string]bool{}
	for _, operation := range registered {
		available[operation.ResourceTypeID+"\x00"+operation.ID] = true
	}
	for _, spec := range specs {
		if !available[spec.ResourceType+"\x00"+spec.Operation] {
			return ErrInvalidRequest
		}
	}
	return nil
}

func sourceCurrentlyValid(tx *authz.PolicyTransaction, source model.ProxyGrantSource) (bool, error) {
	switch source.SourceType {
	case SourceTypeManual, SourceTypeAdmin:
		return source.LifecycleStatus == StatusActive, nil
	case SourceTypeKNProxyBinding:
		if source.LifecycleStatus != StatusActive || strings.TrimSpace(source.GrantedBy) == "" {
			return false, nil
		}
		err := validateDelegatorOperation(tx, source.GrantedBy, specFromModel(source))
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalidRequest) {
			return false, nil
		}
		return err == nil, err
	default:
		return false, nil
	}
}

func validateGrantorIdentity(db *gorm.DB, grantorID string) error {
	var grantor model.User
	if err := db.First(&grantor, "id = ? AND enabled = ?", grantorID, true).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrForbidden
		}
		return err
	}
	var managed int64
	if err := db.Model(&model.ManagedProxyAccount{}).Where("proxy_account_id = ?", grantorID).Count(&managed).Error; err != nil {
		return err
	}
	if managed > 0 {
		return ErrForbidden
	}
	return nil
}

func grantInTransaction(tx *authz.PolicyTransaction, proxyID, grantorID string, spec SourceSpec,
	requirementDerived bool) (model.ProxyGrantSource, bool, error) {
	var row model.ProxyGrantSource
	err := tx.DB().Where(
		"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ? AND source_type = ? AND source_id = ?",
		proxyID, spec.ResourceType, spec.ResourceID, spec.Operation, spec.SourceType, spec.SourceID,
	).First(&row).Error
	if err == nil && !sameBinding(specFromModel(row), spec) {
		return row, false, ErrInvalidRequest
	}
	changed := false
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		id, err := newID()
		if err != nil {
			return row, false, fmt.Errorf("generate proxy grant source id: %w", err)
		}
		row = model.ProxyGrantSource{
			ID: id, ProxyAccountID: proxyID, ResourceType: spec.ResourceType,
			ResourceID: spec.ResourceID, Operation: spec.Operation, SourceType: spec.SourceType,
			SourceID: spec.SourceID, KNID: spec.KNID, BindingType: spec.BindingType,
			BindingID: spec.BindingID, RequirementDerived: requirementDerived,
			GrantedBy: grantorID, LifecycleStatus: StatusActive,
		}
		if err := tx.DB().Create(&row).Error; err != nil {
			return row, false, err
		}
		changed = true
	case err != nil:
		return row, false, err
	case row.LifecycleStatus == StatusRevoked:
		if err := tx.DB().Model(&row).Updates(map[string]any{
			"kn_id": spec.KNID, "binding_type": spec.BindingType, "binding_id": spec.BindingID,
			"requirement_derived": requirementDerived, "granted_by": grantorID,
			"lifecycle_status": StatusActive, "revoked_at": nil,
		}).Error; err != nil {
			return row, false, err
		}
		row.KNID, row.BindingType, row.BindingID = spec.KNID, spec.BindingType, spec.BindingID
		row.RequirementDerived = requirementDerived
		row.GrantedBy, row.LifecycleStatus, row.RevokedAt = grantorID, StatusActive, nil
		changed = true
	case row.LifecycleStatus != StatusActive:
		return row, false, ErrInvalidRequest
	case row.RequirementDerived && !requirementDerived:
		// An explicit grant promotes a synthesized prerequisite. Never demote an
		// explicit row from the additive Grant endpoint; full Sync performs that
		// transition only when its authoritative source set omits the operation.
		if err := tx.DB().Model(&row).Update("requirement_derived", false).Error; err != nil {
			return row, false, err
		}
		row.RequirementDerived = false
		changed = true
	}
	if err := ensureMaterialized(tx, proxyID, spec); err != nil {
		return row, false, err
	}
	return row, changed, nil
}

func ensureMaterialized(tx *authz.PolicyTransaction, proxyID string, spec SourceSpec) error {
	var marker model.ProxyGrantPolicy
	err := tx.DB().First(&marker,
		"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ?",
		proxyID, spec.ResourceType, spec.ResourceID, spec.Operation).Error
	hasPolicy, policyErr := tx.HasObjectPermission(proxyID, spec.ResourceType, spec.ResourceID, spec.Operation)
	if policyErr != nil {
		return policyErr
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		marker = model.ProxyGrantPolicy{
			ProxyAccountID: proxyID, ResourceType: spec.ResourceType, ResourceID: spec.ResourceID,
			Operation: spec.Operation, PolicyOwned: !hasPolicy,
		}
		if err := tx.DB().Create(&marker).Error; err != nil {
			return err
		}
		if !hasPolicy {
			return tx.GrantObjectPermission(proxyID, spec.ResourceType, spec.ResourceID, spec.Operation)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if hasPolicy {
		return nil
	}
	if err := tx.GrantObjectPermission(proxyID, spec.ResourceType, spec.ResourceID, spec.Operation); err != nil {
		return err
	}
	return tx.DB().Model(&marker).Update("policy_owned", true).Error
}

// revokeWithRequirements retires one source together with the direct
// prerequisite sources that Grant synthesized for the same published-model
// binding. A prerequisite remains active while another operation in that same
// source family still requires it; revokeByID then preserves the shared Casbin
// policy when an independent source family still grants the permission.
func (s *Service) revokeWithRequirements(ctx context.Context, tx *authz.PolicyTransaction,
	id string) (model.ProxyGrantSource, bool, []model.ProxyGrantSource, error) {
	var target model.ProxyGrantSource
	if err := tx.DB().Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return target, false, nil, ErrNotFound
		}
		return target, false, nil, err
	}
	// Replaying deletion of an already-revoked target is a strict no-op. In
	// particular, it must not cascade into prerequisites that were explicitly
	// granted or reactivated after the original target revocation.
	if target.LifecycleStatus == StatusRevoked {
		return target, false, nil, nil
	}

	var activeFamily []model.ProxyGrantSource
	if err := tx.DB().Where(
		"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND source_type = ? AND source_id = ? AND kn_id = ? AND binding_type = ? AND binding_id = ? AND lifecycle_status = ?",
		target.ProxyAccountID, target.ResourceType, target.ResourceID, target.SourceType,
		target.SourceID, target.KNID, target.BindingType, target.BindingID, StatusActive,
	).Find(&activeFamily).Error; err != nil {
		return target, false, nil, err
	}
	operations := make([]string, 0, len(activeFamily)+1)
	operations = append(operations, target.Operation)
	for _, source := range activeFamily {
		operations = append(operations, source.Operation)
	}
	requirements, err := tx.DirectRequirements(ctx, target.ResourceType, operations)
	if err != nil {
		return target, false, nil, err
	}
	// A normalized prerequisite cannot be removed while another active operation
	// in the same source family still depends on it. This mirrors whole-set and
	// role-grant normalization and prevents a caller that sees the derived row in
	// Sync output from breaking the stored invariant by deleting its id directly.
	for _, source := range activeFamily {
		if source.ID == target.ID {
			continue
		}
		for _, required := range requirements[source.Operation] {
			if required == target.Operation {
				return target, false, nil, ErrSourceRequired
			}
		}
	}

	target, changed, err := revokeByID(tx, id)
	if err != nil {
		return target, false, nil, err
	}

	stillRequired := make(map[string]bool)
	activeByOperation := make(map[string]model.ProxyGrantSource, len(activeFamily))
	for _, source := range activeFamily {
		if source.ID == target.ID {
			continue
		}
		activeByOperation[source.Operation] = source
		for _, required := range requirements[source.Operation] {
			stillRequired[required] = true
		}
	}
	var revokedRequirements []model.ProxyGrantSource
	for _, required := range requirements[target.Operation] {
		if stillRequired[required] {
			continue
		}
		source, exists := activeByOperation[required]
		if !exists || !source.RequirementDerived {
			continue
		}
		revoked, dependencyChanged, err := revokeByID(tx, source.ID)
		if err != nil {
			return target, false, nil, err
		}
		if dependencyChanged {
			changed = true
			revokedRequirements = append(revokedRequirements, revoked)
		}
	}
	return target, changed, revokedRequirements, nil
}

func revokeByID(tx *authz.PolicyTransaction, id string) (model.ProxyGrantSource, bool, error) {
	var row model.ProxyGrantSource
	if err := tx.DB().Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return row, false, ErrNotFound
		}
		return row, false, err
	}
	if row.LifecycleStatus == StatusRevoked {
		return row, false, nil
	}
	now := time.Now().UTC()
	if err := tx.DB().Model(&row).Updates(map[string]any{
		"lifecycle_status": StatusRevoked,
		"revoked_at":       &now,
	}).Error; err != nil {
		return row, false, err
	}
	row.LifecycleStatus, row.RevokedAt = StatusRevoked, &now

	var active int64
	if err := tx.DB().Model(&model.ProxyGrantSource{}).Where(
		"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ? AND lifecycle_status = ?",
		row.ProxyAccountID, row.ResourceType, row.ResourceID, row.Operation, StatusActive,
	).Count(&active).Error; err != nil {
		return row, false, err
	}
	if active > 0 {
		return row, true, nil
	}
	var marker model.ProxyGrantPolicy
	err := tx.DB().First(&marker,
		"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ?",
		row.ProxyAccountID, row.ResourceType, row.ResourceID, row.Operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, true, nil
	}
	if err != nil {
		return row, false, err
	}
	if marker.PolicyOwned {
		if err := tx.RevokeObjectPermission(row.ProxyAccountID, row.ResourceType, row.ResourceID, row.Operation); err != nil {
			return row, false, err
		}
	}
	if err := tx.DB().Delete(&marker).Error; err != nil {
		return row, false, err
	}
	return row, true, nil
}

func reconcileProxy(tx *authz.PolicyTransaction, proxyID, requestedBy string, result *ReconcileResult) error {
	var active []model.ProxyGrantSource
	if err := tx.DB().Where("proxy_account_id = ? AND lifecycle_status = ?", proxyID, StatusActive).Find(&active).Error; err != nil {
		return err
	}
	activeByPermission := make(map[permissionKey]model.ProxyGrantSource, len(active))
	for _, source := range active {
		valid, err := sourceCurrentlyValid(tx, source)
		if err != nil {
			return err
		}
		if !valid {
			result.InvalidSources++
			if err := recordAudit(tx.DB(), "reconcile_invalid", "deny", "active source has no current delegation authority",
				requestedBy, proxyID, specFromModel(source)); err != nil {
				return err
			}
			continue
		}
		key := permissionForModel(source)
		if _, exists := activeByPermission[key]; !exists {
			activeByPermission[key] = source
		}
	}
	for key, source := range activeByPermission {
		var marker model.ProxyGrantPolicy
		err := tx.DB().First(&marker,
			"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ?",
			key.ProxyAccountID, key.ResourceType, key.ResourceID, key.Operation).Error
		has, policyErr := tx.HasObjectPermission(key.ProxyAccountID, key.ResourceType, key.ResourceID, key.Operation)
		if policyErr != nil {
			return policyErr
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			marker = model.ProxyGrantPolicy{
				ProxyAccountID: key.ProxyAccountID, ResourceType: key.ResourceType,
				ResourceID: key.ResourceID, Operation: key.Operation, PolicyOwned: !has,
			}
			if err := tx.DB().Create(&marker).Error; err != nil {
				return err
			}
			result.MarkersCreated++
		} else if err != nil {
			return err
		}
		if !has {
			if err := tx.GrantObjectPermission(key.ProxyAccountID, key.ResourceType, key.ResourceID, key.Operation); err != nil {
				return err
			}
			if !marker.PolicyOwned {
				if err := tx.DB().Model(&marker).Update("policy_owned", true).Error; err != nil {
					return err
				}
			}
			result.PoliciesRestored++
			if err := recordAudit(tx.DB(), "reconcile_restore", "allow", "active source had no policy", requestedBy, proxyID, specFromModel(source)); err != nil {
				return err
			}
		}
	}

	var markers []model.ProxyGrantPolicy
	if err := tx.DB().Where("proxy_account_id = ?", proxyID).Find(&markers).Error; err != nil {
		return err
	}
	for _, marker := range markers {
		key := permissionKey{marker.ProxyAccountID, marker.ResourceType, marker.ResourceID, marker.Operation}
		if _, exists := activeByPermission[key]; exists {
			continue
		}
		has, err := tx.HasObjectPermission(marker.ProxyAccountID, marker.ResourceType, marker.ResourceID, marker.Operation)
		if err != nil {
			return err
		}
		if marker.PolicyOwned && has {
			if err := tx.RevokeObjectPermission(marker.ProxyAccountID, marker.ResourceType, marker.ResourceID, marker.Operation); err != nil {
				return err
			}
			result.PoliciesRemoved++
			if err := recordAudit(tx.DB(), "reconcile_remove", "allow", "owned policy had no active source", requestedBy, proxyID, SourceSpec{
				ResourceType: marker.ResourceType, ResourceID: marker.ResourceID, Operation: marker.Operation,
			}); err != nil {
				return err
			}
		}
		if err := tx.DB().Delete(&marker).Error; err != nil {
			return err
		}
		result.MarkersRemoved++
	}

	var rules []gormadapter.CasbinRule
	if err := tx.DB().Where("ptype = ? AND v0 = ?", "p", proxyID).Find(&rules).Error; err != nil {
		return err
	}
	for _, rule := range rules {
		rtype, rid, ok := strings.Cut(rule.V1, ":")
		if !ok {
			continue
		}
		key := permissionKey{proxyID, rtype, rid, rule.V2}
		if _, sourced := activeByPermission[key]; sourced {
			continue
		}
		var markerCount int64
		if err := tx.DB().Model(&model.ProxyGrantPolicy{}).Where(
			"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ?",
			proxyID, rtype, rid, rule.V2).Count(&markerCount).Error; err != nil {
			return err
		}
		if markerCount == 0 {
			result.UntrackedPolicies++
		}
	}
	return nil
}

func reconcileProxyIDs(db *gorm.DB, only string) ([]string, error) {
	if only != "" {
		var count int64
		if err := db.Model(&model.ManagedProxyAccount{}).Where("proxy_account_id = ?", only).Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, ErrNotFound
		}
		return []string{only}, nil
	}
	var ids []string
	err := db.Model(&model.ManagedProxyAccount{}).Order("proxy_account_id").Pluck("proxy_account_id", &ids).Error
	return ids, err
}

func recordAudit(db *gorm.DB, action, decision, reason, grantorID, proxyID string, spec SourceSpec) error {
	audit, err := newAudit(action, decision, reason, grantorID, proxyID, spec)
	if err != nil {
		return err
	}
	return db.Create(&audit).Error
}

func newAudit(action, decision, reason, grantorID, proxyID string,
	spec SourceSpec) (model.ProxyGrantAuditLog, error) {
	if len(reason) > 255 {
		reason = reason[:255]
	}
	id, err := newID()
	if err != nil {
		return model.ProxyGrantAuditLog{}, fmt.Errorf("generate proxy grant audit id: %w", err)
	}
	return model.ProxyGrantAuditLog{
		ID: id, Action: action, Decision: decision, Reason: reason, GrantorID: grantorID,
		ProxyAccountID: proxyID, ResourceType: spec.ResourceType, ResourceID: spec.ResourceID,
		Operation: spec.Operation, SourceType: spec.SourceType, SourceID: spec.SourceID,
	}, nil
}

func (s *Service) recordDenied(ctx context.Context, action, grantorID, proxyID string, spec SourceSpec, decisionErr error) {
	if err := recordAudit(s.db.WithContext(ctx), action, "deny", decisionErr.Error(), grantorID, proxyID, spec); err != nil {
		slog.ErrorContext(ctx, "failed to persist proxy grant denial audit", "action", action, "error", err)
	}
}

func keyForSpec(spec SourceSpec) sourceKey {
	return sourceKey{spec.ResourceType, spec.ResourceID, spec.Operation, spec.SourceType, spec.SourceID}
}

func keyForModel(row model.ProxyGrantSource) sourceKey {
	return sourceKey{row.ResourceType, row.ResourceID, row.Operation, row.SourceType, row.SourceID}
}

func sameBinding(left, right SourceSpec) bool {
	return left.KNID == right.KNID && left.BindingType == right.BindingType && left.BindingID == right.BindingID
}

func permissionForModel(row model.ProxyGrantSource) permissionKey {
	return permissionKey{row.ProxyAccountID, row.ResourceType, row.ResourceID, row.Operation}
}

func permissionForSpec(proxyAccountID string, spec SourceSpec) permissionKey {
	return permissionKey{proxyAccountID, spec.ResourceType, spec.ResourceID, spec.Operation}
}

func specFromModel(row model.ProxyGrantSource) SourceSpec {
	return SourceSpec{
		ResourceType: row.ResourceType, ResourceID: row.ResourceID, Operation: row.Operation,
		SourceType: row.SourceType, SourceID: row.SourceID, KNID: row.KNID,
		BindingType: row.BindingType, BindingID: row.BindingID,
	}
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
