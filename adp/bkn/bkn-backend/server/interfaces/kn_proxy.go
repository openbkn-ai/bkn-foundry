// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

const (
	KNProxyAccountTypeApp = "app"

	KNProxyLifecycleActive    = "active"
	KNProxyLifecycleDisabling = "disabling"
	KNProxyLifecycleArchived  = "archived"

	KNProxySyncPending = "pending"
	KNProxySyncReady   = "ready"
	KNProxySyncFailed  = "failed"

	ProxyGrantSourceTypeKNBinding = "kn_proxy_binding"
	KNProxyBindingTypeCapability  = "capability_binding"

	// KNProxyTargetTypeSkill is the bkn-safe resource type of a mounted Skill.
	// Its grant sources are best effort: see IsBestEffortProxyGrantSource.
	KNProxyTargetTypeSkill = "skill"
)

// ManagedProxyStatusError is a non-success status answered by bkn-safe's
// managed proxy API. Callers use the status to tell a request bkn-safe refuses
// to understand (400) from an unavailable service.
type ManagedProxyStatusError struct {
	Method     string
	Path       string
	StatusCode int
}

func (e *ManagedProxyStatusError) Error() string {
	return fmt.Sprintf("bkn-safe %s %s returned status %d", e.Method, e.Path, e.StatusCode)
}

// IsBestEffortProxyGrantSource reports whether a grant source may be left
// unmaterialized without failing the network's proxy synchronization.
//
// Only a mounted Skill qualifies. A delegator who lacks execute on it, or an
// authorization service that predates Skill sources, leaves that one Skill
// unreadable through the proxy instead of blocking every proxied data read and
// execution of the network.
func IsBestEffortProxyGrantSource(source ProxyGrantSourceSpec) bool {
	return source.BindingType == KNProxyBindingTypeCapability && source.ResourceType == KNProxyTargetTypeSkill
}

// KNProxyAccount is BKN's authoritative, environment-local mapping between one
// knowledge network and one bkn-safe managed application account.
type KNProxyAccount struct {
	KNID                  string `json:"kn_id"`
	ProxyAccountID        string `json:"proxy_account_id"`
	ProxyAccountType      string `json:"proxy_account_type"`
	LifecycleStatus       string `json:"lifecycle_status"`
	Version               int64  `json:"version"`
	SyncStatus            string `json:"sync_status"`
	PublishedModelVersion string `json:"published_model_version"`
	SyncedModelVersion    string `json:"synced_model_version"`
	// ResolvedBinding is populated only by the internal proxy-resolution endpoint.
	// It is read from the published snapshot and is never persisted on the mapping.
	ResolvedBinding     *KNProxyBinding `json:"resolved_binding,omitempty"`
	PendingModelVersion string          `json:"-"`
	SyncGeneration      int64           `json:"-"`
	LastSyncError       string          `json:"last_error,omitempty"`
	LastGrantorID       string          `json:"-"`
	LockOwner           string          `json:"-"`
	LockUntil           int64           `json:"-"`
	LastSyncStartedAt   int64           `json:"-"`
	LastSyncSucceededAt int64           `json:"-"`
	CreatedAt           int64           `json:"created_at"`
	UpdatedAt           int64           `json:"updated_at"`
}

// KNProxyGovernanceView is the public, sanitized projection of a proxy
// mapping. Internal synchronization details such as last_error and grantor or
// lock identities are never returned by OAuth governance APIs.
type KNProxyGovernanceView struct {
	KNID                  string `json:"kn_id"`
	ProxyAccountID        string `json:"proxy_account_id"`
	ProxyAccountType      string `json:"proxy_account_type"`
	LifecycleStatus       string `json:"lifecycle_status"`
	LastErrorCode         string `json:"last_error_code,omitempty"`
	Version               int64  `json:"version"`
	SyncStatus            string `json:"sync_status"`
	PublishedModelVersion string `json:"published_model_version"`
	SyncedModelVersion    string `json:"synced_model_version"`
	CreatedAt             int64  `json:"created_at"`
	UpdatedAt             int64  `json:"updated_at"`
}

type KNProxyAccountList struct {
	Entries []*KNProxyGovernanceView `json:"entries"`
	Total   int                      `json:"total"`
}

func NewKNProxyGovernanceView(mapping *KNProxyAccount) *KNProxyGovernanceView {
	if mapping == nil {
		return nil
	}
	view := &KNProxyGovernanceView{
		KNID:                  mapping.KNID,
		ProxyAccountID:        mapping.ProxyAccountID,
		ProxyAccountType:      mapping.ProxyAccountType,
		LifecycleStatus:       mapping.LifecycleStatus,
		Version:               mapping.Version,
		SyncStatus:            mapping.SyncStatus,
		PublishedModelVersion: mapping.PublishedModelVersion,
		SyncedModelVersion:    mapping.SyncedModelVersion,
		CreatedAt:             mapping.CreatedAt,
		UpdatedAt:             mapping.UpdatedAt,
	}
	if mapping.LastSyncError != "" {
		view.LastErrorCode = "PROXY_SYNC_FAILED"
	}
	return view
}

