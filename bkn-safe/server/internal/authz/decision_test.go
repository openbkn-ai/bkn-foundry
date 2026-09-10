// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permobject"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

func TestProxyProvenanceSkipsDatabaseWhenEveryDecisionIsDenied(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	var queries atomic.Int64
	const callback = "test:denied-proxy-provenance-query-count"
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(*gorm.DB) {
		queries.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })

	decisions := map[ResourceRef]map[string]Evaluation{
		{Type: "resource", ID: "r-1"}: {
			"view_detail": {Scope: ScopeEffective, Decision: DecisionDeny, Basis: BasisDefault},
		},
	}
	if err := e.applyManagedProxyProvenanceToBatch(t.Context(), "u-denied", decisions); err != nil {
		t.Fatal(err)
	}
	if got := queries.Load(); got != 0 {
		t.Fatalf("denied provenance queries = %d, want 0", got)
	}
}

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

func TestOperationRequiresIsEnforcedAcrossFinalEntryPoints(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	if err := db.Create(&[]model.Operation{
		{ResourceTypeID: "document", ID: "view", Name: "view"},
		{ResourceTypeID: "document", ID: "modify", Name: "modify", RequiredOperationIDs: "view"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	const user, allowRole, denyRole, resource = "requires-user", "document-editor", "document-hidden", "doc-1"
	mustNoErr(t, e.GrantRolePermission(allowRole, "document", resource, "modify"))
	mustNoErr(t, e.GrantRolePermission(allowRole, "document", resource, "view"))
	mustNoErr(t, e.DenyObjectPermission(denyRole, "document", resource, "view"))
	mustNoErr(t, e.AssignRole(user, allowRole))
	mustNoErr(t, e.AssignRole(user, denyRole))

	decision, err := e.OperationDecision(t.Context(), user, "document", resource, "modify")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, decision, DecisionDeny, BasisRequires)
	if decision.DeniedRequirement != "view" || decision.RequirementBasis != BasisDirect {
		t.Fatalf("requires reason = %+v", decision)
	}
	if len(decision.Requirements) != 1 || decision.Requirements[0] != "view" {
		t.Fatalf("requirements = %v, want [view]", decision.Requirements)
	}

	allowed, err := e.AllowedOps(user, "document", resource, []string{"modify", "view"})
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 0 {
		t.Fatalf("AllowedOps = %v, want no final operations", allowed)
	}

	filtered, err := e.FilterResourceOps(user,
		[]ResourceRef{{Type: "document", ID: resource}}, nil, []string{"modify"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || len(filtered[0].Operations) != 0 ||
		len(filtered[0].Decisions) != 1 || filtered[0].Decisions[0].Basis != BasisRequires {
		t.Fatalf("FilterResourceOps = %+v", filtered)
	}

	// Removing the later prerequisite deny restores the untouched modify allow;
	// no authorization rewrite is needed.
	if _, err := e.RemoveAccessorResourcePoliciesForEffect(denyRole, "document", resource, EffectDeny); err != nil {
		t.Fatal(err)
	}
	decision, err = e.OperationDecision(t.Context(), user, "document", resource, "modify")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, decision, DecisionAllow, BasisDirect)
}

func TestLocalDecisionReportsButDoesNotExecuteRequirements(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	if err := db.Create(&[]model.Operation{
		{ResourceTypeID: "catalog", ID: "view_detail"},
		{ResourceTypeID: "catalog", ID: "resource_manage", RequiredOperationIDs: "view_detail"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	const user, resource = "local-requires-user", "catalog-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", resource, "resource_manage"))
	mustNoErr(t, e.DenyObjectPermission(user, "catalog", resource, "view_detail"))

	local, err := e.LocalDecision(t.Context(), user, "catalog", resource, "resource_manage")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, local, DecisionAllow, BasisDirect)
	if len(local.Requirements) != 1 || local.Requirements[0] != "view_detail" {
		t.Fatalf("local requirements = %v", local.Requirements)
	}

	batch, err := e.FilterResourceOpsScoped(t.Context(), user,
		[]ResourceRef{{Type: "catalog", ID: resource}}, nil, []string{"resource_manage"}, ScopeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 || len(batch[0].Decisions) != 2 {
		t.Fatalf("local batch = %+v", batch)
	}
	byOperation := map[string]OperationDecision{}
	for _, decision := range batch[0].Decisions {
		byOperation[decision.Operation] = decision
	}
	if byOperation["resource_manage"].Decision != DecisionAllow ||
		byOperation["view_detail"].Decision != DecisionDeny {
		t.Fatalf("local decisions = %+v", byOperation)
	}
}

func TestOperationsWithoutDeclaredRequirementsStayIndependent(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	operations := []string{"query_data", "execute", "use", "create", "public_access"}
	rows := make([]model.Operation, 0, len(operations)+1)
	rows = append(rows, model.Operation{ResourceTypeID: "tool", ID: "view"})
	for _, operation := range operations {
		rows = append(rows, model.Operation{ResourceTypeID: "tool", ID: operation})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	const user = "independent-user"
	mustNoErr(t, e.DenyObjectPermission(user, "tool", "tool-1", "view"))
	for _, operation := range operations {
		mustNoErr(t, e.GrantObjectPermission(user, "tool", "tool-1", operation))
		decision, err := e.OperationDecision(t.Context(), user, "tool", "tool-1", operation)
		if err != nil {
			t.Fatal(err)
		}
		requireDecision(t, decision, DecisionAllow, BasisDirect)
		if len(decision.Requirements) != 0 {
			t.Fatalf("%s unexpectedly requires %v", operation, decision.Requirements)
		}
	}
}

func TestOperationSupportsMultipleDirectRequirements(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	if err := db.Create(&[]model.Operation{
		{ResourceTypeID: "release", ID: "view"},
		{ResourceTypeID: "release", ID: "approve"},
		{ResourceTypeID: "release", ID: "publish", RequiredOperationIDs: "view,approve"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	const user = "publisher"
	for _, operation := range []string{"publish", "view", "approve"} {
		mustNoErr(t, e.GrantObjectPermission(user, "release", "release-1", operation))
	}
	mustNoErr(t, e.DenyObjectPermission(user, "release", "release-1", "approve"))

	decision, err := e.OperationDecision(t.Context(), user, "release", "release-1", "publish")
	if err != nil {
		t.Fatal(err)
	}
	requireDecision(t, decision, DecisionDeny, BasisRequires)
	if decision.DeniedRequirement != "approve" || len(decision.Requirements) != 2 ||
		decision.Requirements[0] != "view" || decision.Requirements[1] != "approve" {
		t.Fatalf("multi-requirement decision = %+v", decision)
	}
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
