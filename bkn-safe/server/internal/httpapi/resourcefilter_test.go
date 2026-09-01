// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
)

type filterEntry struct {
	ResourceType string   `json:"resource_type"`
	ResourceID   string   `json:"resource_id"`
	Operations   []string `json:"operations"`
}

func postFilter(t *testing.T, r *gin.Engine, body any) []filterEntry {
	t.Helper()
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", body)
	if w.Code != http.StatusOK {
		t.Fatalf("resource-filter = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Resources []filterEntry `json:"resources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return resp.Resources
}

// TestResourceFilterEndpoint is the contract this endpoint exists for: one call
// per list page returns the visible resources with their full operation set.
func TestResourceFilterEndpoint(t *testing.T) {
	r, e, db := newTestServer(t)
	const user, role = "u-1", "role-builder"
	seedEnabledUser(t, db, user)
	_ = e.GrantRolePermission(role, "knowledge_network", "*", "view_detail")
	_ = e.AssignRole(user, role)
	_ = e.GrantObjectPermission(user, "knowledge_network", "kn-1", "modify")
	_ = e.GrantObjectPermission(user, "knowledge_network", "kn-1", "delete")

	got := postFilter(t, r, map[string]any{
		"accessor_id":           user,
		"resource_type":         "knowledge_network",
		"resource_ids":          []string{"kn-1", "kn-2"},
		"visibility_operations": []string{"view_detail"},
		"candidate_operations":  []string{"view_detail", "create", "modify", "delete"},
	})

	if len(got) != 2 {
		t.Fatalf("got %v, want 2 entries", got)
	}
	byID := map[string][]string{}
	for _, r := range got {
		if r.ResourceType != "knowledge_network" {
			t.Errorf("resource_type = %q", r.ResourceType)
		}
		byID[r.ResourceID] = r.Operations
	}
	if want := []string{"view_detail", "modify", "delete"}; !reflect.DeepEqual(byID["kn-1"], want) {
		t.Errorf("kn-1 = %v, want %v", byID["kn-1"], want)
	}
	if want := []string{"view_detail"}; !reflect.DeepEqual(byID["kn-2"], want) {
		t.Errorf("kn-2 = %v, want %v", byID["kn-2"], want)
	}
}

// TestResourceFilterEndpointMixedTypes covers the resources[] form with more
// than one type in a single request.
func TestResourceFilterEndpointMixedTypes(t *testing.T) {
	r, e, db := newTestServer(t)
	const user = "u-1"
	seedEnabledUser(t, db, user)
	_ = e.GrantObjectPermission(user, "knowledge_network", "kn-1", "view_detail")
	_ = e.GrantObjectPermission(user, "resource", "r-1", "view_detail")
	_ = e.GrantObjectPermission(user, "resource", "r-1", "modify")

	got := postFilter(t, r, map[string]any{
		"accessor_id": user,
		"resources": []map[string]string{
			{"type": "knowledge_network", "id": "kn-1"},
			{"type": "resource", "id": "r-1"},
			{"type": "resource", "id": "r-2"},
		},
		"visibility_operations": []string{"view_detail"},
		"candidate_operations":  []string{"view_detail", "modify"},
	})

	if len(got) != 2 {
		t.Fatalf("got %v, want kn-1 and r-1", got)
	}
	for _, entry := range got {
		if entry.ResourceType == "resource" && entry.ResourceID != "r-1" {
			t.Errorf("unexpected entry %v", entry)
		}
	}
}

// TestResourceFilterEndpointSuperAdmin pins the reported regression: a wildcard
// accessor must get the whole candidate set back, not just the visibility op.
func TestResourceFilterEndpointSuperAdmin(t *testing.T) {
	r, e, db := newTestServer(t)
	const admin, role = "admin-1", "role-super"
	seedEnabledUser(t, db, admin)
	_ = e.Grant(role, "*", "*")
	_ = e.AssignRole(admin, role)

	candidates := []string{"view_detail", "create", "modify", "delete", "query_data", "authorize", "task_manage"}
	got := postFilter(t, r, map[string]any{
		"accessor_id":           admin,
		"resource_type":         "knowledge_network",
		"resource_ids":          []string{"kn-1"},
		"visibility_operations": []string{"view_detail"},
		"candidate_operations":  candidates,
	})
	if len(got) != 1 {
		t.Fatalf("got %v, want 1 entry", got)
	}
	if !reflect.DeepEqual(got[0].Operations, candidates) {
		t.Errorf("ops = %v, want %v", got[0].Operations, candidates)
	}
}

// TestResourceFilterEndpointEdges covers the empty page, the missing accessor
// and the malformed single-type form.
func TestResourceFilterEndpointEdges(t *testing.T) {
	r, e, db := newTestServer(t)
	seedEnabledUser(t, db, "u-1")
	seedCatalogOps(t, db, "agent", "use")
	if err := e.GrantObjectPermission("u-1", "agent", "a-1", "use"); err != nil {
		t.Fatal(err)
	}

	t.Run("empty resource list is not an error", func(t *testing.T) {
		got := postFilter(t, r, map[string]any{
			"accessor_id":           "u-1",
			"visibility_operations": []string{"view_detail"},
			"candidate_operations":  []string{"view_detail"},
		})
		if len(got) != 0 {
			t.Fatalf("got %v, want empty", got)
		}
	})

	t.Run("missing accessor_id is rejected", func(t *testing.T) {
		w := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
			"resource_type": "knowledge_network",
			"resource_ids":  []string{"kn-1"},
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("resource_ids without resource_type is rejected", func(t *testing.T) {
		w := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
			"accessor_id":  "u-1",
			"resource_ids": []string{"kn-1"},
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("empty operation is not treated as omission", func(t *testing.T) {
		got := postFilter(t, r, map[string]any{
			"accessor_id":          "u-1",
			"resources":            []map[string]string{{"type": "agent", "id": "a-1"}},
			"candidate_operations": []string{""},
		})
		if len(got) != 1 || len(got[0].Operations) != 0 {
			t.Fatalf("empty candidate result = %v, want one resource with no operations", got)
		}

		got = postFilter(t, r, map[string]any{
			"accessor_id":           "u-1",
			"resources":             []map[string]string{{"type": "agent", "id": "a-1"}},
			"visibility_operations": []string{""},
			"candidate_operations":  []string{"use"},
		})
		if len(got) != 0 {
			t.Fatalf("empty visibility operation result = %v, want empty", got)
		}
	})
}

// TestResourceFilterEndpointCatalogFallback checks that omitting
// candidate_operations projects the resource type's catalog ops, matching what
// POST /operations returns for the same resource.
func TestResourceFilterEndpointCatalogFallback(t *testing.T) {
	r, e, db := newTestServer(t)
	const user = "u-1"
	seedEnabledUser(t, db, user)
	seedCatalogOps(t, db, "knowledge_network", "view_detail", "modify")
	_ = e.GrantObjectPermission(user, "knowledge_network", "kn-1", "view_detail")

	got := postFilter(t, r, map[string]any{
		"accessor_id":           user,
		"resource_type":         "knowledge_network",
		"resource_ids":          []string{"kn-1"},
		"visibility_operations": []string{"view_detail"},
	})
	if len(got) != 1 {
		t.Fatalf("got %v, want 1 entry", got)
	}
	if want := []string{"view_detail"}; !reflect.DeepEqual(got[0].Operations, want) {
		t.Errorf("ops = %v, want %v", got[0].Operations, want)
	}
}

// TestResourceFilterEndpointCatalogFallbackMixedTypes covers the one case where
// the batch has to be split: without candidate_operations each type projects its
// own catalog, so the two types must not borrow each other's operations.
func TestResourceFilterEndpointCatalogFallbackMixedTypes(t *testing.T) {
	r, e, db := newTestServer(t)
	const user = "u-1"
	seedEnabledUser(t, db, user)
	seedCatalogOps(t, db, "knowledge_network", "view_detail", "modify")
	seedCatalogOps(t, db, "toolbox", "use")
	_ = e.GrantObjectPermission(user, "knowledge_network", "kn-1", "view_detail")
	_ = e.GrantObjectPermission(user, "knowledge_network", "kn-1", "modify")
	_ = e.GrantObjectPermission(user, "toolbox", "tb-1", "use")

	got := postFilter(t, r, map[string]any{
		"accessor_id": user,
		"resources": []map[string]string{
			{"type": "knowledge_network", "id": "kn-1"},
			{"type": "toolbox", "id": "tb-1"},
		},
	})

	// Catalog order is a database detail, so compare the projections as sets.
	ops := map[string][]string{}
	for _, entry := range got {
		sort.Strings(entry.Operations)
		ops[entry.ResourceType] = entry.Operations
	}
	if want := []string{"modify", "view_detail"}; !reflect.DeepEqual(ops["knowledge_network"], want) {
		t.Errorf("knowledge_network ops = %v, want %v", ops["knowledge_network"], want)
	}
	if want := []string{"use"}; !reflect.DeepEqual(ops["toolbox"], want) {
		t.Errorf("toolbox ops = %v, want %v", ops["toolbox"], want)
	}
}

func TestResourceFilterDeduplicatesAndPreservesFirstSeenOrder(t *testing.T) {
	r, e, db := newTestServer(t)
	const user = "u-order"
	seedEnabledUser(t, db, user)
	seedCatalogOps(t, db, "type-a", "view_detail")
	seedCatalogOps(t, db, "type-b", "view_detail")
	for _, ref := range []struct{ resourceType, resourceID string }{
		{"type-a", "a-1"}, {"type-b", "b-1"}, {"type-a", "a-2"},
	} {
		if err := e.GrantObjectPermission(user, ref.resourceType, ref.resourceID, "view_detail"); err != nil {
			t.Fatal(err)
		}
	}

	got := postFilter(t, r, map[string]any{
		"accessor_id": user,
		"resources": []map[string]string{
			{"type": "type-a", "id": "a-1"},
			{"type": "type-b", "id": "b-1"},
			{"type": "type-a", "id": "a-1"},
			{"type": "type-a", "id": "a-2"},
		},
	})
	want := []string{"type-a:a-1", "type-b:b-1", "type-a:a-2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, entry := range got {
		if key := entry.ResourceType + ":" + entry.ResourceID; key != want[i] {
			t.Errorf("result %d = %s, want %s", i, key, want[i])
		}
	}

	got = postFilter(t, r, map[string]any{
		"accessor_id": user,
		"resources": []map[string]string{
			{"type": "type-a", "id": "a-1"},
			{"type": "type-a", "id": "a-1"},
		},
		"candidate_operations": []string{"view_detail", "view_detail"},
	})
	if len(got) != 1 || !reflect.DeepEqual(got[0].Operations, []string{"view_detail"}) {
		t.Fatalf("deduplicated result = %v", got)
	}

	duplicateResources := make([]map[string]string, 501)
	for i := range duplicateResources {
		duplicateResources[i] = map[string]string{"type": "type-a", "id": "a-1"}
	}
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":           user,
		"resources":             duplicateResources,
		"visibility_operations": []string{"view_detail", "query_data", "modify", "delete"},
		"candidate_operations":  []string{"view_detail", "query_data", "modify", "delete", "authorize"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("large duplicate input should be accepted after deduplication: %d %s", w.Code, w.Body.String())
	}
}

func TestResourceFilterDoesNotRejectLargeBatch(t *testing.T) {
	r, _, db := newTestServer(t)
	const user = "u-large-batch"
	seedEnabledUser(t, db, user)

	resources := make([]map[string]string, 0, 501)
	for i := 0; i < 501; i++ {
		resources = append(resources, map[string]string{"type": "resource", "id": strconv.Itoa(i + 1)})
	}
	operations := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		operations = append(operations, "op-"+strconv.Itoa(i+1))
	}
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":          user,
		"resources":            resources,
		"candidate_operations": operations,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("large batch status = %d, want 200: %s", w.Code, w.Body.String())
	}
}
