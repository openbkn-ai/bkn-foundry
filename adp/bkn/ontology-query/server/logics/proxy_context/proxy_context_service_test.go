// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy_context

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

type proxyAccessStub struct {
	mapping *interfaces.KnowledgeNetworkProxyAccount
	err     error
	binding interfaces.TrustedProxyBinding
}

func (s *proxyAccessStub) ResolveKnowledgeNetworkProxy(_ context.Context,
	binding interfaces.TrustedProxyBinding) (*interfaces.KnowledgeNetworkProxyAccount, error) {
	s.binding = binding
	return s.mapping, s.err
}

func TestResolveBuildsReadyDualPrincipalContext(t *testing.T) {
	access := &proxyAccessStub{mapping: readyMapping()}
	resolver := NewProxyContextResolver(access)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "caller-1", Type: "user"})
	binding := validBinding()

	got, err := resolver.Resolve(ctx, binding)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if access.binding != binding || got.Caller.ID != "caller-1" || got.Proxy.ID != "proxy-1" ||
		got.Proxy.Type != interfaces.ProxyAccountTypeApp || got.ProxyVersion != 4 || got.Binding != binding {
		t.Fatalf("unexpected proxy context: %#v", got)
	}
}

func TestResolveAcceptsPublishedLogicPropertyToolBinding(t *testing.T) {
	access := &proxyAccessStub{mapping: readyMapping()}
	resolver := NewProxyContextResolver(access)
	binding := interfaces.TrustedProxyBinding{
		KNID: "kn-1", ChildType: interfaces.PermissionResourceTypeLogicProperty, ChildID: "binding-hash",
		TargetType: interfaces.ProxyTargetTypeToolBox, TargetID: "box-1", Operation: interfaces.PermissionOperationExecute,
	}

	got, err := resolver.Resolve(callerContext(), binding)
	if err != nil || got == nil || got.Binding != binding || access.binding != binding {
		t.Fatalf("Resolve() = %#v, %v; resolved binding = %#v", got, err, access.binding)
	}
}

func TestResolveFailsClosedForInvalidMappingStates(t *testing.T) {
	tests := map[string]func(*interfaces.KnowledgeNetworkProxyAccount){
		"wrong knowledge network": func(m *interfaces.KnowledgeNetworkProxyAccount) { m.KNID = "kn-2" },
		"missing proxy":           func(m *interfaces.KnowledgeNetworkProxyAccount) { m.ProxyAccountID = "" },
		"wrong proxy type":        func(m *interfaces.KnowledgeNetworkProxyAccount) { m.ProxyAccountType = "user" },
		"disabled":                func(m *interfaces.KnowledgeNetworkProxyAccount) { m.LifecycleStatus = "disabled" },
		"sync failed":             func(m *interfaces.KnowledgeNetworkProxyAccount) { m.SyncStatus = "failed" },
		"version missing":         func(m *interfaces.KnowledgeNetworkProxyAccount) { m.Version = 0 },
		"published missing":       func(m *interfaces.KnowledgeNetworkProxyAccount) { m.PublishedModelVersion = "" },
		"model version mismatch":  func(m *interfaces.KnowledgeNetworkProxyAccount) { m.SyncedModelVersion = "v3" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			mapping := readyMapping()
			mutate(mapping)
			resolver := NewProxyContextResolver(&proxyAccessStub{mapping: mapping})
			got, err := resolver.Resolve(callerContext(), validBinding())
			assertServiceUnavailable(t, got, err)
		})
	}
}

func TestResolveFailsClosedWhenMappingDependencyFails(t *testing.T) {
	resolver := NewProxyContextResolver(&proxyAccessStub{err: errors.New("unavailable")})
	got, err := resolver.Resolve(callerContext(), validBinding())
	assertServiceUnavailable(t, got, err)
}

