// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
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
	r, e, _ := newTestServer(t)
	const user, role = "u-1", "role-builder"
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
	r, e, _ := newTestServer(t)
	const user = "u-1"
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
	r, e, _ := newTestServer(t)
	const admin, role = "admin-1", "role-super"
	_ = e.Grant(role, "*", "*")
	_ = e.AssignRole(admin, role)

	candidates := []string{"view_detail", "create", "modify", "delete", "data_query", "authorize", "task_manage"}
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
	r, _, _ := newTestServer(t)

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
}

// TestResourceFilterEndpointCatalogFallback checks that omitting
// candidate_operations projects the resource type's catalog ops, matching what
// POST /operations returns for the same resource.
func TestResourceFilterEndpointCatalogFallback(t *testing.T) {
	r, e, db := newTestServer(t)
	const user = "u-1"
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
