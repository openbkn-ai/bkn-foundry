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

func TestValidateImplicationsRejectsAuthoringMistakes(t *testing.T) {
	cases := []struct {
		name    string
		catalog catalog
		wantErr string
	}{
		{
			name: "implies an operation the type does not declare",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", Operations: []catalogOperation{
					{ID: "resource_manage", Implies: []string{"view_detail"}},
				}},
			}},
			wantErr: "does not declare",
		},
		{
			name: "self implication",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", Operations: []catalogOperation{
					{ID: "resource_manage", Implies: []string{"resource_manage"}},
				}},
			}},
			wantErr: "implies itself",
		},
		{
			name: "cycle",
			catalog: catalog{ResourceTypes: []catalogResourceType{
				{ID: "catalog", Operations: []catalogOperation{
					{ID: "a", Implies: []string{"b"}},
					{ID: "b", Implies: []string{"a"}},
				}},
			}},
			wantErr: "cycle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImplications(tc.catalog)
			if err == nil {
				t.Fatalf("validateImplications accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestShippedCatalogBindsResourceManageToViewDetail is the regression guard on
// the product rule (#1121): managing the tables in a catalog is unreachable
// without the right to open the catalog, because every management route loads
// its target first and that load is a view_detail judgement. Dropping the
// implication would let the console hand out a grant whose every route answers
// 403 while naming a permission the operator never meant to withhold.
func TestShippedCatalogBindsResourceManageToViewDetail(t *testing.T) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		t.Fatalf("parse catalog.json: %v", err)
	}
	if err := validateImplications(c); err != nil {
		t.Fatalf("shipped catalog.json declares an invalid implication: %v", err)
	}
	for _, rt := range c.ResourceTypes {
		if rt.ID != "catalog" {
			continue
		}
		for _, op := range rt.Operations {
			if op.ID != "resource_manage" {
				continue
			}
			if len(op.Implies) != 1 || op.Implies[0] != "view_detail" {
				t.Fatalf("catalog.resource_manage implies %v, want [view_detail]", op.Implies)
			}
			return
		}
		t.Fatal("catalog type no longer declares resource_manage")
	}
	t.Fatal("catalog type missing from catalog.json")
}

// TestSeedPersistsImplications proves the declaration survives the seed, since
// the grant paths read it from the operations table rather than from the file.
func TestSeedPersistsImplications(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var row model.Operation
	if err := db.First(&row, "resource_type_id = ? AND id = ?", "catalog", "resource_manage").Error; err != nil {
		t.Fatalf("load operation: %v", err)
	}
	if row.ImpliedOperationIDs != "view_detail" {
		t.Fatalf("implied_operation_ids = %q, want %q", row.ImpliedOperationIDs, "view_detail")
	}
}

// TestBackfillRepairsGrantsWrittenBeforeTheRule is the reason the write-time
// rule alone is not a fix: a grant made before it existed carries only
// resource_manage, reaches nothing, and nobody tells the administrator to
// re-save it.
func TestBackfillRepairsGrantsWrittenBeforeTheRule(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The shape an operator could produce before #1121: management without the
	// visibility every management route needs.
	if err := e.GrantObjectPermission("u-1", "catalog", "c1", "resource_manage"); err != nil {
		t.Fatal(err)
	}
	// An unrelated grant on the same type must come through untouched.
	if err := e.GrantObjectPermission("u-2", "catalog", "c2", "query_data"); err != nil {
		t.Fatal(err)
	}

	if err := Apply(db, e); err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	if ok, _ := e.Check("u-1", "catalog", "c1", "view_detail"); !ok {
		t.Fatal("backfill did not repair the pre-existing grant")
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "resource_manage"); !ok {
		t.Fatal("backfill dropped the operation it was repairing")
	}
	if ok, _ := e.Check("u-2", "catalog", "c2", "view_detail"); ok {
		t.Fatal("backfill widened a grant that implies nothing")
	}

	// Idempotent: a start with nothing left to repair changes nothing.
	if err := Apply(db, e); err != nil {
		t.Fatalf("third apply: %v", err)
	}
	grants, err := e.ListObjectGrants("u-1", "catalog", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || len(grants[0].Operations) != 2 {
		t.Fatalf("grant after repeated apply: %+v", grants)
	}
}
