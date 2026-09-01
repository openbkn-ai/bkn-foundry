// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// declareHierarchy registers the catalog/resource pair the way the seed does, so
// the endpoint's shape check has something to accept. newTestServer does not run
// the seed.
func declareHierarchy(t *testing.T, db *gorm.DB) {
	t.Helper()
	rows := []model.ResourceType{
		{ID: "catalog", Name: "数据目录"},
		{ID: "resource", Name: "数据资源", ParentTypeID: "catalog"},
		{ID: "agent", Name: "数据智能体"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed resource types: %v", err)
	}
	ops := []model.Operation{
		{ResourceTypeID: "catalog", ID: "resource_manage"},
		{ResourceTypeID: "resource", ID: "modify", ParentOperationID: "resource_manage"},
	}
	if err := db.Create(&ops).Error; err != nil {
		t.Fatalf("seed operations: %v", err)
	}
}

type parentItem struct {
	ResourceID string `json:"resource_id"`
	ParentType string `json:"parent_type"`
	ParentID   string `json:"parent_id"`
}

func getParents(t *testing.T, r *gin.Engine, query string) ([]parentItem, int) {
	t.Helper()
	w := do(t, r, http.MethodGet, "/api/safe/v1/authz/resource-parents?"+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET resource-parents = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []parentItem `json:"items"`
		Total int          `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return resp.Items, resp.Total
}

// TestResourceParentsUpsert is the contract vega's synchroniser depends on:
// pushing a snapshot twice is not an error, and a resource that MOVED to another
// catalog ends up with one row pointing at the new one — not two rows.
func TestResourceParentsUpsert(t *testing.T) {
	r, _, db := newTestServer(t)
	declareHierarchy(t, db)

	put := func(items []map[string]string) *gin.Engine {
		w := do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
			"resource_type": "resource",
			"parent_type":   "catalog",
			"items":         items,
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("PUT resource-parents = %d body=%s", w.Code, w.Body.String())
		}
		return r
	}

	put([]map[string]string{
		{"resource_id": "res-1", "parent_id": "cat-a"},
		{"resource_id": "res-2", "parent_id": "cat-a"},
	})
	items, total := getParents(t, r, "resource_type=resource")
	if total != 2 || len(items) != 2 {
		t.Fatalf("after first push: total=%d items=%v, want 2", total, items)
	}

	// Re-push with res-2 moved to another catalog: still two rows, res-2 repointed.
	put([]map[string]string{
		{"resource_id": "res-1", "parent_id": "cat-a"},
		{"resource_id": "res-2", "parent_id": "cat-b"},
	})
	items, total = getParents(t, r, "resource_type=resource")
	if total != 2 {
		t.Fatalf("after re-push: total=%d, want 2 (upsert, not insert)", total)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.ResourceID] = it.ParentID
		if it.ParentType != "catalog" {
			t.Errorf("%s parent_type = %q, want catalog", it.ResourceID, it.ParentType)
		}
	}
	if got["res-1"] != "cat-a" || got["res-2"] != "cat-b" {
		t.Errorf("parents = %v, want res-1 under cat-a and res-2 under cat-b", got)
	}

	// parent_id filter is what a "what is in this catalog" reconciliation reads.
	items, total = getParents(t, r, "resource_type=resource&parent_id=cat-b")
	if total != 1 || len(items) != 1 || items[0].ResourceID != "res-2" {
		t.Errorf("filtered by cat-b = %v (total %d), want only res-2", items, total)
	}
}

// TestResourceParentsRejectsUndeclaredShapes covers the guard that stands in for
// a credential on this tokenless route. Each rejected shape is a privilege path:
// an undeclared pair invents a hierarchy the seed never sanctioned, and a
// wildcard id makes one grant reach every instance of the type.
func TestResourceParentsRejectsUndeclaredShapes(t *testing.T) {
	r, _, db := newTestServer(t)
	declareHierarchy(t, db)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown child type", map[string]any{
			"resource_type": "not_a_type", "parent_type": "catalog",
			"items": []map[string]string{{"resource_id": "x", "parent_id": "c"}},
		}},
		{"child type has no parent", map[string]any{
			"resource_type": "agent", "parent_type": "catalog",
			"items": []map[string]string{{"resource_id": "x", "parent_id": "c"}},
		}},
		{"wrong parent type", map[string]any{
			"resource_type": "resource", "parent_type": "agent",
			"items": []map[string]string{{"resource_id": "x", "parent_id": "c"}},
		}},
		{"wildcard resource id", map[string]any{
			"resource_type": "resource", "parent_type": "catalog",
			"items": []map[string]string{{"resource_id": "*", "parent_id": "cat-a"}},
		}},
		{"wildcard parent id", map[string]any{
			"resource_type": "resource", "parent_type": "catalog",
			"items": []map[string]string{{"resource_id": "res-1", "parent_id": "*"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("PUT = %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
			var n int64
			db.Model(&model.ResourceParent{}).Count(&n)
			if n != 0 {
				t.Fatalf("rejected request still wrote %d rows", n)
			}
		})
	}
}

// TestResourceParentsSelfParentRejected keeps the enforce-time chain walk from
// ever meeting a one-node cycle.
func TestResourceParentsSelfParentRejected(t *testing.T) {
	r, _, db := newTestServer(t)
	if err := db.Create(&model.ResourceType{ID: "catalog", Name: "数据目录", ParentTypeID: "catalog"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": "catalog", "parent_type": "catalog",
		"items": []map[string]string{{"resource_id": "cat-a", "parent_id": "cat-a"}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("self-parent PUT = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

// TestResourceParentsDelete covers the synchroniser's orphan cleanup.
func TestResourceParentsDelete(t *testing.T) {
	r, _, db := newTestServer(t)
	declareHierarchy(t, db)
	do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": "resource", "parent_type": "catalog",
		"items": []map[string]string{
			{"resource_id": "res-1", "parent_id": "cat-a"},
			{"resource_id": "res-2", "parent_id": "cat-a"},
		},
	})

	w := do(t, r, http.MethodDelete, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": "resource", "resource_ids": []string{"res-1", "res-unknown"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d body=%s", w.Code, w.Body.String())
	}
	items, total := getParents(t, r, "resource_type=resource")
	if total != 1 || len(items) != 1 || items[0].ResourceID != "res-2" {
		t.Errorf("after delete = %v (total %d), want only res-2", items, total)
	}
}

// TestOwnershipPushMakesCheckInherit walks the whole contract end to end: a
// module pushes the ownership fact over HTTP, and the very next /authz/check on
// a resource with no policy row of its own is answered by its catalog's grant.
// Before the push, the same call is refused.
func TestOwnershipPushMakesCheckInherit(t *testing.T) {
	r, e, db := newTestServer(t)
	declareHierarchy(t, db)
	const user = "u-1"
	seedEnabledUser(t, db, user)
	_ = e.GrantObjectPermission(user, "catalog", "cat-a", "resource_manage")

	check := func() bool {
		w := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
			"accessor_id": user,
			"resource":    map[string]string{"type": "resource", "id": "res-1"},
			"operation":   "modify",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("check = %d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Allowed bool `json:"allowed"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.Allowed
	}

	if check() {
		t.Fatal("resource/modify was allowed before any ownership was recorded")
	}
	do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": "resource", "parent_type": "catalog",
		"items": []map[string]string{{"resource_id": "res-1", "parent_id": "cat-a"}},
	})
	if !check() {
		t.Error("resource/modify still refused after the table was recorded under a granted catalog")
	}
}

