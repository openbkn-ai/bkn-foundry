// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"sort"
	"testing"

	"vega-backend/interfaces"
)

// stubLister is an AccessibleResourceLister returning canned per-op access sets.
type stubLister struct {
	byType map[string]map[string]interfaces.OpAccess
}

func (s stubLister) AccessibleResourceIDs(_ context.Context, _, resourceType string, _ []string) (map[string]interfaces.OpAccess, error) {
	return s.byType[resourceType], nil
}

func opsSet(rops interfaces.PermissionResourceOps) map[string]bool {
	m := map[string]bool{}
	for _, o := range rops.Operations {
		m[o] = true
	}
	return m
}

func TestFillResourceOpsBulk(t *testing.T) {
	lister := stubLister{byType: map[string]map[string]interfaces.OpAccess{
		interfaces.AUTH_RESOURCE_TYPE_RESOURCE: {
			interfaces.OPERATION_TYPE_VIEW_DETAIL: {IDs: map[string]bool{"r1": true, "r2": true}},
			interfaces.OPERATION_TYPE_MODIFY:      {IDs: map[string]bool{"r1": true}},
		},
		// internal resources: a type-wide view_detail grant, no other ops.
		interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE: {
			interfaces.OPERATION_TYPE_VIEW_DETAIL: {All: true},
		},
	}}

	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "u-1"})
	ids := []string{"r1", "r2", "r3", "i1"} // r3 has no access; i1 is internal
	internal := map[string]struct{}{"i1": {}}

	out := map[string]interfaces.PermissionResourceOps{}
	rs := &resourceService{}
	if err := rs.fillResourceOpsBulk(ctx, ids, internal, lister, out); err != nil {
		t.Fatal(err)
	}

	// r3 is invisible (no view_detail) and must be absent; the other three visible.
	if len(out) != 3 {
		keys := make([]string, 0, len(out))
		for k := range out {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("want 3 visible resources, got %d: %v", len(out), keys)
	}
	if _, ok := out["r3"]; ok {
		t.Fatal("r3 has no view_detail and must be excluded")
	}

	// r1: view_detail + modify (concrete on both).
	if got := opsSet(out["r1"]); !(got[interfaces.OPERATION_TYPE_VIEW_DETAIL] && got[interfaces.OPERATION_TYPE_MODIFY] && len(got) == 2) {
		t.Fatalf("r1 ops = %v, want {view_detail, modify}", out["r1"].Operations)
	}
	// r2: view_detail only.
	if got := opsSet(out["r2"]); !(got[interfaces.OPERATION_TYPE_VIEW_DETAIL] && len(got) == 1) {
		t.Fatalf("r2 ops = %v, want {view_detail}", out["r2"].Operations)
	}
	// i1: internal, type-wide view_detail only (other ops not granted).
	if got := opsSet(out["i1"]); !(got[interfaces.OPERATION_TYPE_VIEW_DETAIL] && len(got) == 1) {
		t.Fatalf("i1 ops = %v, want {view_detail}", out["i1"].Operations)
	}
}
