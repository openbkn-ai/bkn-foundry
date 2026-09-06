// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"errors"
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

	ProxyAccountTypeApp        = "app"
	ProxyManagerBKN            = "bkn"
	ProxyManagedResourceTypeKN = "knowledge_network"
	ProxyLifecycleActive       = "active"
	ProxyTargetTypeToolBox     = "tool_box"
	ProxyTargetTypeMCP         = "mcp"
	ProxyChildTypeAction       = "action_type"
	ProxyChildTypeLogic        = "logic_property"
	ProxyOperationExecute      = "execute"
)

var (
	ErrProxyExecutionDenied      = errors.New("proxy execution denied")
	ErrProxyExecutionUnavailable = errors.New("proxy execution authorization unavailable")
)

// ProxyExecutionContext is the trusted dual-principal context produced from a
// current published knowledge-network binding by ontology-query.
type ProxyExecutionContext struct {
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

type proxyExecutionContextKey struct{}

// WithProxyExecutionContext attaches a route-validated internal proxy context.
func WithProxyExecutionContext(ctx context.Context, request ProxyExecutionContext) context.Context {
	return context.WithValue(ctx, proxyExecutionContextKey{}, request)
}

// ProxyExecutionContextFromContext returns the route-validated proxy context.
func ProxyExecutionContextFromContext(ctx context.Context) (ProxyExecutionContext, bool) {
	if ctx == nil {
		return ProxyExecutionContext{}, false
	}
	request, ok := ctx.Value(proxyExecutionContextKey{}).(ProxyExecutionContext)
	return request, ok
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

// ProxyExecutionAuthorizationAccess reads managed proxy state and exact
// resource authorization directly from bkn-safe.
type ProxyExecutionAuthorizationAccess interface {
	GetManagedProxy(ctx context.Context, proxyID string) (*ManagedProxyAccount, error)
	CheckPermission(ctx context.Context, proxyID, resourceType, resourceID, operation string) (bool, error)
}

// ProxyExecutionAuthorizer is the final PEP immediately before Tool or MCP execution.
type ProxyExecutionAuthorizer interface {
	Authorize(ctx context.Context, request ProxyExecutionContext) error
}

// ProxyExecutionAuditEvent correlates both principals with the final outbound decision.
type ProxyExecutionAuditEvent struct {
	CallerID       string `json:"caller_id"`
	CallerType     string `json:"caller_type"`
	KnowledgeID    string `json:"kn_id"`
	ChildType      string `json:"kn_child_type"`
	ChildID        string `json:"kn_child_id"`
	ProxyAccountID string `json:"proxy_account_id"`
	ProxyVersion   uint64 `json:"proxy_version"`
	TargetType     string `json:"target_resource_type"`
	TargetID       string `json:"target_resource_id"`
	Operation      string `json:"operation"`
	ExecutionID    string `json:"execution_id,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
}

// ProxyExecutionAuditRecorder persists or logs final proxy execution decisions.
type ProxyExecutionAuditRecorder interface {
	RecordProxyExecution(context.Context, ProxyExecutionAuditEvent)
}
