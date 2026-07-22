// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"vega-backend/interfaces"
)

// TestSafeAccessibleResourceIDs locks the two bkn-safe contract paths of
// safePermissionAccess.AccessibleResourceIDs:
//   - a type-wide/wildcard grant is detected by a single obj="*" check and
//     yields All=true WITHOUT fetching the concrete id set;
//   - otherwise the concrete id set is fetched and loaded into IDs.
func TestSafeAccessibleResourceIDs(t *testing.T) {
	const op = interfaces.OPERATION_TYPE_VIEW_DETAIL

	t.Run("wildcard grant -> All=true, no id-set fetch", func(t *testing.T) {
		var resourceCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/safe/v1/authz/check":
				// The obj="*" probe returns allowed -> type-wide grant.
				var body struct {
					Resource struct {
						ID string `json:"id"`
					} `json:"resource"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": body.Resource.ID == "*"})
			case r.Method == http.MethodGet && r.URL.Path == "/api/safe/v1/authz/resources":
				resourceCalls.Add(1)
				_ = json.NewEncoder(w).Encode(map[string][]string{"ids": {"should-not-be-used"}})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.AccessibleResourceIDs(context.Background(), "acc", "resource", []string{op})
		if err != nil {
			t.Fatal(err)
		}
		if a := got[op]; !a.All || a.IDs != nil {
			t.Fatalf("wildcard: want All=true and nil IDs, got %+v", a)
		}
		if n := resourceCalls.Load(); n != 0 {
			t.Fatalf("wildcard hit must not fetch the id set, but it was called %d times", n)
		}
	})

	t.Run("no wildcard -> fetch concrete id set", func(t *testing.T) {
		var resourceCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/safe/v1/authz/check":
				_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": false})
			case r.Method == http.MethodGet && r.URL.Path == "/api/safe/v1/authz/resources":
				resourceCalls.Add(1)
				_ = json.NewEncoder(w).Encode(map[string][]string{"ids": {"r1", "r2"}})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		s := &safePermissionAccess{safe: newSafeClient(srv.URL)}
		got, err := s.AccessibleResourceIDs(context.Background(), "acc", "resource", []string{op})
		if err != nil {
			t.Fatal(err)
		}
		a := got[op]
		if a.All || len(a.IDs) != 2 || !a.IDs["r1"] || !a.IDs["r2"] {
			t.Fatalf("non-wildcard: want All=false and IDs{r1,r2}, got %+v", a)
		}
		if n := resourceCalls.Load(); n != 1 {
			t.Fatalf("non-wildcard must fetch the id set exactly once, got %d", n)
		}
	})
}
