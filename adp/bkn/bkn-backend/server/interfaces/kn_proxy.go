// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
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
)

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
	LastSyncError         string `json:"last_error,omitempty"`
	LastGrantorID         string `json:"-"`
	LockOwner             string `json:"-"`
	LockUntil             int64  `json:"-"`
	CreatedAt             int64  `json:"created_at"`
	UpdatedAt             int64  `json:"updated_at"`
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

type ProxyGrantSyncResult struct {
	Added     int `json:"added"`
	Revoked   int `json:"revoked"`
	Unchanged int `json:"unchanged"`
}

type ProxyGrantReconcileResult struct {
	PoliciesRestored  int `json:"policies_restored"`
	PoliciesRemoved   int `json:"policies_removed"`
	MarkersCreated    int `json:"markers_created"`
	MarkersRemoved    int `json:"markers_removed"`
	UntrackedPolicies int `json:"untracked_policies"`
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

type KNProxySyncPlan struct {
	KNID           string                 `json:"kn_id"`
	ProxyAccountID string                 `json:"proxy_account_id,omitempty"`
	ModelVersion   string                 `json:"model_version"`
	Sources        []ProxyGrantSourceSpec `json:"sources"`
}

//go:generate mockgen -source ../interfaces/kn_proxy.go -destination ../interfaces/mock/mock_kn_proxy.go
type KNProxyAccess interface {
	Get(ctx context.Context, knID string) (*KNProxyAccount, error)
	List(ctx context.Context) ([]*KNProxyAccount, error)
	Ensure(ctx context.Context, mapping *KNProxyAccount) (*KNProxyAccount, bool, error)
	SetPending(ctx context.Context, tx *sql.Tx, knID, modelVersion, grantorID string, updatedAt int64) error
	SetSyncResult(ctx context.Context, knID, modelVersion, syncStatus, syncedVersion, lastError string, updatedAt int64) (bool, error)
	SetLifecycle(ctx context.Context, knID, lifecycleStatus string, updatedAt int64) error
	TryAcquireLock(ctx context.Context, knID, owner string, now, lockUntil int64) (bool, error)
	ReleaseLock(ctx context.Context, knID, owner string, updatedAt int64) error
	ListProxyConflicts(ctx context.Context) (map[string][]string, error)
}

type ManagedProxyAccess interface {
	Create(ctx context.Context, knID, name string) (*ManagedProxyAccount, bool, error)
	Restore(ctx context.Context, proxyAccountID string) (*ManagedProxyAccount, error)
	Disable(ctx context.Context, proxyAccountID string) (*ManagedProxyAccount, error)
	Archive(ctx context.Context, proxyAccountID string) (*ManagedProxyAccount, error)
	CheckGrant(ctx context.Context, proxyAccountID, grantorID string, source ProxyGrantSourceSpec) (ProxyGrantCheckResult, error)
	SyncGrants(ctx context.Context, proxyAccountID, grantorID string, sources []ProxyGrantSourceSpec) (ProxyGrantSyncResult, error)
	ReconcileGrants(ctx context.Context, proxyAccountID, requestedBy string) (ProxyGrantReconcileResult, error)
}
