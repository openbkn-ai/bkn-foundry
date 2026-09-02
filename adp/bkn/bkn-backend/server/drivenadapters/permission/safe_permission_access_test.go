// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"bkn-backend/interfaces"
)

// capturedFilter is the decoded /resource-filter request body.
type capturedFilter struct {
	AccessorID string `json:"accessor_id"`
	Resources  []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"resources"`
	VisibilityOperations []string `json:"visibility_operations"`
	CandidateOperations  []string `json:"candidate_operations"`
}

// newFilterStub stands in for bkn-safe: it records every request it receives and
// replies with the given resource -> operations mapping.
func newFilterStub(t *testing.T, reply map[string][]string) (*safePermissionAccess, *[]capturedFilter) {
	t.Helper()
	calls := &[]capturedFilter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/safe/v1/authz/resource-filter" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var got capturedFilter
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode request: %v (%s)", err, body)
		}
		*calls = append(*calls, got)

		entries := make([]map[string]any, 0, len(reply))
		for id, ops := range reply {
			entries = append(entries, map[string]any{
				"resource_type": "knowledge_network",
				"resource_id":   id,
				"operations":    ops,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resources": entries})
	}))
	t.Cleanup(srv.Close)
	return &safePermissionAccess{safe: newSafeClient(srv.URL)}, calls
}

func knFilter(ids []string, ops, candidates []string) interfaces.PermissionResourcesFilter {
	resources := make([]interfaces.PermissionResource, 0, len(ids))
	for _, id := range ids {
		resources = append(resources, interfaces.PermissionResource{Type: "knowledge_network", ID: id})
	}
	return interfaces.PermissionResourcesFilter{
		Accessor:            interfaces.PermissionAccessor{ID: "u-1", Type: "user"},
		Resources:           resources,
		Operations:          ops,
		CandidateOperations: candidates,
		AllowOperation:      true,
	}
}

func TestNewPermissionAccessUsesBknSafe(t *testing.T) {
	access, ok := NewPermissionAccess("  http://bkn-safe:3000/  ").(*safePermissionAccess)
	if !ok {
		t.Fatalf("NewPermissionAccess() returned %T, want *safePermissionAccess", access)
	}
	if access.safe.baseURL != "http://bkn-safe:3000" {
		t.Fatalf("safe base URL = %q, want normalized bkn-safe URL", access.safe.baseURL)
	}
}

func TestSafeResourceParentRequests(t *testing.T) {
	type capturedRequest struct {
		method string
		body   map[string]any
	}
	requests := make([]capturedRequest, 0, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/safe/v1/authz/resource-parents" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, capturedRequest{method: r.Method, body: body})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := newSafeClient(srv.URL)
	items := []interfaces.PermissionResourceParent{{ResourceID: "kn-1/ot-1", ParentID: "kn-1"}}
	if err := client.upsertResourceParents(context.Background(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		interfaces.RESOURCE_TYPE_KN, items); err != nil {
		t.Fatalf("upsertResourceParents() error = %v", err)
	}
	if err := client.deleteResourceParents(context.Background(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/ot-1"}); err != nil {
		t.Fatalf("deleteResourceParents() error = %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if requests[0].method != http.MethodPut {
		t.Fatalf("upsert method = %s, want PUT", requests[0].method)
	}
	if requests[0].body["resource_type"] != interfaces.RESOURCE_TYPE_OBJECT_TYPE ||
		requests[0].body["parent_type"] != interfaces.RESOURCE_TYPE_KN {
		t.Fatalf("unexpected upsert body: %#v", requests[0].body)
	}
	wantItems := []any{map[string]any{"resource_id": "kn-1/ot-1", "parent_id": "kn-1"}}
	if !reflect.DeepEqual(requests[0].body["items"], wantItems) {
		t.Fatalf("upsert items = %#v, want %#v", requests[0].body["items"], wantItems)
	}
	if requests[1].method != http.MethodDelete {
		t.Fatalf("delete method = %s, want DELETE", requests[1].method)
	}
	if requests[1].body["resource_type"] != interfaces.RESOURCE_TYPE_OBJECT_TYPE {
		t.Fatalf("unexpected delete body: %#v", requests[1].body)
	}
	wantResourceIDs := []any{"kn-1/ot-1"}
	if !reflect.DeepEqual(requests[1].body["resource_ids"], wantResourceIDs) {
		t.Fatalf("delete resource IDs = %#v, want %#v", requests[1].body["resource_ids"], wantResourceIDs)
	}
}

func TestSafeResourceParentEmptyInputsDoNotCallServer(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := newSafeClient(srv.URL)
	if err := client.upsertResourceParents(context.Background(), "type", "parent", nil); err != nil {
		t.Fatal(err)
	}
	if err := client.deleteResourceParents(context.Background(), "type", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("empty inputs made %d requests, want 0", calls)
	}
}

func TestSafeResourceParentErrorsPropagate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"safe unavailable"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	access := NewPermissionAccess(srv.URL)
	items := []interfaces.PermissionResourceParent{{ResourceID: "kn-1/ot-1", ParentID: "kn-1"}}
	if err := access.UpsertResourceParents(context.Background(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		interfaces.RESOURCE_TYPE_KN, items); err == nil {
		t.Fatal("UpsertResourceParents() error = nil, want bkn-safe failure")
	}
	if err := access.DeleteResourceParents(context.Background(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/ot-1"}); err == nil {
		t.Fatal("DeleteResourceParents() error = nil, want bkn-safe failure")
	}
}

// TestSafeFilterResourcesIsOneCall pins the two properties the batch endpoint
// exists for: a page costs one round trip regardless of size, and the candidate
// operations reach bkn-safe instead of collapsing to the visibility op.
func TestSafeFilterResourcesIsOneCall(t *testing.T) {
	candidates := []string{"view_detail", "modify", "delete"}
	access, calls := newFilterStub(t, map[string][]string{
		"kn-1": {"view_detail", "modify", "delete"},
		"kn-2": {"view_detail"},
	})

	got, err := access.FilterResources(context.Background(),
		knFilter([]string{"kn-1", "kn-2", "kn-3"}, []string{"view_detail"}, candidates))
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("made %d requests, want exactly 1", len(*calls))
	}
	call := (*calls)[0]
	if call.AccessorID != "u-1" || len(call.Resources) != 3 {
		t.Errorf("request = %+v", call)
	}
	if !reflect.DeepEqual(call.VisibilityOperations, []string{"view_detail"}) {
		t.Errorf("visibility = %v", call.VisibilityOperations)
	}
	if !reflect.DeepEqual(call.CandidateOperations, candidates) {
		t.Errorf("candidates = %v, want %v", call.CandidateOperations, candidates)
	}

	// kn-3 is absent from the reply: not visible, so it must not appear.
	if len(got) != 2 {
		t.Fatalf("got %v, want kn-1 and kn-2", got)
	}
	if want := []string{"view_detail", "modify", "delete"}; !reflect.DeepEqual(got["kn-1"].Operations, want) {
		t.Errorf("kn-1 ops = %v, want %v", got["kn-1"].Operations, want)
	}
	if got["kn-1"].ResourceID != "kn-1" {
		t.Errorf("resource id = %q", got["kn-1"].ResourceID)
	}
}

// TestSafeFilterResourcesCandidateFallback covers callers that pass no candidate
// set: the operations they filtered on stay the ones projected back, which is
// the pre-existing behaviour.
func TestSafeFilterResourcesCandidateFallback(t *testing.T) {
	access, calls := newFilterStub(t, map[string][]string{"kn-1": {"view_detail"}})

	if _, err := access.FilterResources(context.Background(),
		knFilter([]string{"kn-1"}, []string{"view_detail"}, nil)); err != nil {
		t.Fatalf("filter: %v", err)
	}
	if want := []string{"view_detail"}; !reflect.DeepEqual((*calls)[0].CandidateOperations, want) {
		t.Errorf("candidates = %v, want %v", (*calls)[0].CandidateOperations, want)
	}
}

// TestSafeGetResourcesOperationsHasNoVisibilityFilter checks the other adapter
// method keeps its "every requested resource comes back" contract, which the
// endpoint expresses as an empty visibility list.
func TestSafeGetResourcesOperationsHasNoVisibilityFilter(t *testing.T) {
	access, calls := newFilterStub(t, map[string][]string{"kn-1": {"view_detail"}, "kn-2": {}})

	got, err := access.GetResourcesOperations(context.Background(),
		knFilter([]string{"kn-1", "kn-2"}, []string{"view_detail"}, []string{"view_detail", "modify"}))
	if err != nil {
		t.Fatalf("get operations: %v", err)
	}
	if len((*calls)[0].VisibilityOperations) != 0 {
		t.Errorf("visibility = %v, want empty", (*calls)[0].VisibilityOperations)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want both resources", got)
	}
	if len(got["kn-2"].Operations) != 0 {
		t.Errorf("kn-2 ops = %v, want empty", got["kn-2"].Operations)
	}
}

// TestSafeFilterResourcesEmptyBatch guards the empty page: no request at all.
func TestSafeFilterResourcesEmptyBatch(t *testing.T) {
	access, calls := newFilterStub(t, nil)

	got, err := access.FilterResources(context.Background(),
		knFilter(nil, []string{"view_detail"}, []string{"view_detail"}))
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 0 || len(*calls) != 0 {
		t.Fatalf("got %v after %d calls, want empty and no call", got, len(*calls))
	}
}

// TestSafeFilterResourcesPropagatesError checks a bkn-safe failure surfaces as an
// error rather than an empty (i.e. silently unauthorized) result.
func TestSafeFilterResourcesPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	access := &safePermissionAccess{safe: newSafeClient(srv.URL)}

	if _, err := access.FilterResources(context.Background(),
		knFilter([]string{"kn-1"}, []string{"view_detail"}, []string{"view_detail"})); err == nil {
		t.Fatal("want error from a failing bkn-safe, got nil")
	}
}
