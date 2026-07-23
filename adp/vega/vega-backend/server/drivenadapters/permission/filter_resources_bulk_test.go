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
	"sync/atomic"
	"testing"

	"vega-backend/interfaces"
)

// bknSafeStub mocks the two bkn-safe authz endpoints the adapter uses and counts
// the round-trips, so tests can assert they do NOT scale with the resource count.
type bknSafeStub struct {
	wildcard   bool     // obj="*" check result
	accessible []string // ids returned by /authz/resources
	checkCalls atomic.Int32
	listCalls  atomic.Int32
}

func (b *bknSafeStub) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/safe/v1/authz/check":
			b.checkCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": b.wildcard})
		case r.Method == http.MethodGet && r.URL.Path == "/api/safe/v1/authz/resources":
			b.listCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string][]string{"ids": b.accessible})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func resourcesOfType(n int, rtype string) []interfaces.PermissionResource {
	out := make([]interfaces.PermissionResource, 0, n)
	for i := range n {
		out = append(out, interfaces.PermissionResource{ID: fmt.Sprintf("r%d", i), Type: rtype})
	}
	return out
}

// The adapter must resolve authorization in bulk: round-trips depend on the
// number of operations and resource types, never on how many resources are being
// filtered. Filtering per resource is what made large catalogs time out (#357).
func TestSafeFilterResourcesIsBulk(t *testing.T) {
	const op = interfaces.OPERATION_TYPE_VIEW_DETAIL
	ctx := context.Background()

	t.Run("concrete grants: one check + one id-set fetch for any resource count", func(t *testing.T) {
		stub := &bknSafeStub{wildcard: false, accessible: []string{"r1", "r5"}}
		srv := stub.server()
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.FilterResources(ctx, interfaces.PermissionResourcesFilter{
			Accessor:   interfaces.PermissionAccessor{ID: "acc", Type: interfaces.ACCESSOR_TYPE_USER},
			Resources:  resourcesOfType(100, interfaces.AUTH_RESOURCE_TYPE_RESOURCE),
			Operations: []string{op},
		})
		if err != nil {
			t.Fatal(err)
		}
		// Only the granted ids come back, each carrying the requested op.
		if len(got) != 2 || got["r1"].Operations[0] != op || got["r5"].Operations[0] != op {
			t.Fatalf("want only r1,r5 with [%s]; got %+v", op, got)
		}
		// One op, one resource type -> one wildcard probe + one id-set fetch,
		// independent of the 100 resources filtered.
		if c, l := stub.checkCalls.Load(), stub.listCalls.Load(); c != 1 || l != 1 {
			t.Fatalf("round-trips must not scale with resource count: checks=%d lists=%d", c, l)
		}
	})

	t.Run("wildcard grant: everything passes without fetching the id set", func(t *testing.T) {
		stub := &bknSafeStub{wildcard: true, accessible: []string{"unused"}}
		srv := stub.server()
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.FilterResources(ctx, interfaces.PermissionResourcesFilter{
			Accessor:   interfaces.PermissionAccessor{ID: "acc", Type: interfaces.ACCESSOR_TYPE_USER},
			Resources:  resourcesOfType(100, interfaces.AUTH_RESOURCE_TYPE_RESOURCE),
			Operations: []string{op},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 100 {
			t.Fatalf("wildcard grant must pass every resource, got %d", len(got))
		}
		if c, l := stub.checkCalls.Load(), stub.listCalls.Load(); c != 1 || l != 0 {
			t.Fatalf("wildcard: want 1 check and no id-set fetch, got checks=%d lists=%d", c, l)
		}
	})

	// GetResourcesOperations keeps every requested resource (even with no ops),
	// unlike FilterResources which drops the unauthorized ones.
	t.Run("GetResourcesOperations keeps unauthorized resources with empty ops", func(t *testing.T) {
		stub := &bknSafeStub{wildcard: false, accessible: []string{"r1"}}
		srv := stub.server()
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.GetResourcesOperations(ctx, interfaces.PermissionResourcesFilter{
			Accessor:   interfaces.PermissionAccessor{ID: "acc", Type: interfaces.ACCESSOR_TYPE_USER},
			Resources:  resourcesOfType(3, interfaces.AUTH_RESOURCE_TYPE_RESOURCE),
			Operations: []string{op},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("want all 3 resources present, got %d", len(got))
		}
		if len(got["r1"].Operations) != 1 || len(got["r0"].Operations) != 0 {
			t.Fatalf("want r1 granted and r0 empty, got %+v", got)
		}
	})
}
