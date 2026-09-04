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

	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// fakeAuthz serves the bkn-safe authz endpoints the adapter uses.
func fakeAuthz(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/authz/check", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccessorID string `json:"accessor_id"`
			Resource   struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"resource"`
			Operation string `json:"operation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		allowed := req.AccessorID == "admin" &&
			req.Resource.Type == "skill" &&
			req.Resource.ID == interfaces.ResourceIDAll &&
			req.Operation == "view"
		_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed})
	})
	mux.HandleFunc("/api/safe/v1/authz/resources", func(w http.ResponseWriter, r *http.Request) {
		accessorID := r.URL.Query().Get("accessor_id")
		rtype := r.URL.Query().Get("resource_type")
		op := r.URL.Query().Get("operation")
		var ids []string
		switch {
		case accessorID == "u1" && rtype == "skill" && op == "view":
			ids = []string{"s1", "s2"}
		case accessorID == "u1" && rtype == "skill" && op == "modify":
			ids = []string{"s1", "s3"}
		case accessorID == "u2" && rtype == "skill" && op == "view":
			ids = []string{"s9"}
		default:
			ids = []string{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ids": ids})
	})
	mux.HandleFunc("/api/safe/v1/authz/resource-filter", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccessorID string `json:"accessor_id"`
			Resources  []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"resources"`
			VisibilityOperations []string `json:"visibility_operations"`
			CandidateOperations  []string `json:"candidate_operations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.AccessorID != "u1" || len(req.Resources) != 2 || req.Resources[0].Type != "skill" || req.Resources[0].ID != "s1" ||
			len(req.VisibilityOperations) != 1 || req.VisibilityOperations[0] != "view" ||
			len(req.CandidateOperations) != 1 || req.CandidateOperations[0] != "authorize" {
			http.Error(w, "unexpected resource filter request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resources": []map[string]any{
			{"resource_id": "s1", "resource_type": "skill", "operations": []string{"authorize"}},
			{"resource_id": "s2", "resource_type": "skill", "operations": []string{}},
		}})
	})
	return httptest.NewServer(mux)
}

func TestSafeAuthorizationResourceFilterProjectsCandidateOperations(t *testing.T) {
	srv := fakeAuthz(t)
	defer srv.Close()

	resources, err := newSafeAuthorization(srv.URL, testLogger{}).ResourceFilter(context.Background(), &interfaces.AuthResourceFilterRequest{
		Accessor: &interfaces.AuthAccessor{ID: "u1"},
		Resources: []*interfaces.AuthResource{
			{ID: "s1", Type: "skill"},
			{ID: "s2", Type: "skill"},
		},
		Operations:          []interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
		CandidateOperations: []interfaces.AuthOperationType{interfaces.AuthOperationTypeAuthorize},
	})
	if err != nil {
		t.Fatalf("ResourceFilter: %v", err)
	}
	if len(resources) != 2 || resources[0].ID != "s1" || resources[0].Type != "skill" ||
		len(resources[0].Operations) != 1 || resources[0].Operations[0] != interfaces.AuthOperationTypeAuthorize ||
		len(resources[1].Operations) != 0 {
		t.Fatalf("ResourceFilter = %+v, want authorize only for s1", resources)
	}
}

func TestSafeAuthorizationResourceFilterRejectsMissingResources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	resources, err := newSafeAuthorization(srv.URL, testLogger{}).ResourceFilter(context.Background(), &interfaces.AuthResourceFilterRequest{
		Accessor:   &interfaces.AuthAccessor{ID: "u1"},
		Resources:  []*interfaces.AuthResource{{ID: "s1", Type: "skill"}},
		Operations: []interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
	})
	if err == nil {
		t.Fatalf("ResourceFilter = %+v, want an invalid response error", resources)
	}
}

