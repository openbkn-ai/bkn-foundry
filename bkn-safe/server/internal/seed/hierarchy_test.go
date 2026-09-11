// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// TestSeedDeclaresKnowledgeNetworkHierarchy pins the final BKN operation table
// and the direct-first action execution fallback.
func TestSeedDeclaresKnowledgeNetworkHierarchy(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	children := []string{"concept_group", "object_type", "relation_type", "action_type", "metric", "risk_type"}
	for _, child := range children {
		var rt model.ResourceType
		if err := db.First(&rt, "id = ?", child).Error; err != nil {
			t.Fatalf("load resource type %s: %v", child, err)
		}
		if rt.ParentTypeID != "knowledge_network" {
			t.Errorf("%s parent = %q, want knowledge_network", child, rt.ParentTypeID)
		}
	}

	type operationSpec struct {
		parent   string
		requires string
	}
	want := map[string]map[string]operationSpec{
		"knowledge_network": {
			"view_detail": {}, "create": {}, "modify": {requires: "view_detail"},
			"delete": {requires: "view_detail"}, "query_data": {},
			"authorize": {requires: "view_detail"}, "execute": {},
		},
		"concept_group": {
			"view_detail": {parent: "view_detail"}, "modify": {parent: "modify", requires: "view_detail"},
			"delete": {parent: "modify", requires: "view_detail"},
		},
		"object_type": {
			"view_detail": {parent: "view_detail"}, "query_data": {parent: "query_data"},
			"modify": {parent: "modify", requires: "view_detail"},
			"delete": {parent: "modify", requires: "view_detail"},
		},
		"relation_type": {
			"view_detail": {parent: "view_detail"}, "query_data": {parent: "query_data"},
			"modify": {parent: "modify", requires: "view_detail"},
			"delete": {parent: "modify", requires: "view_detail"},
		},
		"action_type": {
			"view_detail": {parent: "view_detail"}, "modify": {parent: "modify", requires: "view_detail"},
			"delete": {parent: "modify", requires: "view_detail"}, "execute": {parent: "execute"},
		},
		"metric": {
			"view_detail": {parent: "view_detail"}, "query_data": {parent: "query_data"},
			"modify": {parent: "modify", requires: "view_detail"},
			"delete": {parent: "modify", requires: "view_detail"},
		},
		"risk_type": {
			"view_detail": {parent: "view_detail"}, "modify": {parent: "modify", requires: "view_detail"},
			"delete": {parent: "modify", requires: "view_detail"},
		},
	}
	for resourceType, expected := range want {
		var operations []model.Operation
		if err := db.Where("resource_type_id = ?", resourceType).Find(&operations).Error; err != nil {
			t.Fatalf("load operations for %s: %v", resourceType, err)
		}
		if len(operations) != len(expected) {
			t.Errorf("%s operations = %+v, want exactly %d", resourceType, operations, len(expected))
		}
		for _, operation := range operations {
			spec, ok := expected[operation.ID]
			if !ok {
				t.Errorf("%s unexpectedly declares %s", resourceType, operation.ID)
				continue
			}
			if operation.ParentOperationID != spec.parent || operation.RequiredOperationIDs != spec.requires {
				t.Errorf("%s/%s = parent %q requires %q, want parent %q requires %q",
					resourceType, operation.ID, operation.ParentOperationID, operation.RequiredOperationIDs,
					spec.parent, spec.requires)
			}
		}
	}
	var executeActionCount int64
	if err := db.Model(&model.Operation{}).Where("id = ?", "execute_action").Count(&executeActionCount).Error; err != nil {
		t.Fatal(err)
	}
	if executeActionCount != 0 {
		t.Fatalf("catalog declares %d execute_action operations, want none", executeActionCount)
	}

	var vegaResource model.ResourceType
	if err := db.First(&vegaResource, "id = ?", "resource").Error; err != nil {
		t.Fatalf("load Vega resource type: %v", err)
	}
	if vegaResource.ParentTypeID != "" {
		t.Errorf("Vega resource parent = %q, want no hierarchy", vegaResource.ParentTypeID)
	}

	parents := []model.ResourceParent{
		{ResourceTypeID: "object_type", ResourceID: "kn-1/shared", ParentTypeID: "knowledge_network", ParentID: "kn-1"},
		{ResourceTypeID: "object_type", ResourceID: "kn-2/shared", ParentTypeID: "knowledge_network", ParentID: "kn-2"},
		{ResourceTypeID: "action_type", ResourceID: "kn-1/run", ParentTypeID: "knowledge_network", ParentID: "kn-1"},
	}
	if err := db.Create(&parents).Error; err != nil {
		t.Fatalf("create resource parents: %v", err)
	}
	mustNoErrSeed(t, e.GrantCommunityBundle("bundle-parent", "knowledge_network", "kn-1", authz.AuthoritySourceSystem))
	for _, operation := range []string{"view_detail", "query_data", "modify", "delete"} {
		if allowed, checkErr := e.Check("bundle-parent", "object_type", "kn-1/shared", operation); checkErr != nil || !allowed {
			t.Fatalf("Community KN bundle did not inherit object_type/%s: %v, %v", operation, allowed, checkErr)
		}
	}
	for _, operation := range []string{"authorize", "task_manage"} {
		if allowed, checkErr := e.Check("bundle-parent", "object_type", "kn-1/shared", operation); checkErr != nil || allowed {
			t.Fatalf("removed child operation %s = %v, %v; want denied", operation, allowed, checkErr)
		}
	}
	if allowed, checkErr := e.Check("bundle-parent", "object_type", "kn-2/shared", "view_detail"); checkErr != nil || allowed {
		t.Fatalf("parent inheritance crossed the KN boundary: %v, %v", allowed, checkErr)
	}
	mustNoErrSeed(t, e.GrantObjectPermission("requires-view", "object_type", "kn-1/shared", "modify"))
	if allowed, _ := e.Check("requires-view", "object_type", "kn-1/shared", "modify"); allowed {
		t.Fatal("object_type/modify bypassed its view_detail requirement")
	}
	mustNoErrSeed(t, e.GrantObjectPermission("requires-view", "object_type", "kn-1/shared", "view_detail"))
	if allowed, _ := e.Check("requires-view", "object_type", "kn-1/shared", "modify"); !allowed {
		t.Fatal("object_type/modify stayed denied after its view_detail requirement was granted")
	}

	assertFinal := func(user string, allowed bool, basis authz.DecisionBasis) {
		t.Helper()
		decision, err := e.OperationDecision(t.Context(), user, "action_type", "kn-1/run", "execute")
		if err != nil || decision.Allowed() != allowed || decision.Basis != basis {
			t.Fatalf("OperationDecision(%s) = %+v, %v; want allowed=%v basis=%s", user, decision, err, allowed, basis)
		}
		checked, err := e.Check(user, "action_type", "kn-1/run", "execute")
		if err != nil || checked != allowed {
			t.Fatalf("Check(%s) = %v, %v; want %v", user, checked, err, allowed)
		}
		operations, err := e.AllowedOps(user, "action_type", "kn-1/run", []string{"execute"})
		if err != nil || (len(operations) == 1) != allowed {
			t.Fatalf("AllowedOps(%s) = %v, %v; want allowed=%v", user, operations, err, allowed)
		}
		filtered, err := e.FilterResourceOps(user,
			[]authz.ResourceRef{{Type: "action_type", ID: "kn-1/run"}}, nil, []string{"execute"})
		if err != nil || len(filtered) != 1 || (len(filtered[0].Operations) == 1) != allowed {
			t.Fatalf("FilterResourceOps(%s) = %+v, %v; want allowed=%v", user, filtered, err, allowed)
		}
		accessible, err := e.AccessibleResources(user, "action_type", "execute")
		if err != nil || slices.Contains(accessible, "kn-1/run") != allowed {
			t.Fatalf("AccessibleResources(%s) = %v, %v; want allowed=%v", user, accessible, err, allowed)
		}
	}

	mustNoErrSeed(t, e.GrantObjectPermission("parent-allow", "knowledge_network", "kn-1", "execute"))
	assertFinal("parent-allow", true, authz.BasisInherited)
	mustNoErrSeed(t, e.DenyObjectPermission("parent-deny", "knowledge_network", "kn-1", "execute"))
	assertFinal("parent-deny", false, authz.BasisInherited)
	mustNoErrSeed(t, e.DenyObjectPermission("child-allow", "knowledge_network", "kn-1", "execute"))
	mustNoErrSeed(t, e.GrantObjectPermission("child-allow", "action_type", "kn-1/run", "execute"))
	assertFinal("child-allow", true, authz.BasisDirect)
	mustNoErrSeed(t, e.GrantObjectPermission("child-deny", "knowledge_network", "kn-1", "execute"))
	mustNoErrSeed(t, e.DenyObjectPermission("child-deny", "action_type", "kn-1/run", "execute"))
	assertFinal("child-deny", false, authz.BasisDirect)
	mustNoErrSeed(t, e.GrantObjectPermission("same-deny", "action_type", "kn-1/run", "execute"))
	mustNoErrSeed(t, e.DenyObjectPermission("same-deny", "action_type", "kn-1/run", "execute"))
	assertFinal("same-deny", false, authz.BasisDirect)
	mustNoErrSeed(t, e.GrantObjectPermission("historical-direct", "action_type", "kn-1/run", "execute"))
	assertFinal("historical-direct", true, authz.BasisDirect)
	assertFinal("default-deny", false, authz.BasisDefault)
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

// TestSeedMigratesLegacyOperationSpelling: 知识网络上的「数据查询」原本拼作
// data_query，而目录/资源侧是 query_data。统一成后者时，角色授权会随种子
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
	// 同名但不同类型的授权不该被动到：catalog 侧从来就叫 query_data。
	mustNoErrSeed(t, e.GrantObjectPermission(user, "catalog", "c-1", "query_data"))

	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ rtype, id string }{{"knowledge_network", "kn-1"}} {
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
