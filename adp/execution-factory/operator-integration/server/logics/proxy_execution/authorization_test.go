// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy_execution

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type fakeAuthorizationAccess struct {
	account         *interfaces.ManagedProxyAccount
	accountErr      error
	permission      bool
	permissionErr   error
	permissionCalls int
}

func (f *fakeAuthorizationAccess) GetManagedProxy(context.Context, string) (*interfaces.ManagedProxyAccount, error) {
	return f.account, f.accountErr
}

func (f *fakeAuthorizationAccess) CheckPermission(context.Context, string, string, string, string) (bool, error) {
	f.permissionCalls++
	return f.permission, f.permissionErr
}

func validRequest() interfaces.ProxyExecutionContext {
	return interfaces.ProxyExecutionContext{
		CallerID:     "caller-1",
		CallerType:   "user",
		KnowledgeID:  "kn-1",
		ChildType:    interfaces.ProxyChildTypeAction,
		ChildID:      "action-1",
		ProxyID:      "proxy-1",
		ProxyType:    interfaces.ProxyAccountTypeApp,
		ProxyVersion: 3,
		TargetType:   interfaces.ProxyTargetTypeToolBox,
		TargetID:     "box-1",
		Operation:    interfaces.ProxyOperationExecute,
		ExecutionID:  "execution-1",
	}
}

func validManagedProxy() *interfaces.ManagedProxyAccount {
	return &interfaces.ManagedProxyAccount{
		ProxyAccountID:      "proxy-1",
		AccountType:         interfaces.ProxyAccountTypeApp,
		ManagedBy:           interfaces.ProxyManagerBKN,
		ManagedResourceType: interfaces.ProxyManagedResourceTypeKN,
		ManagedResourceID:   "kn-1",
		LifecycleStatus:     interfaces.ProxyLifecycleActive,
		Enabled:             true,
		Version:             9,
	}
}

func TestAuthorizerAllowsCurrentManagedProxyWithExactGrant(t *testing.T) {
	access := &fakeAuthorizationAccess{account: validManagedProxy(), permission: true}
	if err := NewAuthorizer(access).Authorize(context.Background(), validRequest()); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if access.permissionCalls != 1 {
		t.Fatalf("permission calls = %d, want 1", access.permissionCalls)
	}
}

func TestAuthorizerRejectsInvalidManagedProxyBeforePolicyCheck(t *testing.T) {
	tests := map[string]func(*interfaces.ManagedProxyAccount){
		"missing":           func(account *interfaces.ManagedProxyAccount) { *account = interfaces.ManagedProxyAccount{} },
		"wrong id":          func(account *interfaces.ManagedProxyAccount) { account.ProxyAccountID = "proxy-2" },
		"wrong type":        func(account *interfaces.ManagedProxyAccount) { account.AccountType = "user" },
		"wrong manager":     func(account *interfaces.ManagedProxyAccount) { account.ManagedBy = "other" },
		"wrong owner type":  func(account *interfaces.ManagedProxyAccount) { account.ManagedResourceType = "other" },
		"wrong owner":       func(account *interfaces.ManagedProxyAccount) { account.ManagedResourceID = "kn-2" },
		"revoked lifecycle": func(account *interfaces.ManagedProxyAccount) { account.LifecycleStatus = "archived" },
		"disabled":          func(account *interfaces.ManagedProxyAccount) { account.Enabled = false },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			account := validManagedProxy()
			mutate(account)
			access := &fakeAuthorizationAccess{account: account, permission: true}
			err := NewAuthorizer(access).Authorize(context.Background(), validRequest())
			if !errors.Is(err, interfaces.ErrProxyExecutionDenied) {
				t.Fatalf("Authorize() error = %v, want denied", err)
			}
			if access.permissionCalls != 0 {
				t.Fatalf("permission calls = %d, want 0", access.permissionCalls)
			}
		})
	}
}

