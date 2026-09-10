// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permobject"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

func requireDecision(t *testing.T, got Evaluation, decision Decision, basis DecisionBasis) {
	t.Helper()
	if got.Decision != decision || got.Basis != basis {
		t.Fatalf("decision = (%s, %s), want (%s, %s)", got.Decision, got.Basis, decision, basis)
	}
}

func TestStructuredDecisionPriority(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")

	const user = "decision-user"
	mustNoErr(t, e.GrantRolePermission("resource-reader", "resource", "*", "view_detail"))
	mustNoErr(t, e.AssignRole(user, "resource-reader"))

	// A parent deny is more specific than the child's type-wide allow.
	mustNoErr(t, e.DenyObjectPermission(user, "catalog", "cat-1", "view_detail"))
	got, err := e.OperationDecision(t.Context(), user, "resource", "res-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, got, DecisionDeny, BasisInherited)

	// An exact child allow is more specific than the parent deny.
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-1", "view_detail"))
	got, err = e.OperationDecision(t.Context(), user, "resource", "res-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, got, DecisionAllow, BasisDirect)

	// A type-wide deny is terminal even when an exact allow also exists.
	mustNoErr(t, e.DenyObjectPermission(user, "resource", "*", "view_detail"))
	got, err = e.OperationDecision(t.Context(), user, "resource", "res-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, got, DecisionDeny, BasisWildcard)
}

func TestLocalAndEffectiveScopes(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	const user = "scope-user"
	mustNoErr(t, e.GrantRolePermission("reader", "resource", "*", "view_detail"))
	mustNoErr(t, e.AssignRole(user, "reader"))

	local, err := e.LocalDecision(t.Context(), user, "resource", "r-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, local, DecisionAllow, BasisWildcard)
	if local.Scope != ScopeLocal || !local.Allowed() {
		t.Fatalf("local = %+v", local)
	}

	missing, err := e.LocalDecision(t.Context(), user, "resource", "r-1", "modify")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, missing, DecisionNone, BasisNone)
	if missing.Allowed() {
		t.Fatal("local none must never set allowed=true")
	}

	effective, err := e.OperationDecision(t.Context(), user, "resource", "r-1", "modify")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, effective, DecisionDeny, BasisDefault)
	if effective.Scope != ScopeEffective {
		t.Fatalf("scope = %q, want effective", effective.Scope)
	}
}

func TestCommunityBundleHasStructuredBundleBasis(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "bundle-user"
	if err := e.GrantCommunityBundle(user, "knowledge_network", "kn-1", AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	got, err := e.LocalDecision(t.Context(), user, "knowledge_network", "kn-1", "query_data")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, got, DecisionAllow, BasisBundle)
}

func TestRemovingChildRuleRestoresParentFallback(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")
	const user = "fallback-user"
	mustNoErr(t, e.DenyObjectPermission(user, "catalog", "cat-1", "view_detail"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-1", "view_detail"))

	direct, err := e.OperationDecision(t.Context(), user, "resource", "res-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, direct, DecisionAllow, BasisDirect)

	mustNoErr(t, e.RevokeObjectPermission(user, "resource", "res-1", "view_detail"))
	fallback, err := e.OperationDecision(t.Context(), user, "resource", "res-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, fallback, DecisionDeny, BasisInherited)
}

type structuredEEFake struct {
	opinion permobject.LocalOpinion
	seen    permobject.Request
}

type contextEEFake struct{}

func (contextEEFake) Decide(ctx context.Context, _ permobject.Request) (permobject.LocalOpinion, error) {
	return permobject.LocalOpinion{}, ctx.Err()
}

func TestAllowedOpsContextReachesEnterpriseProvider(t *testing.T) {
	permobject.ResetForTest()
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Licensed: true, Edition: licverify.EditionEnterprise}
	}))
	t.Cleanup(func() {
		permobject.ResetForTest()
		entitlement.ResetForTest()
	})
	permobject.Register(licverify.EditionEnterprise, contextEEFake{})

	e := newTestEnforcer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := e.AllowedOpsContext(ctx, "context-user", "resource", "r-1", []string{"view_detail"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AllowedOpsContext error = %v, want context.Canceled", err)
	}
}

func (f *structuredEEFake) Decide(_ context.Context, req permobject.Request) (permobject.LocalOpinion, error) {
	f.seen = req
	return f.opinion, nil
}

func TestEnterpriseOpinionMergesBeforeParentFallback(t *testing.T) {
	permobject.ResetForTest()
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Licensed: true, Edition: licverify.EditionEnterprise}
	}))
	t.Cleanup(func() {
		permobject.ResetForTest()
		entitlement.ResetForTest()
	})

	fake := &structuredEEFake{opinion: permobject.LocalOpinion{Direct: permobject.Allow}}
	permobject.Register(licverify.EditionEnterprise, fake)

	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	const user, role = "enterprise-user", "enterprise-role"
	mustNoErr(t, e.GrantRolePermission(role, "knowledge_network", "kn-1", "view_detail"))
	mustNoErr(t, e.AssignRole(user, role))
	mustNoErr(t, e.DenyObjectPermission(user, "knowledge_network", "kn-1", "view_detail"))

	got, err := e.LocalDecision(t.Context(), user, "knowledge_network", "kn-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, got, DecisionDeny, BasisDirect)
	if fake.seen.CoreDecision != permobject.CoreDeny || fake.seen.CoreBasis != permobject.CoreBasisDirect {
		t.Fatalf("EE saw Core decision (%q, %q)", fake.seen.CoreDecision, fake.seen.CoreBasis)
	}
	if !containsString(fake.seen.AccessorIDs, user) || !containsString(fake.seen.AccessorIDs, role) {
		t.Fatalf("EE subjects = %v, want user and transitive role", fake.seen.AccessorIDs)
	}

	// The opposite conflict is also fail-closed: an EE deny joins the same
	// local layer and overrides a Core allow.
	fake.opinion = permobject.LocalOpinion{Direct: permobject.Deny}
	const other = "enterprise-allow-user"
	mustNoErr(t, e.GrantObjectPermission(other, "knowledge_network", "kn-2", "view_detail"))
	got, err = e.LocalDecision(t.Context(), other, "knowledge_network", "kn-2", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, got, DecisionDeny, BasisDirect)

	// An EE type-wide allow is still only a fallback; a parent deny wins before
	// Core considers it.
	fake.opinion = permobject.LocalOpinion{Wildcard: permobject.Allow}
	const fallbackUser = "enterprise-wildcard-user"
	ownedBy(t, db, "enterprise-res", "enterprise-cat")
	mustNoErr(t, e.DenyObjectPermission(fallbackUser, "catalog", "enterprise-cat", "view_detail"))
	got, err = e.OperationDecision(t.Context(), fallbackUser, "resource", "enterprise-res", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, got, DecisionDeny, BasisInherited)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
