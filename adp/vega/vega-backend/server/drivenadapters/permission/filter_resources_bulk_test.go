// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"vega-backend/interfaces"
)

// bknSafeStub mocks bkn-safe's effective resource filter and counts the
// round-trips, so tests can assert they do NOT scale with the resource count.
type bknSafeStub struct {
	allowAll          bool
	allowedIDs        []string
	catalogOperations []string
	filterCalls       atomic.Int32
}

func (b *bknSafeStub) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/safe/v1/authz/resource-filter" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b.filterCalls.Add(1)
		var req struct {
			Resources            []interfaces.PermissionResource `json:"resources"`
			VisibilityOperations []string                        `json:"visibility_operations"`
			IncludeOperations    bool                            `json:"include_operations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		allowedIDs := make(map[string]bool, len(b.allowedIDs))
		for _, id := range b.allowedIDs {
			allowedIDs[id] = true
		}
		resources := make([]map[string]any, 0, len(req.Resources))
		for _, resource := range req.Resources {
			allowed := b.allowAll || allowedIDs[resource.ID]
			if len(req.VisibilityOperations) > 0 && !allowed {
				continue
			}
			operations := []string{}
			if allowed && req.IncludeOperations {
				operations = append(operations, b.catalogOperations...)
			}
			resources = append(resources, map[string]any{
				"resource_type": resource.Type,
				"resource_id":   resource.ID,
				"operations":    operations,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resources": resources})
	}))
}

func resourcesOfType(n int, rtype string) []interfaces.PermissionResource {
	out := make([]interfaces.PermissionResource, 0, n)
	for i := range n {
		out = append(out, interfaces.PermissionResource{ID: fmt.Sprintf("r%d", i), Type: rtype})
	}
	return out
}

func TestSafeCheckPermissionBatchesOperations(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/safe/v1/authz/checks" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request struct {
			AccessorID string `json:"accessor_id"`
			Checks     []struct {
				Resource  interfaces.PermissionResource `json:"resource"`
				Operation string                        `json:"operation"`
			} `json:"checks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode checks request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.AccessorID != "u-1" || len(request.Checks) != 2 ||
			request.Checks[0].Operation != interfaces.OPERATION_TYPE_VIEW_DETAIL ||
			request.Checks[1].Operation != interfaces.OPERATION_TYPE_QUERY_DATA {
			t.Errorf("checks request = %+v", request)
		}
		_, _ = w.Write([]byte(`{"allowed":true,"evaluation_scope":"effective","results":[` +
			`{"resource_type":"object_type","resource_id":"kn-1/orders","operation":"view_detail","allowed":true},` +
			`{"resource_type":"object_type","resource_id":"kn-1/orders","operation":"query_data","allowed":true}]}`))
	}))
	t.Cleanup(srv.Close)

	access := &safePermissionAccess{safe: newSafeClient(srv.URL)}
	allowed, err := access.CheckPermission(context.Background(), interfaces.PermissionCheck{
		Accessor: interfaces.PermissionAccessor{ID: "u-1", Type: interfaces.ACCESSOR_TYPE_USER},
		Resource: interfaces.PermissionResource{Type: "object_type", ID: "kn-1/orders"},
		Operations: []string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_QUERY_DATA,
		},
	})
	if err != nil || !allowed {
		t.Fatalf("CheckPermission() = %v, %v; want allowed", allowed, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("CheckPermission() made %d requests, want one batched request", got)
	}
}

