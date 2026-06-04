package directory

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"bkn-safe/internal/model"
)

// seedTree builds a 3-level org: d0(root) <- d1 <- d2; u1 in d2, u3 in d1;
// group g2 has a user member (u1) and a department member (d1). A casbin_rule
// g-row binds u1 -> role-x (so userRoles has something to read).
func seedTree(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Create(&model.User{ID: "u1", Account: "alice", Name: "Alice", Enabled: true})
	db.Create(&model.User{ID: "u3", Account: "carol", Name: "Carol", Enabled: true})
	db.Create(&model.Department{ID: "d0", Name: "总部"})
	db.Create(&model.Department{ID: "d1", Name: "研发部", ParentID: "d0"})
	db.Create(&model.Department{ID: "d2", Name: "平台组", ParentID: "d1"})
	db.Create(&model.UserDepartment{UserID: "u1", DepartmentID: "d2"})
	db.Create(&model.UserDepartment{UserID: "u3", DepartmentID: "d1"})
	db.Create(&model.Group{ID: "g2", Name: "技术组", Notes: "note"})
	db.Create(&model.GroupMember{GroupID: "g2", MemberID: "u1", MemberType: "user"})
	db.Create(&model.GroupMember{GroupID: "g2", MemberID: "d1", MemberType: "department"})
	// minimal casbin_rule table + a role binding for u1
	db.Exec("CREATE TABLE IF NOT EXISTS casbin_rule (id INTEGER PRIMARY KEY, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT)")
	db.Exec("INSERT INTO casbin_rule (ptype, v0, v1) VALUES ('g', 'u1', 'role-x')")
}

func ids(refs []DeptRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

func eq(a, b []string) bool {
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

func TestDeptChainAndTransitive(t *testing.T) {
	s, db := newSvc(t)
	seedTree(t, db)
	ctx := context.Background()

	chain, _ := s.deptChain(ctx, "d2")
	if got := ids(chain); !eq(got, []string{"d0", "d1", "d2"}) {
		t.Fatalf("deptChain(d2) = %v, want [d0 d1 d2]", got)
	}
	deptIDs, _ := s.UserDeptIDs(ctx, "u1")
	if !sameSetLocal(deptIDs, []string{"d0", "d1", "d2"}) {
		t.Fatalf("UserDeptIDs(u1) = %v, want {d0,d1,d2}", deptIDs)
	}
	pd, _ := s.UserParentDeps(ctx, "u1")
	if len(pd) != 1 || !eq(ids(pd[0]), []string{"d0", "d1", "d2"}) {
		t.Fatalf("UserParentDeps(u1) = %v", pd)
	}
	infos, _ := s.DepartmentInfos(ctx, []string{"d1", "ghost"})
	if len(infos) != 1 || !eq(ids(infos[0].ParentDeps), []string{"d0", "d1"}) {
		t.Fatalf("DepartmentInfos(d1) = %+v", infos)
	}
}

func TestGroupSplitAndUserGroups(t *testing.T) {
	s, db := newSvc(t)
	seedTree(t, db)
	ctx := context.Background()

	users, depts, _ := s.GroupMembersSplit(ctx, "g2")
	if !sameSetLocal(users, []string{"u1"}) || !sameSetLocal(depts, []string{"d1"}) {
		t.Fatalf("split = users %v depts %v", users, depts)
	}
	// u1: direct in g2 AND via dept d1 -> g2 once.
	g1, _ := s.UserGroups(ctx, "u1")
	if len(g1) != 1 || g1[0].ID != "g2" {
		t.Fatalf("UserGroups(u1) = %+v, want [g2]", g1)
	}
	// u3: in d1 only; inherits g2 via the department membership.
	g3, _ := s.UserGroups(ctx, "u3")
	if len(g3) != 1 || g3[0].ID != "g2" {
		t.Fatalf("UserGroups(u3) = %+v, want [g2] via dept", g3)
	}
}

func TestUsersDetailAndSearchOrg(t *testing.T) {
	s, db := newSvc(t)
	seedTree(t, db)
	ctx := context.Background()

	users, _ := s.UsersDetail(ctx, []string{"u1", "ghost"})
	if len(users) != 1 {
		t.Fatalf("want 1 user, got %d", len(users))
	}
	u := users[0]
	if u.Name != "Alice" || !sameSetLocal(u.Roles, []string{"role-x"}) {
		t.Fatalf("user detail = %+v", u)
	}
	if len(u.ParentDeps) != 1 || !eq(ids(u.ParentDeps[0]), []string{"d0", "d1", "d2"}) {
		t.Fatalf("parent_deps = %v", u.ParentDeps)
	}
	if len(u.Groups) != 1 || u.Groups[0].ID != "g2" {
		t.Fatalf("groups = %+v", u.Groups)
	}

	// scope d1: u1(in d2) and u3(in d1) are under it; d2 is under d1.
	mu, md, _ := s.SearchOrgFull(ctx, []string{"u1", "u3"}, []string{"d2", "d0"}, []string{"d1"})
	if !sameSetLocal(mu, []string{"u1", "u3"}) {
		t.Fatalf("search users = %v, want {u1,u3}", mu)
	}
	if !sameSetLocal(md, []string{"d2"}) {
		t.Fatalf("search depts = %v, want {d2} (d0 is an ancestor, not under d1)", md)
	}
}

func sameSetLocal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}
