// Copyright openbkn.ai
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"errors"
)

type trustedProxyReadKey struct{}

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

	ProxyAccountTypeApp        = "app"
	ProxyManagerBKN            = "bkn"
	ProxyManagedResourceTypeKN = "knowledge_network"
	ProxyLifecycleActive       = "active"
	ProxyTargetTypeResource    = "resource"
	ProxyChildTypeObjectType   = "object_type"
	ProxyChildTypeRelationType = "relation_type"
	ProxyChildTypeMetric       = "metric"
)

var (
	ErrProxyAuthorizationDenied      = errors.New("proxy authorization denied")
	ErrProxyAuthorizationUnavailable = errors.New("proxy authorization unavailable")
)

// ProxyReadContext is the trusted proxy context resolved by ontology-query from a published model.
type ProxyReadContext struct {
	CallerID     string
	CallerType   string
	KnowledgeID  string
	ChildType    string
	ChildID      string
	ProxyID      string
	ProxyType    string
	ProxyVersion uint64
	TargetType   string
	TargetID     string
	Operation    string
	ExecutionID  string
}

// ManagedProxyAccount is the current managed proxy state returned by bkn-safe.
type ManagedProxyAccount struct {
	ProxyAccountID      string `json:"proxy_account_id"`
	AccountType         string `json:"account_type"`
	ManagedBy           string `json:"managed_by"`
	ManagedResourceType string `json:"managed_resource_type"`
	ManagedResourceID   string `json:"managed_resource_id"`
	LifecycleStatus     string `json:"lifecycle_status"`
	Enabled             bool   `json:"enabled"`
	Version             uint64 `json:"version"`
}

// ProxyAuthorizationAccess reads managed proxy state and checks resource policy directly with bkn-safe.
type ProxyAuthorizationAccess interface {
	GetManagedProxy(ctx context.Context, proxyID string) (*ManagedProxyAccount, error)
	CheckPermission(ctx context.Context, proxyID, resourceType, resourceID, operation string) (bool, error)
}

// ProxyAuthorizationService is the final PEP for Vega proxy reads.
type ProxyAuthorizationService interface {
	Authorize(ctx context.Context, request ProxyReadContext) error
}

// WithTrustedProxyRead marks a request whose target and operation already
// passed the restricted proxy PEP. Only the proxy handlers may set this marker;
// downstream metadata reads use it to avoid a second, unrelated account check.
func WithTrustedProxyRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, trustedProxyReadKey{}, true)
}

// IsTrustedProxyRead reports whether the restricted proxy PEP authorized this
// server-side call chain.
func IsTrustedProxyRead(ctx context.Context) bool {
	trusted, ok := ctx.Value(trustedProxyReadKey{}).(bool)
	return ok && trusted
}
