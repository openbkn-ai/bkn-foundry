// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package proxy_execution implements the final authorization policy
// enforcement point for knowledge-network managed proxy execution.
package proxy_execution

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/bytedance/sonic"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type authorizer struct {
	access interfaces.ProxyExecutionAuthorizationAccess
}

// NewAuthorizer creates a fail-closed proxy execution authorizer.
func NewAuthorizer(access interfaces.ProxyExecutionAuthorizationAccess) interfaces.ProxyExecutionAuthorizer {
	return &authorizer{access: access}
}

// Authorize rechecks proxy ownership, current state, and the exact execution
// grant for every Tool or MCP call.
func (a *authorizer) Authorize(ctx context.Context, request interfaces.ProxyExecutionContext) error {
	if a.access == nil {
		return fmt.Errorf("%w: bkn-safe access is not configured", interfaces.ErrProxyExecutionUnavailable)
	}

	account, err := a.access.GetManagedProxy(ctx, request.ProxyID)
	if err != nil {
		return fmt.Errorf("%w: load managed proxy: %v", interfaces.ErrProxyExecutionUnavailable, err)
	}
	if !matchesCurrentProxy(account, request) {
		return interfaces.ErrProxyExecutionDenied
	}

	allowed, err := a.access.CheckPermission(
		ctx, request.ProxyID, request.TargetType, request.TargetID, request.Operation,
	)
	if err != nil {
		return fmt.Errorf("%w: check resource policy: %v", interfaces.ErrProxyExecutionUnavailable, err)
	}
	if !allowed {
		return interfaces.ErrProxyExecutionDenied
	}
	return nil
}

func matchesCurrentProxy(account *interfaces.ManagedProxyAccount, request interfaces.ProxyExecutionContext) bool {
	// ProxyVersion is the BKN mapping version. bkn-safe's Version is a separate
	// account lifecycle counter, so the two values must not be compared. The
	// mapping version is validated against the latest published model upstream
	// and retained here for trusted-context validation and audit correlation.
	return account != nil &&
		account.ProxyAccountID == request.ProxyID &&
		account.AccountType == interfaces.ProxyAccountTypeApp &&
		account.ManagedBy == interfaces.ProxyManagerBKN &&
		account.ManagedResourceType == interfaces.ProxyManagedResourceTypeKN &&
		account.ManagedResourceID == request.KnowledgeID &&
		account.LifecycleStatus == interfaces.ProxyLifecycleActive &&
		account.Enabled
}

type auditLogger struct {
	logger interfaces.Logger
}

// NewAuditLogger creates a structured audit recorder for proxy execution decisions.
func NewAuditLogger(logger interfaces.Logger) interfaces.ProxyExecutionAuditRecorder {
	return auditLogger{logger: logger}
}

func (a auditLogger) RecordProxyExecution(ctx context.Context, event interfaces.ProxyExecutionAuditEvent) {
	if a.logger == nil {
		return
	}
	encoded, err := sonic.MarshalString(event)
	if err != nil {
		a.logger.WithContext(ctx).Errorf("marshal proxy execution audit failed: %v", err)
		return
	}
	a.logger.WithContext(ctx).Infof("proxy execution authorization audit: %s", encoded)
}

// AuthorizeOutbound runs the final PEP only when the request carries a
// route-validated managed proxy context. Callers must invoke it immediately
// before creating or using an outbound client.
func AuthorizeOutbound(
	ctx context.Context,
	authorizer interfaces.ProxyExecutionAuthorizer,
	recorder interfaces.ProxyExecutionAuditRecorder,
) error {
	request, ok := interfaces.ProxyExecutionContextFromContext(ctx)
	if !ok {
		return nil
	}
	if authorizer == nil {
		RecordDecision(ctx, recorder, request, "deny", "authorization_unavailable")
		return oerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "proxy execution authorization is unavailable")
	}
	err := authorizer.Authorize(ctx, request)
	if err == nil {
		RecordDecision(ctx, recorder, request, "allow", "")
		return nil
	}
	if errors.Is(err, interfaces.ErrProxyExecutionDenied) {
		RecordDecision(ctx, recorder, request, "deny", "proxy_or_policy_denied")
		return oerrors.DefaultHTTPError(ctx, http.StatusForbidden, "proxy execution is forbidden")
	}
	RecordDecision(ctx, recorder, request, "deny", "authorization_unavailable")
	return oerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "proxy execution authorization is unavailable")
}

// RecordDecision emits a correlated dual-principal authorization decision.
func RecordDecision(
	ctx context.Context,
	recorder interfaces.ProxyExecutionAuditRecorder,
	request interfaces.ProxyExecutionContext,
	decision string,
	reason string,
) {
	if recorder == nil {
		return
	}
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	spanContext := trace.SpanContextFromContext(ctx)
	traceID := ""
	if spanContext.IsValid() {
		traceID = spanContext.TraceID().String()
	}
	recorder.RecordProxyExecution(ctx, interfaces.ProxyExecutionAuditEvent{
		CallerID:       request.CallerID,
		CallerType:     request.CallerType,
		KnowledgeID:    request.KnowledgeID,
		ChildType:      request.ChildType,
		ChildID:        request.ChildID,
		ProxyAccountID: request.ProxyID,
		ProxyVersion:   request.ProxyVersion,
		TargetType:     request.TargetType,
		TargetID:       request.TargetID,
		Operation:      request.Operation,
		ExecutionID:    request.ExecutionID,
		RequestID:      traceContext.RequestID,
		TraceID:        traceID,
		Decision:       decision,
		Reason:         reason,
	})
}
