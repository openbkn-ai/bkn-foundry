// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package identity

import (
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func TestImportRoleKeepsIdentityAndRejectsLocalCollisions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Role{}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ name, description string }{{" Analyst ", "first"}, {"Analyst", "updated"}} {
		if err := ImportRole(db, "remote", input.name, input.description); err != nil {
			t.Fatal(err)
		}
	}
	var role model.Role
	if err := db.First(&role, "id = ?", "remote").Error; err != nil {
		t.Fatal(err)
	}
	if role.Name != "Analyst" || role.Description != "updated" || role.Source != model.RoleSourceCustom {
		t.Fatalf("role: %+v", role)
	}
	if err := ImportRole(db, "other", "analyst", ""); !errors.Is(err, adminwrite.ErrRoleNameExisted) {
		t.Fatalf("collision: %v", err)
	}
	if err := db.Create(&model.Role{ID: "system", Name: "System", Source: model.RoleSourceSystem}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ImportRole(db, "system", "Changed", ""); !errors.Is(err, adminwrite.ErrImmutable) {
		t.Fatalf("system: %v", err)
	}
}
