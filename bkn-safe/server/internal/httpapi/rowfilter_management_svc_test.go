// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestRowFilterManagementAuthorityUsesKnowledgeNetworkRoot(t *testing.T) {
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
	services := &rowFilterManagementServices{enforcer: enforcer, db: db}
	seedKnowledgeNetworkObjectAuthority(t, db, enforcer)
	if err := enforcer.GrantObjectPermission("role-viewer", "admin-role", "*", "view"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := services.CanInspectRoleMembership(t.Context(), "owner"); err != nil || allowed {
		t.Fatalf("owner role membership inspection = (%t, %v), want denied", allowed, err)
	}
	if allowed, err := services.CanInspectRoleMembership(t.Context(), "role-viewer"); err != nil || !allowed {
		t.Fatalf("platform role viewer membership inspection = (%t, %v), want allowed", allowed, err)
	}
	if err := db.Create(&model.User{ID: "enabled-user", Account: "enabled-user", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "disabled-user", Account: "disabled-user", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Role{ID: "existing-role", Name: "Existing role", Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}
	for _, subject := range []struct {
		typeName string
		id       string
		want     bool
	}{
		{typeName: "user", id: "enabled-user", want: true},
		{typeName: "user", id: "disabled-user", want: false},
		{typeName: "role", id: "existing-role", want: true},
		{typeName: "role", id: "missing-role", want: false},
	} {
		exists, existsErr := services.GrantSubjectExists(t.Context(), subject.typeName, subject.id)
		if existsErr != nil || exists != subject.want {
			t.Fatalf("subject %s:%s exists = (%t, %v), want %t", subject.typeName, subject.id, exists, existsErr, subject.want)
		}
	}
	for _, check := range []struct {
		name string
		fn   func(string, string) (bool, error)
	}{
		{name: "user read", fn: func(operator, ref string) (bool, error) {
			return services.AuthorizeUserRead(t.Context(), operator, ref)
		}},
		{name: "user write", fn: func(operator, ref string) (bool, error) {
			return services.AuthorizeUserWrite(t.Context(), operator, ref)
		}},
		{name: "role read", fn: func(operator, ref string) (bool, error) {
			return services.AuthorizeRoleRead(t.Context(), operator, ref)
		}},
		{name: "role write", fn: func(operator, ref string) (bool, error) {
			return services.AuthorizeRoleWrite(t.Context(), operator, ref)
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			allowed, err := check.fn("owner", "kn-1/customer")
			if err != nil || !allowed {
				t.Fatalf("visible child in owned network = (%t, %v), want allowed", allowed, err)
			}
			allowed, err = check.fn("owner", "kn-2/order")
			if err != nil || allowed {
				t.Fatalf("child in another network = (%t, %v), want denied", allowed, err)
			}
			allowed, err = check.fn("owner", "kn-1/hidden")
			if err != nil || allowed {
				t.Fatalf("hidden child in owned network = (%t, %v), want denied", allowed, err)
			}
		})
	}
}
