// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// TestSeedDeclaresResourceUnderCatalog pins the operation MAPPING, which is the
// part that is easy to get wrong and dangerous when wrong. Inheriting by name
// would let "modify" on a catalog — rename the catalog — reach every table
// inside it; the mapping sends the table's write verbs to resource_manage
// instead. authorize deliberately maps to nothing: a catalog grant must not turn
// into the right to re-grant every table under it (#800).
func TestSeedDeclaresResourceUnderCatalog(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	var rt model.ResourceType
	if err := db.First(&rt, "id = ?", "resource").Error; err != nil {
		t.Fatalf("load resource type: %v", err)
	}
	if rt.ParentTypeID != "catalog" {
		t.Errorf("resource.parent_type = %q, want catalog", rt.ParentTypeID)
	}

	var catalogType model.ResourceType
	if err := db.First(&catalogType, "id = ?", "catalog").Error; err != nil {
		t.Fatalf("load catalog type: %v", err)
	}
	if catalogType.ParentTypeID != "" {
		t.Errorf("catalog.parent_type = %q, want empty (catalog is a root)", catalogType.ParentTypeID)
	}

	want := map[string]string{
		"view_detail": "view_detail",
		"query_data":  "query_data",
		"modify":      "resource_manage",
		"delete":      "resource_manage",
		"task_manage": "task_manage",
		"authorize":   "", // no second-hand granting through the parent
		// create is judged against the wildcard object resource:* (the table does
		// not exist yet), and a wildcard id can never have an ownership row, so an
		// inheritance mapping here would be one that can never fire. Creating a
		// table is judged at the call site against catalog/resource_manage instead.
		"create": "",
	}
	var ops []model.Operation
	if err := db.Where("resource_type_id = ?", "resource").Find(&ops).Error; err != nil {
		t.Fatalf("load resource operations: %v", err)
	}
	got := make(map[string]string, len(ops))
	for _, op := range ops {
		got[op.ID] = op.ParentOperationID
	}
	for op, parentOp := range want {
		if got[op] != parentOp {
			t.Errorf("resource/%s inherits from catalog/%q, want %q", op, got[op], parentOp)
		}
	}
}

// TestValidateHierarchyRejectsAuthoringMistakes: every case here would compile,
// seed cleanly and then produce a grant that silently never applies, so the seed
// fails instead.
func TestValidateHierarchyRejectsAuthoringMistakes(t *testing.T) {
	cases := []struct {
		name    string
		catalog catalog
		wantErr string
	}{
		{
			name: "unknown parent type",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "resource", ParentType: "nope"},
			}},
			wantErr: "unknown parent_type",
		},
		{
			name: "parent operation not registered on the parent",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", Operations: []catalogOperation{{ID: "view_detail"}}},
				{ID: "resource", ParentType: "catalog", Operations: []catalogOperation{
					{ID: "modify", ParentOperation: "resource_manage"},
				}},
			}},
			wantErr: "not a registered operation",
		},
		{
			name: "operation inherits but the type has no parent",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "resource", Operations: []catalogOperation{
					{ID: "modify", ParentOperation: "resource_manage"},
				}},
			}},
			wantErr: "has no parent_type",
		},
		{
			name: "self parent",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", ParentType: "catalog"},
			}},
			wantErr: "its own parent_type",
		},
		{
			name: "cycle",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "a", ParentType: "b"},
				{ID: "b", ParentType: "a"},
			}},
			wantErr: "cycle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHierarchy(tc.catalog)
			if err == nil {
				t.Fatalf("validateHierarchy accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateHierarchyAcceptsShippedCatalog is the regression guard on the
// embedded file itself: Apply already runs the validation, but this names the
// failure so a bad edit reads as "the catalog is wrong" rather than "the seed
// broke".
func TestValidateHierarchyAcceptsShippedCatalog(t *testing.T) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		t.Fatalf("parse catalog.json: %v", err)
	}
	if err := validateHierarchy(c); err != nil {
		t.Fatalf("shipped catalog.json declares an invalid hierarchy: %v", err)
	}
}
