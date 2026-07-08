// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"testing"

	"bkn-safe/internal/authz"
	"bkn-safe/internal/model"
)

func TestListUsersFiltersAndEnrichment(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u1", "role-a"); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Role{ID: "role-a", Name: "数据分析师", Source: model.RoleSourceCustom})

	enabled := true
	users, total, err := s.ListUsers(context.Background(), UserListFilter{
		DepartmentID: "d1",
		Offset:       0,
		Limit:        10,
		Enabled:      &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(users) != 1 {
		t.Fatalf("dept filter: total=%d users=%d, want 1/1", total, len(users))
	}
	if users[0].ID != "u1" {
		t.Fatalf("user = %s, want u1", users[0].ID)
	}
	if len(users[0].DepartmentNames) != 1 || users[0].DepartmentNames[0] != "研发部" {
		t.Fatalf("department_names = %v", users[0].DepartmentNames)
	}
	if len(users[0].RoleNames) != 1 || users[0].RoleNames[0] != "数据分析师" {
		t.Fatalf("role_names = %v", users[0].RoleNames)
	}

	byRole, total, err := s.ListUsers(context.Background(), UserListFilter{
		RoleID: "role-a",
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || byRole[0].ID != "u1" {
		t.Fatalf("role filter: total=%d user=%s", total, byRole[0].ID)
	}
}

func TestListUsersSearch(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	if err := db.Model(&model.User{}).Where("id = ?", "u1").Updates(map[string]any{
		"email":     "alice@example.com",
		"telephone": "13800138000",
	}).Error; err != nil {
		t.Fatal(err)
	}

	byEmail, total, err := s.ListUsers(context.Background(), UserListFilter{Search: "alice@", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byEmail) != 1 || byEmail[0].ID != "u1" {
		t.Fatalf("search email: total=%d users=%v", total, byEmail)
	}

	byPhone, total, err := s.ListUsers(context.Background(), UserListFilter{Search: "13800", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byPhone) != 1 || byPhone[0].ID != "u1" {
		t.Fatalf("search telephone: total=%d users=%v", total, byPhone)
	}
}

func TestListAllDepartmentsMemberCount(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	items, total, err := s.ListAllDepartments(context.Background(), "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total < 2 {
		t.Fatalf("total = %d, want >= 2", total)
	}
	var d1count int64
	for _, item := range items {
		if item.ID == "d1" {
			d1count = item.MemberCount
		}
		if item.ParentID == "" && item.ID != "d1" {
			t.Errorf("unexpected root %s", item.ID)
		}
	}
	if d1count != 1 {
		t.Fatalf("d1 member_count = %d, want 1", d1count)
	}
}

func TestListAllDepartmentsSubtreeMemberCount(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	if err := db.Create(&model.UserDepartment{UserID: "u2", DepartmentID: "d2"}).Error; err != nil {
		t.Fatal(err)
	}
	items, _, err := s.ListAllDepartments(context.Background(), "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]DepartmentListItem{}
	for _, item := range items {
		counts[item.ID] = item
	}
	if counts["d1"].SubtreeMemberCount != 2 {
		t.Fatalf("d1 subtree_member_count = %d, want 2", counts["d1"].SubtreeMemberCount)
	}
	if counts["d2"].SubtreeMemberCount != 1 {
		t.Fatalf("d2 subtree_member_count = %d, want 1", counts["d2"].SubtreeMemberCount)
	}
}

func TestGetUserRoles(t *testing.T) {
	s, db := newSvc(t)
	seedDir(t, db)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u1", "role-a"); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetUser(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Roles) != 1 || d.Roles[0] != "role-a" {
		t.Fatalf("roles = %v, want [role-a]", d.Roles)
	}
}
