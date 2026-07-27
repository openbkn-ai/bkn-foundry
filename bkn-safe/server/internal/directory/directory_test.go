// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func newSvc(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := authz.New(db); err != nil {
		t.Fatalf("authz: %v", err)
	}
	return New(db), db
}

func seedDir(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Create(&model.User{ID: "u1", Account: "alice", Name: "Alice", Enabled: true, AccountType: model.AccountTypeOther})
	db.Create(&model.User{ID: "u2", Account: "bob", Name: "Bob", Enabled: true})
	db.Create(&model.User{ID: "app1", Account: "svc-app", Name: "服务应用", Enabled: true, AccountType: model.AccountTypeApp})
	db.Create(&model.User{ID: "c1", Account: "contact", Name: "联系人甲", Enabled: true, AccountType: model.AccountTypeContactor})
	db.Create(&model.Department{ID: "d1", Name: "研发部"})
	db.Create(&model.Department{ID: "d2", Name: "测试组", ParentID: "d1"})
	db.Create(&model.Group{ID: "g1", Name: "管理员组"})
	db.Create(&model.UserDepartment{UserID: "u1", DepartmentID: "d1"})
	db.Create(&model.GroupMember{GroupID: "g1", MemberID: "u1", MemberType: "user"})
}

func TestGetUser(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	d, err := s.GetUser(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Alice" || d.AccountType != "other" {
		t.Errorf("got %+v", d)
	}
	if len(d.Departments) != 1 || d.Departments[0] != "d1" {
		t.Errorf("departments = %v, want [d1]", d.Departments)
	}
	if _, err := s.GetUser(context.Background(), "missing"); err == nil {
		t.Error("expected error for missing user")
	}
}

func TestResolveNames(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	names, err := s.ResolveUserNames(context.Background(), []string{"u1", "u2", "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	// unknown id "ghost" is omitted (clean contract returns what it finds)
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2: %v", len(names), names)
	}
	byID := map[string]string{}
	for _, n := range names {
		byID[n.ID] = n.Name
	}
	if byID["u1"] != "Alice" || byID["u2"] != "Bob" {
		t.Errorf("resolved = %v", byID)
	}

	depNames, _ := s.ResolveDepartmentNames(context.Background(), []string{"d1"})
	if len(depNames) != 1 || depNames[0].Name != "研发部" {
		t.Errorf("dept names = %v", depNames)
	}

	// app accounts and contactors are User rows (account_type), resolved by id.
	appNames, _ := s.ResolveAppNames(context.Background(), []string{"app1"})
	if len(appNames) != 1 || appNames[0].Name != "服务应用" {
		t.Errorf("app names = %v", appNames)
	}
	contactorNames, _ := s.ResolveContactorNames(context.Background(), []string{"c1"})
	if len(contactorNames) != 1 || contactorNames[0].Name != "联系人甲" {
		t.Errorf("contactor names = %v", contactorNames)
	}
}

func TestListDepartmentsAndMembers(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	roots, _ := s.ListDepartments(context.Background(), "")
	if len(roots) != 1 || roots[0].ID != "d1" {
		t.Errorf("roots = %v", roots)
	}
	children, _ := s.ListDepartments(context.Background(), "d1")
	if len(children) != 1 || children[0].ID != "d2" {
		t.Errorf("children = %v", children)
	}
	members, _ := s.GroupMembers(context.Background(), "g1")
	if len(members) != 1 || members[0] != "u1" {
		t.Errorf("members = %v", members)
	}
}

func TestSearchOrg(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	// u1 is in d1; u2 is in none.
	got, err := s.SearchOrg(context.Background(), []string{"u1", "u2"}, []string{"d1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "u1" {
		t.Errorf("search-org = %v, want [u1]", got)
	}
	// empty scope -> empty result
	if got, _ := s.SearchOrg(context.Background(), []string{"u1"}, nil); len(got) != 0 {
		t.Errorf("empty scope should yield none, got %v", got)
	}
}
