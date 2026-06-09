package seed

import (
	"testing"

	"bkn-safe/internal/authz"
)

// TestRoleResourceMatrix is the authz equivalence / no-leak proof: for every
// business role × every resource type, a user bound to that role is allowed a
// representative op IFF the type is in the role's agreed domain — and denied
// everywhere else (no cross-domain leakage). Super-admin allows everything; an
// unroled user allows nothing. This pins the full seeded authorization model.
func TestRoleResourceMatrix(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	const (
		appAdmin   = "1572fb82-526f-11f0-bde6-e674ec8dde71"
		dataAdmin  = "00990824-4bf7-11f0-8fa7-865d5643e61f"
		aiAdmin    = "3fb94948-5169-11f0-b662-3a7bdba2913f"
		superAdmin = "7dcfcc9c-ad02-11e8-aa06-000c29358ad6"
	)

	// agreed role -> resource-type domain.
	domain := map[string]map[string]bool{
		appAdmin:  set("agent", "agent_tpl"),
		dataAdmin: set("catalog", "resource", "connector_type", "knowledge_network", "stream_data_pipeline", "data_flow"),
		aiAdmin:   set("operator", "skill", "mcp", "tool_box", "small_model"),
	}
	// a representative, granted op per resource type (positive case uses these).
	repOp := map[string]string{
		"agent": "use", "agent_tpl": "publish",
		"stream_data_pipeline": "create", "catalog": "create", "resource": "create",
		"connector_type": "create", "knowledge_network": "create",
		"tool_box": "execute", "mcp": "execute", "operator": "execute", "skill": "execute",
		"small_model": "execute", "data_flow": "create",
	}
	allTypes := make([]string, 0, len(repOp))
	for tpe := range repOp {
		allTypes = append(allTypes, tpe)
	}

	// Each role's user; plus super-admin and an unroled user.
	roles := []string{appAdmin, dataAdmin, aiAdmin}
	for _, role := range roles {
		user := "u-" + role
		if err := e.AssignRole(user, role); err != nil {
			t.Fatal(err)
		}
		for _, tpe := range allTypes {
			want := domain[role][tpe]
			got, err := e.Check(user, tpe, "inst-1", repOp[tpe])
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("role=%s type=%s op=%s: got allow=%v, want %v (domain=%v)", role, tpe, repOp[tpe], got, want, domain[role][tpe])
			}
		}
	}

	// super-admin: allowed everything.
	su := "u-super"
	if err := e.AssignRole(su, superAdmin); err != nil {
		t.Fatal(err)
	}
	for _, tpe := range allTypes {
		if ok, _ := e.Check(su, tpe, "inst-1", repOp[tpe]); !ok {
			t.Errorf("super-admin should allow %s:%s", tpe, repOp[tpe])
		}
	}
	if ok, _ := e.Check(su, "totally", "x", "unknown_op"); !ok {
		t.Error("super-admin should allow any type/op")
	}

	// unroled user: allowed nothing.
	for _, tpe := range allTypes {
		if ok, _ := e.Check("u-nobody", tpe, "inst-1", repOp[tpe]); ok {
			t.Errorf("unroled user must be denied %s:%s", tpe, repOp[tpe])
		}
	}
}

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