// TestDeletePoliciesDropsHierarchyRow: resource deletion already tears down the
// policies through this route, so the hierarchy row must go with them. Otherwise
// a recycled resource id would inherit from whatever catalog the dead one sat in.
func TestDeletePoliciesDropsHierarchyRow(t *testing.T) {
	r, _, db := newTestServer(t)
	declareHierarchy(t, db)
	do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": "resource", "parent_type": "catalog",
		"items": []map[string]string{{"resource_id": "res-1", "parent_id": "cat-a"}},
	})

	w := do(t, r, http.MethodDelete, "/api/safe/v1/authz/policies", map[string]any{
		"resource": map[string]string{"type": "resource", "id": "res-1"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE policies = %d body=%s", w.Code, w.Body.String())
	}
	var n int64
	db.Model(&model.ResourceParent{}).Count(&n)
	if n != 0 {
		t.Errorf("hierarchy row survived the policy teardown (%d rows left)", n)
	}
}

// TestOwnershipDryRunWritesNothing is the endpoint half of the confirmation
// step: the same request body, plus ?dry_run=true, reports the widening and
// leaves the table untouched — so a synchroniser can ask before it pushes.
func TestOwnershipDryRunWritesNothing(t *testing.T) {
	r, e, db := newTestServer(t)
	declareHierarchy(t, db)
	const user = "u-1"
	_ = e.GrantObjectPermission(user, "catalog", "cat-a", "resource_manage")

	body := map[string]any{
		"resource_type": "resource", "parent_type": "catalog",
		"items": []map[string]string{
			{"resource_id": "res-1", "parent_id": "cat-a"},
			{"resource_id": "res-2", "parent_id": "cat-b"},
		},
	}
	w := do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents?dry_run=true", body)
	if w.Code != http.StatusOK {
		t.Fatalf("dry run = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Changes []struct {
			AccessorID string `json:"accessor_id"`
			ResourceID string `json:"resource_id"`
			Operation  string `json:"operation"`
			Direction  string `json:"direction"`
		} `json:"changes"`
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if resp.Total != 1 || len(resp.Changes) != 1 {
		t.Fatalf("changes = %+v (total %d), want the single modify on res-1", resp.Changes, resp.Total)
	}
	got := resp.Changes[0]
	if got.AccessorID != user || got.ResourceID != "res-1" || got.Operation != "modify" || got.Direction != "grant" {
		t.Errorf("change = %+v, want u-1/res-1/modify as a grant", got)
	}
	if resp.Truncated {
		t.Error("truncated = true on a one-row report")
	}

	var n int64
	db.Model(&model.ResourceParent{}).Count(&n)
	if n != 0 {
		t.Errorf("dry run wrote %d rows", n)
	}

	// And the real push still works afterwards.
	w = do(t, r, http.MethodPut, "/api/safe/v1/authz/resource-parents", body)
	if w.Code != http.StatusNoContent {
		t.Fatalf("push after dry run = %d body=%s", w.Code, w.Body.String())
	}
	db.Model(&model.ResourceParent{}).Count(&n)
	if n != 2 {
		t.Errorf("rows after push = %d, want 2", n)
	}
}