func TestSafeAuthorizationResourceList(t *testing.T) {
	srv := fakeAuthz(t)
	defer srv.Close()
	ctx := context.Background()
	s := newSafeAuthorization(srv.URL, testLogger{})

	t.Run("single operation returns accessible IDs", func(t *testing.T) {
		res, err := s.ResourceList(ctx, &interfaces.ResourceListRequest{
			Accessor:  &interfaces.AuthAccessor{ID: "u1"},
			Resource:  &interfaces.AuthResource{Type: "skill"},
			Operation: []interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
		})
		if err != nil {
			t.Fatalf("ResourceList: %v", err)
		}
		if len(res) != 2 || res[0].ID != "s1" || res[1].ID != "s2" {
			t.Fatalf("ResourceList = %+v, want [s1 s2]", res)
		}
	})

	t.Run("multi operation intersects IDs", func(t *testing.T) {
		res, err := s.ResourceList(ctx, &interfaces.ResourceListRequest{
			Accessor: &interfaces.AuthAccessor{ID: "u1"},
			Resource: &interfaces.AuthResource{Type: "skill"},
			Operation: []interfaces.AuthOperationType{
				interfaces.AuthOperationTypeView,
				interfaces.AuthOperationTypeModify,
			},
		})
		if err != nil {
			t.Fatalf("ResourceList: %v", err)
		}
		if len(res) != 1 || res[0].ID != "s1" {
			t.Fatalf("ResourceList = %+v, want [s1]", res)
		}
	})

	t.Run("type-wide grant returns ResourceIDAll", func(t *testing.T) {
		res, err := s.ResourceList(ctx, &interfaces.ResourceListRequest{
			Accessor:  &interfaces.AuthAccessor{ID: "admin"},
			Resource:  &interfaces.AuthResource{Type: "skill"},
			Operation: []interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
		})
		if err != nil {
			t.Fatalf("ResourceList: %v", err)
		}
		if len(res) != 1 || res[0].ID != interfaces.ResourceIDAll {
			t.Fatalf("ResourceList = %+v, want [*]", res)
		}
	})

	t.Run("empty operations returns empty", func(t *testing.T) {
		res, err := s.ResourceList(ctx, &interfaces.ResourceListRequest{
			Accessor: &interfaces.AuthAccessor{ID: "u1"},
			Resource: &interfaces.AuthResource{Type: "skill"},
		})
		if err != nil {
			t.Fatalf("ResourceList: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("ResourceList = %+v, want []", res)
		}
	})
}

func TestSafeAuthorizationUsesEffectiveLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(sharedrest.AcceptLanguageHeader); got != sharedrest.AmericanEnglish {
			t.Errorf("Accept-Language = %q, want %q", got, sharedrest.AmericanEnglish)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": true})
	}))
	defer server.Close()

	safe := newSafeAuthorization(server.URL, testLogger{})
	ctx := sharedrest.WithLanguage(context.Background(), sharedrest.AmericanEnglish)
	result, err := safe.OperationCheck(ctx, &interfaces.AuthOperationCheckRequest{
		Accessor:  &interfaces.AuthAccessor{ID: "user-1"},
		Resource:  &interfaces.AuthResource{Type: "skill", ID: "skill-1"},
		Operation: []interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
	})
	if err != nil || !result.Result {
		t.Fatalf("OperationCheck() = %#v, %v", result, err)
	}
}

func TestSafeAuthorizationRejectsIncompleteCheckResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()

	safe := newSafeAuthorization(server.URL, testLogger{})
	result, err := safe.OperationCheck(context.Background(), &interfaces.AuthOperationCheckRequest{
		Accessor:  &interfaces.AuthAccessor{ID: "user-1"},
		Resource:  &interfaces.AuthResource{Type: "skill", ID: "skill-1"},
		Operation: []interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
	})
	if err == nil || result != nil {
		t.Fatalf("OperationCheck() = %#v, %v; want incomplete response error", result, err)
	}
}

func TestNormalizeBknSafeURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "http", raw: "http://bkn-safe:3000/", want: "http://bkn-safe:3000"},
		{name: "https", raw: " https://safe.example/ ", want: "https://safe.example"},
		{name: "empty", wantErr: true},
		{name: "relative", raw: "bkn-safe:3000", wantErr: true},
		{name: "unsupported scheme", raw: "ftp://bkn-safe", wantErr: true},
		{name: "query", raw: "http://bkn-safe:3000?token=secret", wantErr: true},
		{name: "userinfo", raw: "http://user:secret@bkn-safe:3000", wantErr: true},
		{name: "path", raw: "https://safe.example/internal", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeBknSafeURL(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeBknSafeURL(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeBknSafeURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestIntersectStringSlices(t *testing.T) {
	got := intersectStringSlices([]string{"a", "b", "c"}, []string{"b", "c", "d"})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("intersect = %v, want [b c]", got)
	}
	if len(intersectStringSlices(nil, []string{"a"})) != 0 {
		t.Fatal("expected empty intersection when one side is empty")
	}
}
