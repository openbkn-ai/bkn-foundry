// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"reflect"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/authzresource"
)

// paidType declares knowledge_base as a Professional type for one test, the way
// an enterprise binary does at assembly (#2512).
func paidType(t *testing.T) {
	t.Helper()
	authzresource.ResetForTest()
	t.Cleanup(authzresource.ResetForTest)
	authzresource.RequireEdition("knowledge_base", "knowledge_base", licverify.EditionProfessional)
}

// editionGateFixture grants a reader and makes an admin super administrator;
// the edition is changed through the returned pointer between calls.
func editionGateFixture(t *testing.T) (*Enforcer, *licverify.Edition) {
	t.Helper()
	edition := useEdition(t, licverify.EditionProfessional)
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission("reader", "knowledge_base", "kb-1", "query_data"))
	mustNoErr(t, e.GrantObjectPermission("reader", "resource", "r-1", "view_detail"))
	mustNoErr(t, e.AssignRole("admin", SuperAdminRoleID))
	mustNoErr(t, e.GrantRolePermission(SuperAdminRoleID, "", "*", ActAll))
	return e, edition
}

func mustCheck(t *testing.T, e *Enforcer, accessor, resourceType, resourceID, op string) bool {
	t.Helper()
	allowed, err := e.Check(accessor, resourceType, resourceID, op)
	if err != nil {
		t.Fatal(err)
	}
	return allowed
}

func TestPaidTypeIsDeniedBelowItsEditionForEveryone(t *testing.T) {
	paidType(t)
	e, edition := editionGateFixture(t)

	for _, licensed := range []licverify.Edition{licverify.EditionProfessional, licverify.EditionEnterprise} {
		*edition = licensed
		if !mustCheck(t, e, "reader", "knowledge_base", "kb-1", "query_data") ||
			!mustCheck(t, e, "admin", "knowledge_base", "kb-1", "query_data") {
			t.Fatalf("a %s licence did not answer the paid type from its grants", licensed)
		}
	}

	*edition = licverify.EditionCommunity
	for _, accessor := range []string{"reader", "admin"} {
		if mustCheck(t, e, accessor, "knowledge_base", "kb-1", "query_data") {
			t.Fatalf("%s was granted a Professional type on a Community licence", accessor)
		}
		ops, err := e.AllowedOps(accessor, "knowledge_base", "kb-1", []string{"query_data", "view_detail"})
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != 0 {
			t.Fatalf("%s AllowedOps on the paid type = %v, want none", accessor, ops)
		}
		decision, err := e.LocalDecision(context.Background(), accessor, "knowledge_base", "kb-1", "query_data")
		if err != nil {
			t.Fatal(err)
		}
		if decision.Allowed() {
			t.Fatalf("%s local decision on the paid type = %+v", accessor, decision)
		}
	}
	// Indistinguishable from a resource nobody was granted.
	gated, err := e.OperationDecision(context.Background(), "reader", "knowledge_base", "kb-1", "query_data")
	if err != nil {
		t.Fatal(err)
	}
	ungranted, err := e.OperationDecision(context.Background(), "nobody", "knowledge_base", "kb-1", "query_data")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gated, ungranted) {
		t.Fatalf("gated decision %+v differs from an ungranted one %+v", gated, ungranted)
	}
	// Types without RequireEdition are untouched.
	if !mustCheck(t, e, "reader", "resource", "r-1", "view_detail") ||
		!mustCheck(t, e, "admin", "resource", "r-2", "view_detail") {
		t.Fatal("the gate reached a type that declared no edition")
	}

	// The licence is read per call: renewing restores the kept grants unchanged.
	*edition = licverify.EditionProfessional
	if !mustCheck(t, e, "reader", "knowledge_base", "kb-1", "query_data") {
		t.Fatal("a renewed licence did not restore the reader's grant")
	}
}