func TestAuthorizerFailsClosedWhenStateOrPolicyIsUnavailable(t *testing.T) {
	t.Run("state unavailable", func(t *testing.T) {
		access := &fakeAuthorizationAccess{accountErr: errors.New("safe unavailable")}
		err := NewAuthorizer(access).Authorize(context.Background(), validRequest())
		if !errors.Is(err, interfaces.ErrProxyExecutionUnavailable) {
			t.Fatalf("Authorize() error = %v, want unavailable", err)
		}
	})
	t.Run("policy unavailable", func(t *testing.T) {
		access := &fakeAuthorizationAccess{
			account:       validManagedProxy(),
			permissionErr: errors.New("safe unavailable"),
		}
		err := NewAuthorizer(access).Authorize(context.Background(), validRequest())
		if !errors.Is(err, interfaces.ErrProxyExecutionUnavailable) {
			t.Fatalf("Authorize() error = %v, want unavailable", err)
		}
	})
	t.Run("policy denied", func(t *testing.T) {
		access := &fakeAuthorizationAccess{account: validManagedProxy(), permission: false}
		err := NewAuthorizer(access).Authorize(context.Background(), validRequest())
		if !errors.Is(err, interfaces.ErrProxyExecutionDenied) {
			t.Fatalf("Authorize() error = %v, want denied", err)
		}
	})
	t.Run("access missing", func(t *testing.T) {
		err := NewAuthorizer(nil).Authorize(context.Background(), validRequest())
		if !errors.Is(err, interfaces.ErrProxyExecutionUnavailable) {
			t.Fatalf("Authorize() error = %v, want unavailable", err)
		}
	})
}

type recordingAudit struct {
	events []interfaces.ProxyExecutionAuditEvent
}

func (r *recordingAudit) RecordProxyExecution(_ context.Context, event interfaces.ProxyExecutionAuditEvent) {
	r.events = append(r.events, event)
}

func TestAuthorizeOutboundRunsOnlyAtManagedProxyBoundary(t *testing.T) {
	if err := AuthorizeOutbound(context.Background(), nil, nil); err != nil {
		t.Fatalf("direct execution error = %v", err)
	}

	t.Run("allowed", func(t *testing.T) {
		access := &fakeAuthorizationAccess{account: validManagedProxy(), permission: true}
		recorder := &recordingAudit{}
		ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "req_12345678"})
		ctx = interfaces.WithProxyExecutionContext(ctx, validRequest())

		if err := AuthorizeOutbound(ctx, NewAuthorizer(access), recorder); err != nil {
			t.Fatalf("AuthorizeOutbound() error = %v", err)
		}
		if len(recorder.events) != 1 || recorder.events[0].Decision != "allow" ||
			recorder.events[0].ExecutionID != "execution-1" ||
			recorder.events[0].RequestID != "req_12345678" {
			t.Fatalf("audit events = %+v", recorder.events)
		}
	})

	tests := []struct {
		name       string
		access     *fakeAuthorizationAccess
		authorizer interfaces.ProxyExecutionAuthorizer
		wantStatus int
		wantReason string
	}{
		{
			name: "revoked after submission",
			access: &fakeAuthorizationAccess{
				account: validManagedProxy(), permission: false,
			},
			wantStatus: http.StatusForbidden,
			wantReason: "proxy_or_policy_denied",
		},
		{
			name: "bkn-safe unavailable",
			access: &fakeAuthorizationAccess{
				accountErr: errors.New("connection refused"),
			},
			wantStatus: http.StatusServiceUnavailable,
			wantReason: "authorization_unavailable",
		},
		{
			name:       "authorizer missing",
			authorizer: nil,
			wantStatus: http.StatusServiceUnavailable,
			wantReason: "authorization_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingAudit{}
			ctx := interfaces.WithProxyExecutionContext(context.Background(), validRequest())
			authorizer := test.authorizer
			if test.access != nil {
				authorizer = NewAuthorizer(test.access)
			}
			err := AuthorizeOutbound(ctx, authorizer, recorder)
			var httpErr *oerrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != test.wantStatus {
				t.Fatalf("AuthorizeOutbound() error = %v, want HTTP %d", err, test.wantStatus)
			}
			if len(recorder.events) != 1 || recorder.events[0].Decision != "deny" ||
				recorder.events[0].Reason != test.wantReason {
				t.Fatalf("audit events = %+v", recorder.events)
			}
		})
	}
}
