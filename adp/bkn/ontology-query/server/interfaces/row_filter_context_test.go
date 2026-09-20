// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"testing"
)

func TestRowFilterCallerFromContextUsesAuthenticatedCaller(t *testing.T) {
	caller := AccountInfo{ID: "user-1", Type: "user"}
	ctx := context.WithValue(context.Background(), ACCOUNT_INFO_KEY, caller)
	actual, ok := RowFilterCallerFromContext(ctx)
	if !ok || actual != caller {
		t.Fatalf("caller = (%+v, %v), want (%+v, true)", actual, ok, caller)
	}
}

func TestRowFilterCallerFromContextUsesTrustedProxyCaller(t *testing.T) {
	caller := AccountInfo{ID: "user-1", Type: "user"}
	ctx := context.WithValue(context.Background(), ACCOUNT_INFO_KEY, AccountInfo{ID: "proxy-1", Type: "app"})
	ctx = WithTrustedProxyContext(ctx, &TrustedProxyContext{
		Caller: caller,
		Proxy:  AccountInfo{ID: "proxy-1", Type: ProxyAccountTypeApp},
	})
	actual, ok := RowFilterCallerFromContext(ctx)
	if !ok || actual != caller {
		t.Fatalf("caller = (%+v, %v), want trusted caller (%+v, true)", actual, ok, caller)
	}
}

func TestRowFilterCallerFromContextFailsClosed(t *testing.T) {
	if _, ok := RowFilterCallerFromContext(context.Background()); ok {
		t.Fatal("missing caller must fail closed")
	}
	ctx := context.WithValue(context.Background(), ACCOUNT_INFO_KEY, AccountInfo{ID: " user-1", Type: "user"})
	if _, ok := RowFilterCallerFromContext(ctx); ok {
		t.Fatal("malformed caller must fail closed")
	}
	ctx = WithTrustedProxyContext(context.Background(), &TrustedProxyContext{Proxy: AccountInfo{ID: "proxy-1", Type: "app"}})
	if _, ok := RowFilterCallerFromContext(ctx); ok {
		t.Fatal("proxy context without real caller must fail closed")
	}
}
