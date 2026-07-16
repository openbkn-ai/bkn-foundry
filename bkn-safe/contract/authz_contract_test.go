// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// authz_contract_test.go proves the Casbin RBAC-subset model reproduces ISF
// authorization decisions on the frozen golden traffic. This validates the core
// replacement assumption: bkn-safe can back the authorization contract with
// Casbin and stay decision-for-decision compatible with ISF.
//
// The golden values below are inlined constants (this test reads no files). The
// contract-freeze spec they came from lives in bkn-docs (docs/foundry). Golden:
// dip-poc real captures — user f6ae435c, resource agent:probe:
//
//	operation-check  {accessor user, resource agent:probe, op use} -> {result:true}
//	resource-operation (allow_operation:true) -> [{id:probe, operation:[mgnt_built_in_agent, use]}]
//
// The user holds the app-admin role (1572fb82-...), which grants the agent
// resource-type ops. We model that and assert Casbin agrees with ISF.
package contract

import (
	"sort"
	"testing"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
)

// modelConf is the RBAC + resource-instance model. sub=accessorID,
// obj="type:id", act=operation. g maps user/app -> role(UUID).
//
// CONTRACT-TEST FINDING (2026-06-03): spec §4 drafted this with keyMatch2.
// That is WRONG for our "type:id" object format — keyMatch2 treats ":" as a
// named-wildcard segment (URL "/foo/:id" syntax), so a per-object policy like
// "pipeline:p1" would over-match "pipeline:p2" → privilege escalation
// (TestKeyMatch2Wildcard catches this). We use keyMatch instead: it treats
// only "*" as a wildcard and ":" as a literal char, so "agent:*" matches any
// agent id while "pipeline:p1" matches exactly that instance.
const modelConf = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && keyMatch(r.obj, p.obj) && r.act == p.act
`

// Frozen identifiers from the golden.
const (
	userF6ae     = "f6ae435c-0000-0000-0000-000000000000"
	roleAppAdmin = "1572fb82-526f-11f0-bde6-e674ec8dde71" // 应用管理员, hardcoded in DA
	otherUser    = "00000000-dead-beef-0000-000000000000"
)

// newEnforcer builds an in-memory Casbin enforcer (no DB adapter) seeded to
// mirror the golden's authorization state.
func newEnforcer(t *testing.T) *casbin.Enforcer {
	t.Helper()
	m, err := model.NewModelFromString(modelConf)
	if err != nil {
		t.Fatalf("parse model: %v", err)
	}
	e, err := casbin.NewEnforcer(m)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}

	// App-admin role grants the agent resource-type ops (RESOURCE_ID_ALL "*").
	// resource-operation golden returned exactly these two ops for agent:probe.
	if _, err := e.AddPolicy(roleAppAdmin, "agent:*", "use"); err != nil {
		t.Fatalf("add policy: %v", err)
	}
	if _, err := e.AddPolicy(roleAppAdmin, "agent:*", "mgnt_built_in_agent"); err != nil {
		t.Fatalf("add policy: %v", err)
	}
	// userF6ae holds the app-admin role.
	if _, err := e.AddGroupingPolicy(userF6ae, roleAppAdmin); err != nil {
		t.Fatalf("add grouping: %v", err)
	}

	// A direct per-object grant (CreateResources pattern): the creator gets
	// ops on one concrete pipeline instance, not the whole type.
	if _, err := e.AddPolicy(userF6ae, "pipeline:p1", "read"); err != nil {
		t.Fatalf("add policy: %v", err)
	}
	return e
}

// TestOperationCheckMatchesGolden — the exact dip-poc operation-check golden.
func TestOperationCheckMatchesGolden(t *testing.T) {
	e := newEnforcer(t)

	// Golden: {accessor user f6ae, resource agent:probe, op use} -> {result:true}
	ok, err := e.Enforce(userF6ae, "agent:probe", "use")
	if err != nil {
		t.Fatalf("enforce: %v", err)
	}
	if !ok {
		t.Fatal("operation-check(user, agent:probe, use) = false; ISF golden = true")
	}
}

// TestResourceWildcardMatch — role grant on agent:* must match a concrete
// agent id but must NOT bleed into other resource types, and a per-object
// grant must NOT leak to sibling instances. This is the test that caught the
// keyMatch2 over-match (see modelConf comment).
func TestResourceWildcardMatch(t *testing.T) {
	e := newEnforcer(t)

	cases := []struct {
		sub, obj, act string
		want          bool
		why           string
	}{
		{userF6ae, "agent:probe", "use", true, "agent:* covers agent:probe"},
		{userF6ae, "agent:anything", "use", true, "agent:* covers any agent id"},
		{userF6ae, "agent:probe", "delete", false, "role has no delete op"},
		{userF6ae, "pipeline:x", "use", false, "agent:* must NOT match pipeline type"},
		{otherUser, "agent:probe", "use", false, "user without the role is denied"},
		// direct per-object grant: only the exact instance, only that op
		{userF6ae, "pipeline:p1", "read", true, "direct grant on pipeline:p1"},
		{userF6ae, "pipeline:p2", "read", false, "direct grant must not leak to p2"},
	}
	for _, c := range cases {
		ok, err := e.Enforce(c.sub, c.obj, c.act)
		if err != nil {
			t.Fatalf("enforce(%s,%s,%s): %v", c.sub, c.obj, c.act, err)
		}
		if ok != c.want {
			t.Errorf("Enforce(%s, %s, %s) = %v, want %v — %s", c.sub, c.obj, c.act, ok, c.want, c.why)
		}
	}
}

// TestResourceOperationMatchesGolden — resource-operation with allow_operation
// returns the FULL set of allowed ops on the resource. ISF golden for
// agent:probe = ["mgnt_built_in_agent","use"]. We reproduce by enumerating the
// candidate op universe and collecting those Casbin allows, then compare sets.
func TestResourceOperationMatchesGolden(t *testing.T) {
	e := newEnforcer(t)

	// Candidate ops the caller might probe for the agent type.
	candidates := []string{"use", "mgnt_built_in_agent", "delete", "publish"}
	got := allowedOps(t, e, userF6ae, "agent:probe", candidates)

	want := []string{"mgnt_built_in_agent", "use"} // ISF golden (sorted)
	sort.Strings(got)
	if !equal(got, want) {
		t.Errorf("resource-operation(user, agent:probe) = %v, want %v (ISF golden)", got, want)
	}
}

func allowedOps(t *testing.T, e *casbin.Enforcer, sub, obj string, candidates []string) []string {
	t.Helper()
	var out []string
	for _, act := range candidates {
		ok, err := e.Enforce(sub, obj, act)
		if err != nil {
			t.Fatalf("enforce(%s,%s,%s): %v", sub, obj, act, err)
		}
		if ok {
			out = append(out, act)
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
