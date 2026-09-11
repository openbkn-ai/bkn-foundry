// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"encoding/json"
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
			var request map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if _, exists := request["evaluation_scope"]; exists {
				t.Fatalf("ontology-query selected a non-default evaluation scope: %#v", request)
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

func TestPermissionAccessResolvePropertyLevels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/safe/v1/authz/property-levels" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[{"object_type_ref":"kn-1/customer","properties":[{"name":"mobile","level":"masked","source":"property"}]}]}`))
	}))
	defer server.Close()
	access := &permissionAccess{
		baseURL:    server.URL,
		httpClient: rest.NewHTTPClientWithOptions(rest.HttpClientOptions{TimeOut: 1}),
	}
	response, err := access.ResolvePropertyLevels(context.Background(), interfaces.PropertyLevelsRequest{
		AccessorID: "user-1",
		Items:      []interfaces.PropertyLevelsRequestItem{{ObjectTypeRef: "kn-1/customer", Properties: []string{"mobile"}}},
	})
	if err != nil || len(response.Entries) != 1 || response.Entries[0].Properties[0].Level != interfaces.PropertyAccessMasked {
		t.Fatalf("ResolvePropertyLevels() = %#v, %v", response, err)
	}
}
