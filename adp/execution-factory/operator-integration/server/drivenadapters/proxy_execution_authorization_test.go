// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
)

func TestProxyExecutionAuthorizationAccessReadsStateAndExactGrant(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/in/v1/managed-proxy-accounts/proxy-1", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(common.HeaderBKNRequestID); got != "req_12345678" {
			t.Errorf("request id = %q, want req_12345678", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"proxy_account_id":      "proxy-1",
			"account_type":          "app",
			"managed_by":            "bkn",
			"managed_resource_type": "knowledge_network",
			"managed_resource_id":   "kn-1",
			"lifecycle_status":      "active",
			"enabled":               true,
			"version":               7,
		})
	})
	mux.HandleFunc("/api/safe/v1/authz/check", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			AccessorID string `json:"accessor_id"`
			Resource   struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"resource"`
			Operation string `json:"operation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.AccessorID != "proxy-1" || request.Resource.Type != "tool_box" ||
			request.Resource.ID != "box-1" || request.Operation != "execute" {
			t.Errorf("authorization request = %+v", request)
		}
		if got := r.Header.Get(common.HeaderBKNRequestID); got != "req_12345678" {
			t.Errorf("request id = %q, want req_12345678", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": true})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	access := &proxyExecutionAuthorizationAccess{
		safe: newSafeAuthorization(server.URL, testLogger{}),
	}
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "req_12345678"})
	account, err := access.GetManagedProxy(ctx, "proxy-1")
	if err != nil {
		t.Fatalf("GetManagedProxy() error = %v", err)
	}
	if account.ProxyAccountID != "proxy-1" || account.ManagedResourceID != "kn-1" || !account.Enabled {
		t.Fatalf("account = %+v", account)
	}
	allowed, err := access.CheckPermission(ctx, "proxy-1", "tool_box", "box-1", "execute")
	if err != nil || !allowed {
		t.Fatalf("CheckPermission() = %v, %v", allowed, err)
	}
}

func TestProxyExecutionAuthorizationAccessFailsClosed(t *testing.T) {
	t.Run("missing configuration", func(t *testing.T) {
		access := &proxyExecutionAuthorizationAccess{safe: newSafeAuthorization("", testLogger{})}
		if _, err := access.GetManagedProxy(context.Background(), "proxy-1"); err == nil {
			t.Fatal("GetManagedProxy() error = nil, want configuration error")
		}
		if _, err := access.CheckPermission(context.Background(), "proxy-1", "mcp", "mcp-1", "execute"); err == nil {
			t.Fatal("CheckPermission() error = nil, want configuration error")
		}
	})

	t.Run("missing account", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		defer server.Close()
		access := &proxyExecutionAuthorizationAccess{safe: newSafeAuthorization(server.URL, testLogger{})}
		account, err := access.GetManagedProxy(context.Background(), "missing")
		if err != nil || account != nil {
			t.Fatalf("GetManagedProxy() = %+v, %v; want nil, nil", account, err)
		}
	})

	t.Run("oversized response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maximumManagedProxyResponse+1)))
		}))
		defer server.Close()
		access := &proxyExecutionAuthorizationAccess{safe: newSafeAuthorization(server.URL, testLogger{})}
		if _, err := access.GetManagedProxy(context.Background(), "proxy-1"); err == nil {
			t.Fatal("GetManagedProxy() error = nil, want response size error")
		}
	})
}