func TestResolveMapsStableBKNProxyErrors(t *testing.T) {
	tests := map[string]struct {
		status int
		code   string
		want   string
	}{
		"missing mapping": {
			status: http.StatusNotFound,
			code:   bknProxyMappingNotFoundCode,
			want:   oerrors.OntologyQuery_Proxy_MappingNotFound,
		},
		"disabled": {
			status: http.StatusServiceUnavailable,
			code:   bknProxyDisabledCode,
			want:   oerrors.OntologyQuery_Proxy_Disabled,
		},
		"sync failed": {
			status: http.StatusServiceUnavailable,
			code:   bknProxySyncFailedCode,
			want:   oerrors.OntologyQuery_Proxy_SyncFailed,
		},
		"sync pending": {
			status: http.StatusServiceUnavailable,
			code:   bknProxySyncPendingCode,
			want:   oerrors.OntologyQuery_Proxy_SyncPending,
		},
		"invalid binding": {
			status: http.StatusForbidden,
			code:   bknProxyBindingInvalidCode,
			want:   oerrors.OntologyQuery_Proxy_BindingInvalid,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			resolver := NewProxyContextResolver(&proxyAccessStub{err: &interfaces.KnowledgeNetworkProxyResolveError{
				StatusCode: test.status,
				Code:       test.code,
			}})
			_, err := resolver.Resolve(callerContext(), validBinding())
			httpErr, ok := err.(*rest.HTTPError)
			if !ok || httpErr.BaseError.ErrorCode != test.want {
				t.Fatalf("Resolve() error = %#v, want code %q", err, test.want)
			}
		})
	}
}

func TestResolveRejectsMissingCallerAndForgedBinding(t *testing.T) {
	resolver := NewProxyContextResolver(&proxyAccessStub{mapping: readyMapping()})
	got, err := resolver.Resolve(context.Background(), validBinding())
	assertServiceUnavailable(t, got, err)

	binding := validBinding()
	binding.TargetID = "resource-*"
	got, err = resolver.Resolve(callerContext(), binding)
	assertProxyError(t, got, err, http.StatusForbidden, oerrors.OntologyQuery_Proxy_BindingInvalid)

	binding = validBinding()
	binding.TargetID = "unbound/resource"
	got, err = resolver.Resolve(callerContext(), binding)
	assertProxyError(t, got, err, http.StatusForbidden, oerrors.OntologyQuery_Proxy_BindingInvalid)

	binding = validBinding()
	binding.Operation = interfaces.PermissionOperationExecute
	got, err = resolver.Resolve(callerContext(), binding)
	assertProxyError(t, got, err, http.StatusForbidden, oerrors.OntologyQuery_Proxy_BindingInvalid)
}

func assertServiceUnavailable(t *testing.T, _ *interfaces.TrustedProxyContext, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok || httpErr.HTTPCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 HTTPError, got %T %#v", err, err)
	}
}

func assertProxyError(t *testing.T, _ *interfaces.TrustedProxyContext, err error, status int, code string) {
	t.Helper()
	httpErr, ok := err.(*rest.HTTPError)
	if !ok || httpErr.HTTPCode != status || httpErr.BaseError.ErrorCode != code {
		t.Fatalf("expected status %d code %q, got %T %#v", status, code, err, err)
	}
}

func callerContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "caller-1", Type: "user"})
}

func readyMapping() *interfaces.KnowledgeNetworkProxyAccount {
	return &interfaces.KnowledgeNetworkProxyAccount{
		KNID:                  "kn-1",
		ProxyAccountID:        "proxy-1",
		ProxyAccountType:      interfaces.ProxyAccountTypeApp,
		LifecycleStatus:       interfaces.ProxyLifecycleActive,
		Version:               4,
		SyncStatus:            interfaces.ProxySyncReady,
		PublishedModelVersion: "v4",
		SyncedModelVersion:    "v4",
	}
}

func validBinding() interfaces.TrustedProxyBinding {
	return interfaces.TrustedProxyBinding{
		KNID:       "kn-1",
		ChildType:  interfaces.PermissionResourceTypeObjectType,
		ChildID:    "ot-1",
		TargetType: interfaces.ProxyTargetTypeResource,
		TargetID:   "resource-1",
		Operation:  interfaces.PermissionOperationQueryData,
	}
}
