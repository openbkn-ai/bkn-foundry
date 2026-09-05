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
	SourceTypeKNProxyBinding = "kn_proxy_binding"
	SourceTypeManual         = "manual"
	SourceTypeAdmin          = "admin"
	StatusActive             = "active"
	StatusRevoked            = "revoked"
)

var (
	ErrInvalidRequest = errors.New("invalid proxy grant request")
	ErrForbidden      = errors.New("proxy grant is forbidden")
	ErrNotFound       = errors.New("proxy grant source not found")
	ErrProxyInactive  = errors.New("managed proxy is not active")
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

type ReconcileRequest struct {
	ProxyAccountID string `json:"proxy_account_id"`
	RequestedBy    string `json:"requested_by"`
}

type CheckResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type SyncResult struct {
	Added     int                      `json:"added"`
	Revoked   int                      `json:"revoked"`
	Unchanged int                      `json:"unchanged"`
	Sources   []model.ProxyGrantSource `json:"sources"`
}

type ReconcileResult struct {
	PoliciesRestored  int `json:"policies_restored"`
	PoliciesRemoved   int `json:"policies_removed"`
	MarkersCreated    int `json:"markers_created"`
	MarkersRemoved    int `json:"markers_removed"`
	UntrackedPolicies int `json:"untracked_policies"`
}

type Service struct {
	db       *gorm.DB
	enforcer *authz.Enforcer
}

func New(db *gorm.DB, enforcer *authz.Enforcer) *Service {
	return &Service{db: db, enforcer: enforcer}
}

// Grant adds or reactivates one source. Replaying the same source tuple is a
// successful no-op. The source, materialization marker, audit row and Casbin
// policy share the adapter's database transaction.
func (s *Service) Grant(ctx context.Context, req GrantRequest) (*model.ProxyGrantSource, bool, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	spec, err := normalizeSpec(req.Source)
	if err != nil || req.ProxyAccountID == "" || req.GrantorID == "" ||
		len(req.ProxyAccountID) > 64 || len(req.GrantorID) > 64 {
		return nil, false, ErrInvalidRequest
	}
	req.Source = spec

	var result model.ProxyGrantSource
	var changed bool
	err = s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		if err := validateProxy(tx.DB(), req.ProxyAccountID, spec.KNID, true); err != nil {
			return err
		}
		if err := validateAuthority(tx, req.GrantorID, spec); err != nil {
			return err
		}
		row, created, err := grantInTransaction(tx, req.ProxyAccountID, req.GrantorID, spec)
		if err != nil {
			return err
		}
		result, changed = row, created
		reason := "created"
		if !created {
			reason = "idempotent replay"
		}
		return recordAudit(tx.DB(), "grant", "allow", reason, req.GrantorID, req.ProxyAccountID, spec)
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
		row, revoked, err := revokeByID(tx, id)
		if err != nil {
			return err
		}
		result, changed = row, revoked
		spec := specFromModel(row)
		reason := "revoked"
		if !revoked {
			reason = "idempotent replay"
		}
		return recordAudit(tx.DB(), "revoke", "allow", reason, req.GrantorID, row.ProxyAccountID, spec)
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

// Check verifies whether the named grantor may create a source without
// changing policy state. Denials are returned as a normal decision payload and
// are persisted in the proxy-grant audit log.
func (s *Service) Check(ctx context.Context, req GrantRequest) (CheckResult, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	spec, err := normalizeSpec(req.Source)
	if err != nil || req.ProxyAccountID == "" || req.GrantorID == "" ||
		len(req.ProxyAccountID) > 64 || len(req.GrantorID) > 64 {
		return CheckResult{}, ErrInvalidRequest
	}
	result := CheckResult{Allowed: true}
	err = s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		decision := "allow"
		reason := "grantor holds authorize and requested operation"
		decisionErr := validateProxy(tx.DB(), req.ProxyAccountID, spec.KNID, true)
		if decisionErr == nil {
			decisionErr = validateAuthority(tx, req.GrantorID, spec)
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

// Sync replaces the active KN binding source set for one proxy with the latest
// published-model set. All additions are authorized before any row changes, so
// one unauthorized target rejects the complete synchronization.
func (s *Service) Sync(ctx context.Context, req SyncRequest) (SyncResult, error) {
	req.ProxyAccountID = strings.TrimSpace(req.ProxyAccountID)
	req.GrantorID = strings.TrimSpace(req.GrantorID)
	if req.ProxyAccountID == "" || req.GrantorID == "" ||
		len(req.ProxyAccountID) > 64 || len(req.GrantorID) > 64 {
		return SyncResult{}, ErrInvalidRequest
	}
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
		desired[key] = spec
	}

	var result SyncResult
	err := s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
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

		var rows []model.ProxyGrantSource
		if err := tx.DB().Where("proxy_account_id = ? AND source_type = ?", req.ProxyAccountID, SourceTypeKNProxyBinding).
			Find(&rows).Error; err != nil {
			return err
		}
		current := make(map[sourceKey]model.ProxyGrantSource, len(rows))
		for _, row := range rows {
			current[keyForModel(row)] = row
		}

		// Preflight every addition before applying any mutation.
		for key, spec := range desired {
			if row, ok := current[key]; ok && row.LifecycleStatus == StatusActive {
				if !sameBinding(specFromModel(row), spec) {
					return ErrInvalidRequest
				}
				continue
			}
			if mapping.LifecycleStatus != managedproxy.StatusActive {
				return ErrProxyInactive
			}
			if err := validateAuthority(tx, req.GrantorID, spec); err != nil {
				return err
			}
		}

		for key, spec := range desired {
			if row, ok := current[key]; ok && row.LifecycleStatus == StatusActive {
				result.Unchanged++
				continue
			}
			row, changed, err := grantInTransaction(tx, req.ProxyAccountID, req.GrantorID, spec)
			if err != nil {
				return err
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

func validateAuthority(tx *authz.PolicyTransaction, grantorID string, spec SourceSpec) error {
	if err := validateGrantorIdentity(tx.DB(), grantorID); err != nil {
		return err
	}
	var registered int64
	if err := tx.DB().Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", spec.ResourceType, spec.Operation).Count(&registered).Error; err != nil {
		return err
	}
	if registered == 0 {
		return ErrInvalidRequest
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

func grantInTransaction(tx *authz.PolicyTransaction, proxyID, grantorID string, spec SourceSpec) (model.ProxyGrantSource, bool, error) {
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
			BindingID: spec.BindingID, GrantedBy: grantorID, LifecycleStatus: StatusActive,
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
			"granted_by": grantorID, "lifecycle_status": StatusActive, "revoked_at": nil,
		}).Error; err != nil {
			return row, false, err
		}
		row.KNID, row.BindingType, row.BindingID = spec.KNID, spec.BindingType, spec.BindingID
		row.GrantedBy, row.LifecycleStatus, row.RevokedAt = grantorID, StatusActive, nil
		changed = true
	case row.LifecycleStatus != StatusActive:
		return row, false, ErrInvalidRequest
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
	if len(reason) > 255 {
		reason = reason[:255]
	}
	id, err := newID()
	if err != nil {
		return fmt.Errorf("generate proxy grant audit id: %w", err)
	}
	return db.Create(&model.ProxyGrantAuditLog{
		ID: id, Action: action, Decision: decision, Reason: reason, GrantorID: grantorID,
		ProxyAccountID: proxyID, ResourceType: spec.ResourceType, ResourceID: spec.ResourceID,
		Operation: spec.Operation, SourceType: spec.SourceType, SourceID: spec.SourceID,
	}).Error
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