// ManagedProxyAccount is the lifecycle representation returned by bkn-safe.
type ManagedProxyAccount struct {
	ProxyAccountID            string `json:"proxy_account_id"`
	AccountType               string `json:"account_type"`
	Name                      string `json:"name"`
	ManagedBy                 string `json:"managed_by"`
	ManagedResourceType       string `json:"managed_resource_type"`
	ManagedResourceID         string `json:"managed_resource_id"`
	LifecycleStatus           string `json:"lifecycle_status"`
	Enabled                   bool   `json:"enabled"`
	LoginEnabled              bool   `json:"login_enabled"`
	CredentialIssuanceEnabled bool   `json:"credential_issuance_enabled"`
	Version                   int64  `json:"version"`
}

// ProxyGrantSourceSpec is one published-model binding's requirement for one
// concrete downstream operation.
type ProxyGrantSourceSpec struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Operation    string `json:"operation"`
	SourceType   string `json:"source_type"`
	SourceID     string `json:"source_id"`
	KNID         string `json:"kn_id"`
	BindingType  string `json:"binding_type"`
	BindingID    string `json:"binding_id"`
}

type ProxyGrantCheckResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type ProxyGrantBatchCheckResult struct {
	DeniedSources   []ProxyGrantSourceSpec     `json:"denied_sources"`
	ResolvedSources []ProxyGrantResolvedSource `json:"resolved_sources"`
}

// ProxyGrantResolvedSource identifies the effective delegator retained or
// selected by bkn-safe for an allowed source.
type ProxyGrantResolvedSource struct {
	ProxyGrantSourceSpec
	GrantedBy string `json:"granted_by"`
}

type ProxyGrantSyncResult struct {
	Added       int `json:"added"`
	Transferred int `json:"transferred"`
	Revoked     int `json:"revoked"`
	Unchanged   int `json:"unchanged"`
}

type ProxyGrantReconcileResult struct {
	PoliciesRestored  int `json:"policies_restored"`
	PoliciesRemoved   int `json:"policies_removed"`
	MarkersCreated    int `json:"markers_created"`
	MarkersRemoved    int `json:"markers_removed"`
	UntrackedPolicies int `json:"untracked_policies"`
	InvalidSources    int `json:"invalid_sources"`
}

// KNProxyReconcileReport identifies BKN mapping defects and reports any
// bkn-safe ledger/policy drift repaired by the internal reconcile operation.
type KNProxyReconcileReport struct {
	MissingMappings    []string                             `json:"missing_mappings"`
	OrphanMappings     []string                             `json:"orphan_mappings"`
	ConflictingProxy   map[string][]string                  `json:"conflicting_proxy_accounts"`
	AuthorizationDrift map[string]ProxyGrantReconcileResult `json:"authorization_drift"`
	Errors             map[string]string                    `json:"errors,omitempty"`
}

type KNProxyGovernanceReconcileReport struct {
	MissingMappings    []string                             `json:"missing_mappings"`
	OrphanMappings     []string                             `json:"orphan_mappings"`
	ConflictingProxy   map[string][]string                  `json:"conflicting_proxy_accounts"`
	AuthorizationDrift map[string]ProxyGrantReconcileResult `json:"authorization_drift"`
	FailedKNIDs        []string                             `json:"failed_kn_ids,omitempty"`
}

func NewKNProxyGovernanceReconcileReport(report *KNProxyReconcileReport) *KNProxyGovernanceReconcileReport {
	if report == nil {
		return nil
	}
	failedKNIDs := make([]string, 0, len(report.Errors))
	for knID := range report.Errors {
		failedKNIDs = append(failedKNIDs, knID)
	}
	sort.Strings(failedKNIDs)
	view := &KNProxyGovernanceReconcileReport{
		MissingMappings:    report.MissingMappings,
		OrphanMappings:     report.OrphanMappings,
		ConflictingProxy:   report.ConflictingProxy,
		AuthorizationDrift: report.AuthorizationDrift,
		FailedKNIDs:        failedKNIDs,
	}
	if view.MissingMappings == nil {
		view.MissingMappings = []string{}
	}
	if view.OrphanMappings == nil {
		view.OrphanMappings = []string{}
	}
	if view.ConflictingProxy == nil {
		view.ConflictingProxy = map[string][]string{}
	}
	if view.AuthorizationDrift == nil {
		view.AuthorizationDrift = map[string]ProxyGrantReconcileResult{}
	}
	return view
}

type KNProxySyncPlan struct {
	KNID           string                 `json:"kn_id"`
	ProxyAccountID string                 `json:"proxy_account_id,omitempty"`
	ModelVersion   string                 `json:"model_version"`
	Sources        []ProxyGrantSourceSpec `json:"sources"`
}

