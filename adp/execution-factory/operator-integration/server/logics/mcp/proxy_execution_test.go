// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type denyingMCPProxyAuthorizer struct {
	calls int
	err   error
}

func (a *denyingMCPProxyAuthorizer) Authorize(context.Context, interfaces.ProxyExecutionContext) error {
	a.calls++
	return a.err
}

type mcpProxyExecutionAudit struct {
	events []interfaces.ProxyExecutionAuditEvent
}

func (a *mcpProxyExecutionAudit) RecordProxyExecution(_ context.Context, event interfaces.ProxyExecutionAuditEvent) {
	a.events = append(a.events, event)
}

func TestCallMCPToolRechecksManagedProxyBeforeCreatingOutboundClient(t *testing.T) {
	tests := map[string]error{
		"grant revoked":        interfaces.ErrProxyExecutionDenied,
		"bkn-safe unavailable": errors.New("connection refused"),
	}
	for name, authorizationErr := range tests {
		t.Run(name, func(t *testing.T) {
			authorizer := &denyingMCPProxyAuthorizer{err: authorizationErr}
			audit := &mcpProxyExecutionAudit{}
			service := &mcpServiceImpl{
				ProxyAuthorizer: authorizer,
				ProxyAudit:      audit,
			}
			ctx := interfaces.WithProxyExecutionContext(context.Background(), interfaces.ProxyExecutionContext{
				CallerID:     "caller-1",
				CallerType:   "user",
				KnowledgeID:  "kn-1",
				ChildType:    interfaces.ProxyChildTypeAction,
				ChildID:      "action-1",
				ProxyID:      "proxy-1",
				ProxyType:    interfaces.ProxyAccountTypeApp,
				ProxyVersion: 3,
				TargetType:   interfaces.ProxyTargetTypeMCP,
				TargetID:     "mcp-1",
				Operation:    interfaces.ProxyOperationExecute,
				ExecutionID:  "execution-1",
			})

			response, err := service.callTool(ctx, &CallToolRequest{})

			if err == nil || response != nil {
				t.Fatalf("callTool() = %+v, %v; want final authorization failure", response, err)
			}
			if authorizer.calls != 1 {
				t.Fatalf("authorizer calls = %d, want 1", authorizer.calls)
			}
			if len(audit.events) != 1 || audit.events[0].Decision != "deny" ||
				audit.events[0].ExecutionID != "execution-1" {
				t.Fatalf("audit events = %+v", audit.events)
			}
		})
	}
}
