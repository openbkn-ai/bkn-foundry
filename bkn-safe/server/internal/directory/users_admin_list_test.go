// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
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

func TestListUsersHidesManagedProxyAccounts(t *testing.T) {
	s, db := newSvc(t)
	if err := db.Create(&model.User{ID: "u-visible", Account: "visible", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	proxy, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-hidden",
	})
	if err != nil {
		t.Fatal(err)
	}

	users, total, err := s.ListUsers(t.Context(), UserListFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(users) != 1 || users[0].ID != "u-visible" {
		t.Fatalf("ListUsers() = total %d, users %+v; proxy %q must stay hidden", total, users, proxy.ProxyAccountID)
	}
	if _, err := s.GetUser(t.Context(), proxy.ProxyAccountID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("GetUser(proxy) error = %v, want not found", err)
	}
	if err := s.SetUserDepartments(t.Context(), proxy.ProxyAccountID, nil); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("SetUserDepartments(proxy) error = %v, want not found", err)
	}
}

func TestManagedProxyIsHiddenFromDirectoryMembershipSurfaces(t *testing.T) {
	s, db := newSvc(t)
	proxy, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-hidden-membership",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Department{ID: "d-proxy", Name: "Hidden"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Group{ID: "g-proxy", Name: "Hidden"}).Error; err != nil {
		t.Fatal(err)
	}
	// Simulate legacy/corrupt memberships created before the managed-account
	// guards existed. Read paths still must not expose the proxy.
	if err := db.Create(&model.UserDepartment{UserID: proxy.ProxyAccountID, DepartmentID: "d-proxy"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.GroupMember{
		GroupID: "g-proxy", MemberID: proxy.ProxyAccountID, MemberType: "user",
	}).Error; err != nil {
		t.Fatal(err)
	}

	for name, resolve := range map[string]func(context.Context, []string) ([]NamedRef, error){
		"user names":      s.ResolveUserNames,
		"app names":       s.ResolveAppNames,
		"contactor names": s.ResolveContactorNames,
	} {
		t.Run(name, func(t *testing.T) {
			refs, err := resolve(t.Context(), []string{proxy.ProxyAccountID})
			if err != nil || len(refs) != 0 {
				t.Fatalf("resolve proxy = (%+v, %v), want empty", refs, err)
			}
		})
	}
	if user, err := s.FindUserByAccount(t.Context(), "bkn-proxy-"+proxy.ProxyAccountID); !errors.Is(err, gorm.ErrRecordNotFound) || user != nil {
		t.Fatalf("FindUserByAccount(proxy) = (%+v, %v), want not found", user, err)
	}
	if members, err := s.DepartmentMembers(t.Context(), "d-proxy"); err != nil || len(members) != 0 {
		t.Fatalf("DepartmentMembers() = (%+v, %v), want empty", members, err)
	}
	if members, err := s.GroupMembers(t.Context(), "g-proxy"); err != nil || len(members) != 0 {
		t.Fatalf("GroupMembers() = (%+v, %v), want empty", members, err)
	}
	if users, _, err := s.GroupMembersSplit(t.Context(), "g-proxy"); err != nil || len(users) != 0 {
		t.Fatalf("GroupMembersSplit() = (%+v, %v), want no users", users, err)
	}
	if ids, err := s.UserDeptIDs(t.Context(), proxy.ProxyAccountID); err != nil || len(ids) != 0 {
		t.Fatalf("UserDeptIDs(proxy) = (%+v, %v), want empty", ids, err)
	}
	if users, err := s.UsersDetail(t.Context(), []string{proxy.ProxyAccountID}); err != nil || len(users) != 0 {
		t.Fatalf("UsersDetail(proxy) = (%+v, %v), want empty", users, err)
	}
	if users, _, err := s.SearchOrgFull(t.Context(), []string{proxy.ProxyAccountID}, nil, []string{"d-proxy"}); err != nil || len(users) != 0 {
		t.Fatalf("SearchOrgFull(proxy) = (%+v, %v), want empty", users, err)
	}
	deps, _, err := s.ListAllDepartments(t.Context(), "", 0, 10)
	if err != nil || len(deps) != 1 || deps[0].MemberCount != 0 || deps[0].SubtreeMemberCount != 0 {
		t.Fatalf("department counts expose proxy membership: (%+v, %v)", deps, err)
	}

	if err := s.RemoveDepartmentMembers(t.Context(), "d-proxy", []string{proxy.ProxyAccountID}); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("RemoveDepartmentMembers(proxy) error = %v, want ErrUnknownUser", err)
	}
	var count int64
	if err := db.Model(&model.UserDepartment{}).
		Where("user_id = ? AND department_id = ?", proxy.ProxyAccountID, "d-proxy").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("generic removal changed proxy membership, rows = %d", count)
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
