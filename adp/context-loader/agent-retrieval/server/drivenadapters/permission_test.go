// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	infrarest "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestPermissionAccessFilterResources(t *testing.T) {
	var got interfaces.PermissionFilterRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/safe/v1/authz/resource-filter" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[{"resource_type":"object_type","resource_id":"kn-1/ot-1","operations":["query_data"]}]}`))
	}))
	defer server.Close()

	access := &permissionAccess{
		baseURL:    server.URL,
		httpClient: infrarest.NewHTTPClientWithRawClient(server.Client()),
	}
	request := interfaces.PermissionFilterRequest{
		AccessorID: "user-1",
		Resources: []interfaces.PermissionResource{{
			Type: interfaces.PermissionResourceTypeObjectType,
			ID:   "kn-1/ot-1",
		}},
		VisibilityOperations: []string{interfaces.PermissionOperationQueryData},
		CandidateOperations:  []string{interfaces.PermissionOperationQueryData},
	}
	response, err := access.FilterResources(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessorID != request.AccessorID || len(got.Resources) != 1 {
		t.Fatalf("request = %#v", got)
	}
	if response.Resources == nil || len(*response.Resources) != 1 {
		t.Fatalf("response = %#v", response)
	}
}

func TestPermissionAccessRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non-200", status: http.StatusServiceUnavailable, body: `{}`},
		{name: "empty body", status: http.StatusOK, body: ``},
		{name: "invalid json", status: http.StatusOK, body: `{`},
		{name: "missing resources", status: http.StatusOK, body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			access := &permissionAccess{baseURL: server.URL, httpClient: infrarest.NewHTTPClientWithRawClient(server.Client())}
			if _, err := access.FilterResources(context.Background(), interfaces.PermissionFilterRequest{}); err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}
