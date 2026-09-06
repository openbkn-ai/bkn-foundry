// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy_context

import (
	"context"
	"testing"

	"ontology-query/interfaces"
)

type rolloutResolverStub struct {
	calls int
}

func (s *rolloutResolverStub) Resolve(
	_ context.Context, binding interfaces.TrustedProxyBinding,
) (*interfaces.TrustedProxyContext, error) {
	s.calls++
	return &interfaces.TrustedProxyContext{Binding: binding, ProxyVersion: 1}, nil
}

func TestRolloutProxyContextResolver(t *testing.T) {
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "caller-1", Type: "user"})
	binding := interfaces.TrustedProxyBinding{KNID: "kn-1"}

	tests := []struct {
		name      string
		mode      string
		allowlist []string
		knID      string
		wantProxy bool
		wantCalls int
	}{
		{name: "default is off", mode: "", wantProxy: false, wantCalls: 0},
		{name: "unknown mode is off", mode: "enabled", wantProxy: false, wantCalls: 0},
		{name: "allowlisted network", mode: "allowlist", allowlist: []string{"kn-1"}, wantProxy: true, wantCalls: 1},
		{name: "network outside allowlist", mode: "allowlist", allowlist: []string{"kn-2"}, wantProxy: false, wantCalls: 0},
		{name: "all networks", mode: "all", knID: "kn-9", wantProxy: true, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := &rolloutResolverStub{}
			resolver := NewRolloutProxyContextResolver(next, tt.mode, tt.allowlist)
			requestBinding := binding
			if tt.knID != "" {
				requestBinding.KNID = tt.knID
			}
			got, err := resolver.Resolve(ctx, requestBinding)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.UseDirectCaller == tt.wantProxy {
				t.Fatalf("Resolve() UseDirectCaller = %v, want proxy enabled %v", got.UseDirectCaller, tt.wantProxy)
			}
			if next.calls != tt.wantCalls {
				t.Fatalf("downstream calls = %d, want %d", next.calls, tt.wantCalls)
			}
			if !tt.wantProxy && got.Caller.ID != "caller-1" {
				t.Fatalf("direct caller = %#v", got.Caller)
			}
		})
	}
}
