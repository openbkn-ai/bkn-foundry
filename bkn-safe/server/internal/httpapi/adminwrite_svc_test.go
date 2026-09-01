// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestAdminWriteRejectsTypeWideActionExecute(t *testing.T) {
	_, e, db := newTestServer(t)
	const roleID = "custom-action-role"
	if err := db.Create(&model.Role{ID: roleID, Name: roleID, Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatalf("create custom role: %v", err)
	}
	svc := newAdminWriteServices(e, db)

	for _, resourceID := range []string{"*", "kn-1/*"} {
		err := svc.GrantRolePermission(t.Context(), roleID, "action_type", resourceID, "execute")
		if !errors.Is(err, adminwrite.ErrTypeWideActionExecute) {
			t.Errorf("resource id %q: error = %v, want ErrTypeWideActionExecute", resourceID, err)
		}
	}
	if err := svc.GrantRolePermission(t.Context(), roleID, "action_type", "kn-1/action-1", "execute"); err != nil {
		t.Fatalf("concrete action execution grant: %v", err)
	}
	if ok, err := e.Check("role-member", "action_type", "kn-1/action-1", "execute"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("unbound accessor unexpectedly received the role grant")
	}
	if err := e.AssignRole("role-member", roleID); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.Check("role-member", "action_type", "kn-1/action-1", "execute"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("concrete action execution role grant must be effective")
	}
}