func TestPaidTypeFiltersAndScopesAreEmptyBelowItsEdition(t *testing.T) {
	paidType(t)
	e, edition := editionGateFixture(t)
	*edition = licverify.EditionCommunity

	resources := []ResourceRef{{Type: "knowledge_base", ID: "kb-1"}, {Type: "resource", ID: "r-1"}}
	for _, scope := range []EvaluationScope{ScopeEffective, ScopeLocal} {
		for _, accessor := range []string{"reader", "admin"} {
			got, err := e.FilterResourceOpsScoped(context.Background(), accessor, resources,
				nil, []string{"query_data", "view_detail"}, VisibilityMatchAll, scope)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range got {
				if item.Type == "knowledge_base" && len(item.Operations) != 0 {
					t.Fatalf("%s %s filter kept %v on the paid type", accessor, scope, item.Operations)
				}
				if item.Type == "resource" && len(item.Operations) == 0 {
					t.Fatalf("%s %s filter dropped the ungated type", accessor, scope)
				}
			}
		}
	}

	for _, accessor := range []string{"reader", "admin"} {
		ids, unrestricted, fallback, err := e.AccessibleResourceScope(accessor, "knowledge_base", "query_data")
		if err != nil {
			t.Fatal(err)
		}
		if unrestricted || fallback || len(ids) != 0 {
			t.Fatalf("%s scope = (%v, %v, %v), want empty", accessor, ids, unrestricted, fallback)
		}
		ids, unrestricted, fallback, err = e.AccessibleResourceScopeAnyOperation(accessor, "knowledge_base")
		if err != nil {
			t.Fatal(err)
		}
		if unrestricted || fallback || len(ids) != 0 {
			t.Fatalf("%s any-operation scope = (%v, %v, %v), want empty", accessor, ids, unrestricted, fallback)
		}
		wildcard, grants, err := e.EffectivePermissions(accessor, PermQuery{ResourceType: "knowledge_base"})
		if err != nil {
			t.Fatal(err)
		}
		if wildcard || len(grants) != 0 {
			t.Fatalf("%s effective permissions on the paid type = (%v, %v), want none", accessor, wildcard, grants)
		}
	}
	_, grants, err := e.EffectivePermissions("reader", PermQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if rtype, _ := splitObjectKey(grant.Object); rtype == "knowledge_base" {
			t.Fatalf("unscoped effective permissions listed the paid type: %v", grants)
		}
	}
	if len(grants) == 0 {
		t.Fatal("unscoped effective permissions lost the ungated type")
	}

	*edition = licverify.EditionProfessional
	ids, _, _, err := e.AccessibleResourceScope("reader", "knowledge_base", "query_data")
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(ids, []string{"kb-1"}) {
		t.Fatalf("licensed reader scope = %v, want kb-1", ids)
	}
}

// A community binary never calls RequireEdition, so every type answers from
// its grants whatever the licence says.
func TestUndeclaredTypesAreNotGated(t *testing.T) {
	authzresource.ResetForTest()
	t.Cleanup(authzresource.ResetForTest)
	e, edition := editionGateFixture(t)
	*edition = licverify.EditionCommunity
	if !mustCheck(t, e, "reader", "knowledge_base", "kb-1", "query_data") ||
		!mustCheck(t, e, "admin", "knowledge_base", "kb-1", "query_data") {
		t.Fatal("an undeclared type was gated")
	}
}

// Every local reader shares the gate: a type under a paid parent inherits
// nothing from it, and explain neither allows a step nor lists kept grants.
func TestPaidParentIsNotInheritedAndExplainAgrees(t *testing.T) {
	edition := useEdition(t, licverify.EditionCommunity)
	authzresource.ResetForTest()
	t.Cleanup(authzresource.ResetForTest)
	authzresource.RequireEdition("catalog", "catalog", licverify.EditionProfessional)
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-in", "cat-1")
	mustNoErr(t, e.GrantObjectPermission("u-1", "catalog", "cat-1", "resource_manage"))

	if mustCheck(t, e, "u-1", "resource", "res-in", "modify") {
		t.Fatal("a child inherited a grant on a paid parent below its edition")
	}
	explained, err := e.ExplainOperation(context.Background(), "u-1", "catalog", "cat-1", "resource_manage")
	if err != nil {
		t.Fatal(err)
	}
	if explained.Evaluation.Allowed() {
		t.Fatalf("explain evaluation = %+v, want deny", explained.Evaluation)
	}
	for _, step := range explained.Steps {
		if step.Decision == DecisionAllow || len(step.MatchedGrants) != 0 {
			t.Fatalf("explain step contradicts the gated evaluation: %+v", step)
		}
	}

	*edition = licverify.EditionProfessional
	if !mustCheck(t, e, "u-1", "resource", "res-in", "modify") {
		t.Fatal("the licensed parent grant was not inherited")
	}
	explained, err = e.ExplainOperation(context.Background(), "u-1", "catalog", "cat-1", "resource_manage")
	if err != nil {
		t.Fatal(err)
	}
	if !explained.Evaluation.Allowed() || len(explained.Steps) == 0 || len(explained.Steps[0].MatchedGrants) == 0 {
		t.Fatalf("licensed explain = %+v", explained)
	}
}
