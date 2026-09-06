// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"
)

const (
	HTTPHeaderBKNCallerID     = "x-bkn-caller-id"
	HTTPHeaderBKNCallerType   = "x-bkn-caller-type"
	HTTPHeaderBKNKnowledgeID  = "x-bkn-kn-id"
	HTTPHeaderBKNChildType    = "x-bkn-child-type"
	HTTPHeaderBKNChildID      = "x-bkn-child-id"
	HTTPHeaderBKNProxyVersion = "x-bkn-proxy-version"
	HTTPHeaderBKNTargetType   = "x-bkn-target-type"
	HTTPHeaderBKNTargetID     = "x-bkn-target-id"
	HTTPHeaderBKNOperation    = "x-bkn-operation"
	HTTPHeaderBKNExecutionID  = "x-bkn-execution-id"

	ProxyAccountTypeApp = "app"

	ProxyLifecycleActive    = "active"
	ProxyLifecycleDisabling = "disabling"
	ProxyLifecycleArchived  = "archived"
	ProxySyncPending        = "pending"
	ProxySyncReady          = "ready"
	ProxySyncFailed         = "failed"

	ProxyTargetTypeResource = "resource"
	ProxyTargetTypeToolBox  = "tool_box"
	ProxyTargetTypeMCP      = "mcp"
)

type proxyContextKey struct{}

// KnowledgeNetworkProxyAccount is the runtime subset of BKN's authoritative
// knowledge-network-to-proxy mapping.
type KnowledgeNetworkProxyAccount struct {
	KNID                  string `json:"kn_id"`
	ProxyAccountID        string `json:"proxy_account_id"`
	ProxyAccountType      string `json:"proxy_account_type"`
	LifecycleStatus       string `json:"lifecycle_status"`
	Version               int64  `json:"version"`
	SyncStatus            string `json:"sync_status"`
	PublishedModelVersion string `json:"published_model_version"`
	SyncedModelVersion    string `json:"synced_model_version"`
}

// KnowledgeNetworkProxyResolveError preserves only the stable status and code
// returned by BKN. Response descriptions and internal details are deliberately
// not propagated across the service boundary.
type KnowledgeNetworkProxyResolveError struct {
	StatusCode int
	Code       string
}

func (e *KnowledgeNetworkProxyResolveError) Error() string {
	return fmt.Sprintf("knowledge network proxy resolution failed with status %d and code %s", e.StatusCode, e.Code)
}

// TrustedProxyBinding identifies one downstream target derived from the
// published main model. Public request payloads must never populate it.
type TrustedProxyBinding struct {
	KNID       string `json:"kn_id"`
	ChildType  string `json:"child_type"`
	ChildID    string `json:"child_id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Operation  string `json:"operation"`
}

// TrustedProxyContext keeps the real caller separate from the effective
// downstream principal used by restricted proxy entry points.
type TrustedProxyContext struct {
	Caller AccountInfo `json:"caller"`
	Proxy  AccountInfo `json:"proxy"`
	// UseDirectCaller marks the rollback-compatible path selected by the
	// deployment rollout policy. It is server-derived and is never accepted
	// from an inbound request.
	UseDirectCaller       bool                `json:"use_direct_caller,omitempty"`
	ProxyVersion          int64               `json:"proxy_version"`
	PublishedModelVersion string              `json:"published_model_version"`
	Binding               TrustedProxyBinding `json:"binding"`
	ExecutionID           string              `json:"execution_id,omitempty"`
}

// WithTrustedProxyContext attaches only a server-derived proxy context.
func WithTrustedProxyContext(ctx context.Context, proxy *TrustedProxyContext) context.Context {
	return context.WithValue(ctx, proxyContextKey{}, proxy)
}

// TrustedProxyContextFromContext returns the server-derived proxy context.
func TrustedProxyContextFromContext(ctx context.Context) (*TrustedProxyContext, bool) {
	if ctx == nil {
		return nil, false
	}
	proxy, ok := ctx.Value(proxyContextKey{}).(*TrustedProxyContext)
	return proxy, ok && proxy != nil
}

// KnowledgeNetworkProxyAccess loads the authoritative proxy mapping from BKN.
type KnowledgeNetworkProxyAccess interface {
	ResolveKnowledgeNetworkProxy(ctx context.Context, binding TrustedProxyBinding) (*KnowledgeNetworkProxyAccount, error)
}

// ProxyContextResolver validates the mapping state and constructs a trusted
// dual-principal context for one published binding.
type ProxyContextResolver interface {
	Resolve(ctx context.Context, binding TrustedProxyBinding) (*TrustedProxyContext, error)
}
