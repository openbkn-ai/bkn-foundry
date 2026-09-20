// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rowfilter

import (
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func newResolver(t *testing.T) (*TrustedCallerResolver, *gorm.DB, *authz.Enforcer) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	return NewTrustedCallerResolver(directory.New(db), enforcer), db, enforcer
}

func TestResolveUsesOnlyDirectoryAndTransitiveRoleData(t *testing.T) {
	resolver, db, enforcer := newResolver(t)
	db.Create(&model.User{ID: "user-1", Account: "alice", Enabled: true})
	db.Create(&model.Department{ID: "d-root", Name: "总部"})
	db.Create(&model.Department{ID: "d-sales", Name: "销售", ParentID: "d-root"})
	db.Create(&model.Department{ID: "d-east", Name: "华东", ParentID: "d-sales"})
	db.Create(&model.UserDepartment{UserID: "user-1", DepartmentID: "d-sales"})
	if err := enforcer.AssignRole("user-1", "sales"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AssignRole("sales", "regional"); err != nil {
		t.Fatal(err)
	}

	caller, err := resolver.Resolve(t.Context(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if caller.UserID != "user-1" {
		t.Fatalf("user id = %q", caller.UserID)
	}
	if !sameStrings(caller.RoleIDs, []string{"regional", "sales"}) {
		t.Fatalf("roles = %v", caller.RoleIDs)
	}
	if !sameStrings(caller.DirectDepartmentIDs, []string{"d-sales"}) {
		t.Fatalf("direct departments = %v", caller.DirectDepartmentIDs)
	}
	if !sameStrings(caller.DepartmentTreeIDs, []string{"d-east", "d-sales"}) {
		t.Fatalf("department tree = %v", caller.DepartmentTreeIDs)
	}
}

func TestResolveFailsClosedForUnknownDisabledAndAppCallers(t *testing.T) {
	resolver, db, _ := newResolver(t)
	db.Create(&model.User{ID: "disabled", Account: "disabled", Enabled: false})
	db.Create(&model.User{ID: "app", Account: "app", Enabled: true, AccountType: model.AccountTypeApp})
	for _, id := range []string{"missing", "disabled", "app"} {
		if _, err := resolver.Resolve(t.Context(), id); !errors.Is(err, ErrCallerUnavailable) {
			t.Fatalf("Resolve(%q) error = %v, want ErrCallerUnavailable", id, err)
		}
	}
}

func sameStrings(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