// KNProxyBinding identifies one runtime target associated with a published
// knowledge-network child. The proxy-resolution endpoint accepts child, target
// ID, and operation, then returns the published target type from its snapshot.
type KNProxyBinding struct {
	ChildType  string `json:"child_type"`
	ChildID    string `json:"child_id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Operation  string `json:"operation"`
}

// KNProxyBindingRef identifies all published grant sources owned by one BKN
// child binding, independently of its current downstream target.
type KNProxyBindingRef struct {
	BindingType string `json:"binding_type"`
	BindingID   string `json:"binding_id"`
}

//go:generate mockgen -source ../interfaces/kn_proxy.go -destination ../interfaces/mock/mock_kn_proxy.go
type KNProxyAccess interface {
	Get(ctx context.Context, knID string) (*KNProxyAccount, error)
	List(ctx context.Context) ([]*KNProxyAccount, error)
	Ensure(ctx context.Context, mapping *KNProxyAccount) (*KNProxyAccount, bool, error)
	SetPending(ctx context.Context, tx *sql.Tx, knID, modelVersion, grantorID, lockOwner string, updatedAt int64) (int64, error)
	ReserveSyncGeneration(ctx context.Context, knID, lockOwner string, updatedAt int64) (int64, error)
	MarkSyncFailed(ctx context.Context, knID string, generation int64, lockOwner, lastError string, updatedAt int64) (bool, error)
	ReplacePublishedSnapshotAndMarkReady(ctx context.Context, knID string, generation int64,
		lockOwner, snapshotVersion string, sources []ProxyGrantSourceSpec, updatedAt int64) error
	ListPublishedSources(ctx context.Context, knID string, bindings []KNProxyBindingRef) ([]ProxyGrantSourceSpec, error)
	ReplacePublishedBindingsAndMarkReady(ctx context.Context, knID string, generation int64,
		lockOwner, snapshotVersion string, bindings []KNProxyBindingRef,
		sources []ProxyGrantSourceSpec, updatedAt int64) error
	DeletePublishedSnapshot(ctx context.Context, knID string) error
	ResolvePublishedBinding(ctx context.Context, knID string, binding KNProxyBinding) (*KNProxyBinding, error)
	ResolvePublishedBindings(ctx context.Context, knID string, bindings []KNProxyBinding) ([]KNProxyBinding, error)
	SetLifecycle(ctx context.Context, knID, lifecycleStatus string, updatedAt int64) error
	TryAcquireLock(ctx context.Context, knID, owner string, now, lockUntil int64) (bool, error)
	RenewLock(ctx context.Context, knID, owner string, now, lockUntil int64) (bool, error)
	ReleaseLock(ctx context.Context, knID, owner string, updatedAt int64) error
	ListProxyConflicts(ctx context.Context) (map[string][]string, error)
}

// KNProxyBindingResolver resolves a knowledge network's managed proxy for
// several runtime targets at once. Callers derive the bindings from their own
// persisted model and must already have authorized the business caller on each
// bound child. The resolver returns the ready mapping and the subset of
// bindings that are current published bindings; no other target may be read
// through the proxy.
type KNProxyBindingResolver interface {
	ResolveKNProxyBindings(ctx context.Context, knID string, bindings []KNProxyBinding) (*KNProxyAccount, []KNProxyBinding, error)
}

type ManagedProxyAccess interface {
	Create(ctx context.Context, knID, name string) (*ManagedProxyAccount, bool, error)
	Restore(ctx context.Context, proxyAccountID string) (*ManagedProxyAccount, error)
	Disable(ctx context.Context, proxyAccountID string) (*ManagedProxyAccount, error)
	Archive(ctx context.Context, proxyAccountID string) (*ManagedProxyAccount, error)
	CheckGrant(ctx context.Context, proxyAccountID, grantorID string, source ProxyGrantSourceSpec) (ProxyGrantCheckResult, error)
	CheckGrants(ctx context.Context, proxyAccountID, grantorID string, sources []ProxyGrantSourceSpec) (ProxyGrantBatchCheckResult, error)
	CheckGrantDelta(ctx context.Context, proxyAccountID, grantorID string,
		upserts, removals []ProxyGrantSourceSpec) (ProxyGrantBatchCheckResult, error)
	SyncGrants(ctx context.Context, proxyAccountID, grantorID string, syncGeneration int64,
		snapshotVersion string, sources []ProxyGrantSourceSpec) (ProxyGrantSyncResult, error)
	SyncGrantDelta(ctx context.Context, proxyAccountID, grantorID string, syncGeneration int64,
		baseSnapshotVersion, targetSnapshotVersion string, upserts, removals []ProxyGrantSourceSpec) (ProxyGrantSyncResult, error)
	ReconcileGrants(ctx context.Context, proxyAccountID, requestedBy string) (ProxyGrantReconcileResult, error)
}
