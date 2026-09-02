// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"ontology-query/interfaces"
)

func TestPermissionAccessFilterResources(t *testing.T) {
	t.Run("decodes the clean bkn-safe response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/safe/v1/authz/resource-filter" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resources":[{"resource_type":"metric","resource_id":"kn-a/m-1","operations":["query_data"]}]}`))
		}))
		defer server.Close()

		access := &permissionAccess{
			baseURL: server.URL,
			httpClient: rest.NewHTTPClientWithOptions(rest.HttpClientOptions{
				TimeOut: 1,
			}),
		}
		result, err := access.FilterResources(context.Background(), interfaces.PermissionFilterRequest{
			AccessorID: "account-1",
			Resources: []interfaces.PermissionResource{{
				Type: interfaces.PermissionResourceTypeMetric,
				ID:   "kn-a/m-1",
			}},
		})
		if err != nil {
			t.Fatalf("FilterResources() error = %v", err)
		}
		if len(result.Resources) != 1 || result.Resources[0].ResourceID != "kn-a/m-1" {
			t.Fatalf("FilterResources() = %#v", result)
		}
	})

	t.Run("rejects an omitted resources field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()

		access := &permissionAccess{
			baseURL:    server.URL,
			httpClient: rest.NewHTTPClientWithOptions(rest.HttpClientOptions{TimeOut: 1}),
		}
		if _, err := access.FilterResources(context.Background(), interfaces.PermissionFilterRequest{}); err == nil {
			t.Fatal("FilterResources() error = nil")
		}
	})

	t.Run("rejects an invalid base url before making a request", func(t *testing.T) {
		access := &permissionAccess{baseURL: "not-a-url", httpClient: rest.NewHTTPClient()}
		if _, err := access.FilterResources(context.Background(), interfaces.PermissionFilterRequest{}); err == nil {
			t.Fatal("FilterResources() error = nil")
		}
	})
}
