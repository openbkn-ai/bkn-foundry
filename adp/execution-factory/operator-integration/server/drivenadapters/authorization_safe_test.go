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

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
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
	return httptest.NewServer(mux)
}

func TestSafeAuthorizationResourceList(t *testing.T) {
	srv := fakeAuthz(t)
	defer srv.Close()
	ctx := context.Background()
	s := newSafeAuthorization(srv.URL, testLogger{})

	t.Run("single operation returns accessible IDs", func(t *testing.T) {
		res, err := s.ResourceList(ctx, &interfaces.ResourceListRequest{
			Accessor: &interfaces.AuthAccessor{ID: "u1"},
			Resource: &interfaces.AuthResource{Type: "skill"},
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
			Accessor: &interfaces.AuthAccessor{ID: "admin"},
			Resource: &interfaces.AuthResource{Type: "skill"},
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

func TestIntersectStringSlices(t *testing.T) {
	got := intersectStringSlices([]string{"a", "b", "c"}, []string{"b", "c", "d"})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("intersect = %v, want [b c]", got)
	}
	if len(intersectStringSlices(nil, []string{"a"})) != 0 {
		t.Fatal("expected empty intersection when one side is empty")
	}
}
