// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"sort"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// TestSeededRolesGainNothingFromInheritance is the upgrade guarantee for #800,
// asserted against the real seed rather than argued: turning inheritance on must
// not widen what any SEEDED role can do to a data table.
//
// It holds today because of how the two roles are already granted —
// network_builder holds every inheritable operation on resource:* directly, and
// normal_user's catalog grant (view_detail) is one it already holds on
// resource:* too. Neither fact is obvious from reading grants.json, and either
// could be broken by a future edit that looks harmless, which is why this is a
// test and not a comment.
//
// The residual widening lives in per-object catalog grants made through the
// admin console, which no seed file can predict.
func TestSeededRolesGainNothingFromInheritance(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	// owned sits in a catalog; orphan has no recorded parent, so it is judged the
	// way the previous release judged everything. Comparing the two isolates the
	// inheritance contribution exactly.
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "resource", ResourceID: "owned",
		ParentTypeID: "catalog", ParentID: "cat-1",
	}).Error; err != nil {
		t.Fatal(err)
	}

	candidates := []string{"view_detail", "query_data", "modify", "delete", "authorize", "task_manage"}
	var roles []model.Role
	if err := db.Find(&roles).Error; err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		t.Fatal("no seeded roles found")
	}
	for _, role := range roles {
		user := "u-" + role.ID
		if err := e.AssignRole(user, role.ID); err != nil {
			t.Fatal(err)
		}
		withParent, err := e.AllowedOps(user, "resource", "owned", candidates)
		if err != nil {
			t.Fatal(err)
		}
		withoutParent, err := e.AllowedOps(user, "resource", "orphan", candidates)
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(withParent)
		sort.Strings(withoutParent)
		if strings.Join(withParent, ",") != strings.Join(withoutParent, ",") {
			t.Errorf("role %s (%s) gains %v from inheritance, but held %v before — "+
				"a seeded grant now reaches further than it did, which an upgrade must not do",
				role.Name, role.ID, withParent, withoutParent)
		}
	}
}