// The adapter must resolve authorization in bulk: round-trips depend on the
// number of operations and resource types, never on how many resources are being
// filtered. Filtering per resource is what made large catalogs time out (#357).
func TestSafeFilterResourcesIsBulk(t *testing.T) {
	const op = interfaces.OPERATION_TYPE_VIEW_DETAIL
	ctx := context.Background()

	t.Run("concrete grants: one effective filter for any resource count", func(t *testing.T) {
		catalogOperations := []string{op, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.OPERATION_TYPE_MODIFY}
		stub := &bknSafeStub{allowedIDs: []string{"r1", "r5"}, catalogOperations: catalogOperations}
		srv := stub.server()
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.FilterResources(ctx, interfaces.PermissionResourcesFilter{
			Accessor:          interfaces.PermissionAccessor{ID: "acc", Type: interfaces.ACCESSOR_TYPE_USER},
			Resources:         resourcesOfType(100, interfaces.AUTH_RESOURCE_TYPE_RESOURCE),
			Operations:        []string{op},
			IncludeOperations: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || !reflect.DeepEqual(got["r1"].Operations, catalogOperations) ||
			!reflect.DeepEqual(got["r5"].Operations, catalogOperations) {
			t.Fatalf("want only r1,r5 with %v; got %+v", catalogOperations, got)
		}
		if calls := stub.filterCalls.Load(); calls != 1 {
			t.Fatalf("round-trips must not scale with resource count: filters=%d", calls)
		}
	})

	t.Run("wildcard grant: everything passes in one effective filter", func(t *testing.T) {
		stub := &bknSafeStub{allowAll: true, catalogOperations: []string{op}}
		srv := stub.server()
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.FilterResources(ctx, interfaces.PermissionResourcesFilter{
			Accessor:          interfaces.PermissionAccessor{ID: "acc", Type: interfaces.ACCESSOR_TYPE_USER},
			Resources:         resourcesOfType(100, interfaces.AUTH_RESOURCE_TYPE_RESOURCE),
			Operations:        []string{op},
			IncludeOperations: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 100 {
			t.Fatalf("wildcard grant must pass every resource, got %d", len(got))
		}
		if calls := stub.filterCalls.Load(); calls != 1 {
			t.Fatalf("wildcard: want 1 effective filter, got %d", calls)
		}
	})

	t.Run("visibility-only filtering returns no operation projection", func(t *testing.T) {
		stub := &bknSafeStub{allowedIDs: []string{"r1"}, catalogOperations: []string{op, interfaces.OPERATION_TYPE_MODIFY}}
		srv := stub.server()
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.FilterResources(ctx, interfaces.PermissionResourcesFilter{
			Accessor:   interfaces.PermissionAccessor{ID: "acc", Type: interfaces.ACCESSOR_TYPE_USER},
			Resources:  resourcesOfType(3, interfaces.AUTH_RESOURCE_TYPE_RESOURCE),
			Operations: []string{op},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || len(got["r1"].Operations) != 0 {
			t.Fatalf("want only visible r1 with no operations; got %+v", got)
		}
	})

}

// TestFilterResourcesReportsCompleteOperations ensures a visible resource
// carries its complete effective operation set.
func TestFilterResourcesReportsCompleteOperations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/resource-filter") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			IncludeOperations bool `json:"include_operations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode resource-filter request: %v", err)
		}
		if !body.IncludeOperations {
			t.Error("resource-filter must request complete operation projection")
		}
		_, _ = w.Write([]byte(`{"resources":[{"resource_type":"resource","resource_id":"r-1","operations":["view_detail","query_data"]}]}`))
	}))
	defer srv.Close()

	pa := &safePermissionAccess{safe: newSafeClient(srv.URL)}
	got, err := pa.FilterResources(context.Background(), interfaces.PermissionResourcesFilter{
		Accessor:          interfaces.PermissionAccessor{ID: "u-1", Type: "user"},
		Resources:         []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "r-1"}},
		Operations:        []string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
		IncludeOperations: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry, ok := got["r-1"]
	if !ok {
		t.Fatal("看得见的表应该在结果里")
	}
	want := []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA}
	if !reflect.DeepEqual(entry.Operations, want) {
		t.Fatalf("operations = %v, want %v(候选集里持有的那些,不含没授的 delete)", entry.Operations, want)
	}
}

// A type-wide operation grant can depend on a prerequisite held only on one
// concrete resource. The final answer therefore cannot be inferred by checking
// type:* and enumerating concrete grants independently; bkn-safe must evaluate
// the supplied resource as one effective decision.
func TestFilterResourcesCombinesWildcardOperationWithConcreteRequirement(t *testing.T) {
	var filterCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/safe/v1/authz/checks":
			// modify on connector_type:* is denied because view_detail exists only
			// on remote-api, not on the wildcard pseudo-resource.
			_, _ = w.Write([]byte(`{"allowed":false}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/safe/v1/authz/resources":
			if r.URL.Query().Get("operation") == interfaces.OPERATION_TYPE_VIEW_DETAIL {
				_, _ = w.Write([]byte(`{"ids":["remote-api"]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ids":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/safe/v1/authz/resource-filter":
			filterCalls.Add(1)
			var body struct {
				AccessorID           string                          `json:"accessor_id"`
				Resources            []interfaces.PermissionResource `json:"resources"`
				VisibilityOperations []string                        `json:"visibility_operations"`
				IncludeOperations    bool                            `json:"include_operations"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode resource-filter request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body.AccessorID != "legacy-user" ||
				!reflect.DeepEqual(body.Resources, []interfaces.PermissionResource{{Type: "connector_type", ID: "remote-api"}}) ||
				!reflect.DeepEqual(body.VisibilityOperations, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}) ||
				!body.IncludeOperations {
				t.Errorf("resource-filter request = %+v", body)
			}
			_, _ = w.Write([]byte(`{"resources":[{"resource_type":"connector_type","resource_id":"remote-api","operations":["view_detail","modify","delete","authorize"]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	pa := &safePermissionAccess{safe: newSafeClient(srv.URL)}
	got, err := pa.FilterResources(context.Background(), interfaces.PermissionResourcesFilter{
		Accessor:  interfaces.PermissionAccessor{ID: "legacy-user", Type: interfaces.ACCESSOR_TYPE_USER},
		Resources: []interfaces.PermissionResource{{Type: "connector_type", ID: "remote-api"}},
		Operations: []string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
		},
		IncludeOperations: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		interfaces.OPERATION_TYPE_VIEW_DETAIL,
		interfaces.OPERATION_TYPE_MODIFY,
		interfaces.OPERATION_TYPE_DELETE,
		interfaces.OPERATION_TYPE_AUTHORIZE,
	}
	if !reflect.DeepEqual(got["remote-api"].Operations, want) {
		t.Fatalf("operations = %v, want %v", got["remote-api"].Operations, want)
	}
	if calls := filterCalls.Load(); calls != 1 {
		t.Fatalf("resource-filter calls = %d, want 1", calls)
	}
}
