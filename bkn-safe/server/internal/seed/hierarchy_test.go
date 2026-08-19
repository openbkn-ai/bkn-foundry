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

// TestSeedDeclaresNoHierarchyForResources pins the hierarchy DORMANT.
//
// The mechanism below (declaration, climb, enumeration) works and is kept, but
// the catalog/resource pair no longer uses it: vega resolves the fallback at its
// own decision point, where the resource row it is judging already carries
// catalog_id (#817). Nothing has to reach bkn-safe, and with no declaration the
// climb never fires — so this asserts the ABSENCE, which is the thing a future
// edit could silently undo.
func TestSeedDeclaresNoHierarchyForResources(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	var types []model.ResourceType
	if err := db.Where("parent_type_id <> ''").Find(&types).Error; err != nil {
		t.Fatalf("load resource types: %v", err)
	}
	for _, rt := range types {
		t.Errorf("resource type %q declares parent %q — the shipped catalog declares no hierarchy, "+
			"and adding one turns the climb back on for every decision on that type", rt.ID, rt.ParentTypeID)
	}

	var ops []model.Operation
	if err := db.Where("parent_operation_id <> ''").Find(&ops).Error; err != nil {
		t.Fatalf("load operations: %v", err)
	}
	for _, op := range ops {
		t.Errorf("operation %s/%s maps to %q on a parent, but no type declares a parent",
			op.ResourceTypeID, op.ID, op.ParentOperationID)
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

// TestSeedMigratesLegacyOperationSpelling: 知识网络与流式数据管道上的「数据查询」
// 原本拼作 data_query，而目录/资源侧是 query_data。统一成后者时，角色授权会随种子
// 重建自动跟上，但管理员在**单个对象**上发过的授权带的还是旧拼写——不迁移的话，
// 用户的体验是「我发过的权限凭空消失了」。
func TestSeedMigratesLegacyOperationSpelling(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	// 升级前的对象级授权：管理员在某个知识网络上发过 data_query。
	const user = "u-1"
	mustNoErrSeed(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "data_query"))
	mustNoErrSeed(t, e.GrantObjectPermission(user, "stream_data_pipeline", "p-1", "data_query"))
	// 同名但不同类型的授权不该被动到：catalog 侧从来就叫 query_data。
	mustNoErrSeed(t, e.GrantObjectPermission(user, "catalog", "c-1", "query_data"))

	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ rtype, id string }{
		{"knowledge_network", "kn-1"},
		{"stream_data_pipeline", "p-1"},
	} {
		ok, err := e.Check(user, tc.rtype, tc.id, "query_data")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("%s/%s: 旧拼写的对象级授权没有迁移过来，用户会以为权限被吞了", tc.rtype, tc.id)
		}
		stale, err := e.Check(user, tc.rtype, tc.id, "data_query")
		if err != nil {
			t.Fatal(err)
		}
		if stale {
			t.Errorf("%s/%s: 旧拼写还在，等于同一件事有两个名字", tc.rtype, tc.id)
		}
	}

	// 幂等：再跑一次不该有任何变化。
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	ok, err := e.Check(user, "catalog", "c-1", "query_data")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("catalog 侧本来就是 query_data，不该被迁移逻辑碰到")
	}
}

func mustNoErrSeed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestSeedPrunesWithdrawnOperations covers the half that upserting cannot do.
//
// A withdrawn operation used to survive in every UPGRADED deployment: the seed
// only ever inserted or updated, so the row stayed, the grant console kept
// offering the verb, and an administrator could hand out a permission that no
// code enforces. A fresh install never showed it, which is exactly why it went
// unnoticed — it was found on a real cluster where knowledge_network carried
// both data_query and query_data after the rename in #882.
func TestSeedPrunesWithdrawnOperations(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	// Simulate what an older seed left behind on an upgraded deployment.
	stale := []model.Operation{
		{ResourceTypeID: "knowledge_network", ID: "data_query", Name: "数据查询"},
		{ResourceTypeID: "resource", ID: "modify", Name: "修改"},
	}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}

	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	for _, tc := range stale {
		var n int64
		if err := db.Model(&model.Operation{}).
			Where("resource_type_id = ? AND id = ?", tc.ResourceTypeID, tc.ID).
			Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s/%s survived the seed — the console would still offer a verb nothing enforces",
				tc.ResourceTypeID, tc.ID)
		}
	}

	// Declared operations are untouched: pruning must not eat the vocabulary.
	for _, tc := range []struct{ rtype, op string }{
		{"resource", "view_detail"},
		{"resource", "query_data"},
		{"catalog", "resource_manage"},
		{"knowledge_network", "query_data"},
	} {
		var n int64
		if err := db.Model(&model.Operation{}).
			Where("resource_type_id = ? AND id = ?", tc.rtype, tc.op).
			Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s/%s is declared but missing after the seed", tc.rtype, tc.op)
		}
	}
}
