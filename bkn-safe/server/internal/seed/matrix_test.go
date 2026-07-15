// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"testing"

	"bkn-safe/internal/authz"
)

// TestRoleResourceMatrix is the authz equivalence / no-leak proof: default
// business roles only get their intended resource/action domain. Super-admin
// allows everything; an unroled user allows nothing. This pins the full seeded
// authorization model.
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
		networkBuilder = "1572fb82-526f-11f0-bde6-e674ec8dde71"
		normalUser     = "b5f9ac3e-992c-4bbd-8126-95e87e51c46e"
		superAdmin     = "7dcfcc9c-ad02-11e8-aa06-000c29358ad6"
	)

	// a representative, granted op per resource type (positive case uses these).
	repOp := map[string]string{
		"agent": "use", "agent_tpl": "publish",
		"stream_data_pipeline": "create", "catalog": "create", "resource": "create",
		"connector_type": "create", "knowledge_network": "create",
		"tool_box": "execute", "mcp": "execute", "operator": "execute", "skill": "execute",
		"small_model": "execute",
	}
	roleAllowed := map[string]map[string]string{
		networkBuilder: repOp,
		normalUser: {
			"agent":             "use",
			"catalog":           "view_detail",
			"resource":          "view_detail",
			"knowledge_network": "data_query",
			"tool_box":          "execute",
			"mcp":               "execute",
			"operator":          "execute",
			"skill":             "execute",
			"small_model":       "execute",
		},
	}
	allTypes := make([]string, 0, len(repOp))
	for tpe := range repOp {
		allTypes = append(allTypes, tpe)
	}

	// Each role's user; plus super-admin and an unroled user.
	roles := []string{networkBuilder, normalUser}
	for _, role := range roles {
		user := "u-" + role
		if err := e.AssignRole(user, role); err != nil {
			t.Fatal(err)
		}
		for _, tpe := range allTypes {
			op, want := roleAllowed[role][tpe]
			if !want {
				op = repOp[tpe]
			}
			got, err := e.Check(user, tpe, "inst-1", op)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("role=%s type=%s op=%s: got allow=%v, want %v", role, tpe, op, got, want)
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
