// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestAdminWriteRoleIdentifierIsUniqueAfterNormalization(t *testing.T) {
	_, e, db := newTestServer(t)
	svc := newAdminWriteServices(e, db)

	firstID, err := svc.CreateRole(t.Context(), adminwrite.RoleSpec{Name: " Same ", Description: "first"})
	if err != nil {
		t.Fatalf("create first role: %v", err)
	}
	if _, err := svc.CreateRole(t.Context(), adminwrite.RoleSpec{Name: "same"}); !errors.Is(err, adminwrite.ErrRoleNameExisted) {
		t.Fatalf("duplicate create error = %v, want ErrRoleNameExisted", err)
	} else {
		var conflict interface{ ExistingRoleID() string }
		if !errors.As(err, &conflict) || conflict.ExistingRoleID() != firstID {
			t.Fatalf("duplicate create existing ID = %v, want %q", conflict, firstID)
		}
	}

	secondID, err := svc.CreateRole(t.Context(), adminwrite.RoleSpec{Name: "other"})
	if err != nil {
		t.Fatalf("create second role: %v", err)
	}
	conflictingName := " SAME "
	if err := svc.UpdateRole(t.Context(), secondID, adminwrite.RolePatch{Name: &conflictingName}); !errors.Is(err, adminwrite.ErrRoleNameExisted) {
		t.Fatalf("duplicate rename error = %v, want ErrRoleNameExisted", err)
	}
	unchangedName := " same "
	if err := svc.UpdateRole(t.Context(), firstID, adminwrite.RolePatch{Name: &unchangedName}); err != nil {
		t.Fatalf("rename to own normalized name: %v", err)
	}
}

func TestRoleNameKeyUniqueIndexRejectsConcurrentWriterEquivalent(t *testing.T) {
	_, _, db := newTestServer(t)
	key := "same"
	if err := db.Create(&model.Role{ID: "first", Name: "same", NameKey: &key, Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatalf("create first role: %v", err)
	}
	if err := db.Create(&model.Role{ID: "second", Name: "SAME", NameKey: &key, Source: model.RoleSourceCustom}).Error; err == nil {
		t.Fatal("unique name key allowed duplicate role")
	} else if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("duplicate insert error = %v, want unique constraint", err)
	}
}

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

func TestAdminWriteRejectsCustomModelRolePermissions(t *testing.T) {
	_, e, db := newTestServer(t)
	const roleID = "custom-model-role"
	if err := db.Create(&model.Role{ID: roleID, Name: roleID, Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatalf("create custom role: %v", err)
	}
	svc := newAdminWriteServices(e, db)
	for _, resourceType := range []string{"small_model", "large_model"} {
		err := svc.GrantRolePermission(t.Context(), roleID, resourceType, "*", "modify")
		if !errors.Is(err, errModelAuthorizationManagedBySystem) {
			t.Errorf("GrantRolePermission(%s) error = %v, want model authorization rejection", resourceType, err)
		}
	}
}

func TestAdminWriteRejectsNonGrantableRolePermission(t *testing.T) {
	_, e, db := newTestServer(t)
	const roleID = "custom-report-role"
	notGrantable := false
	if err := db.Create(&model.Role{ID: roleID, Name: roleID, Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceType{ID: "report", Name: "Report"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{
		ResourceTypeID: "report", ID: "summary", Name: "Summary", Grantable: &notGrantable,
	}).Error; err != nil {
		t.Fatal(err)
	}

	err := newAdminWriteServices(e, db).GrantRolePermission(t.Context(), roleID, "report", "r-1", "summary")
	if !errors.Is(err, adminwrite.ErrInvalid) {
		t.Fatalf("GrantRolePermission error = %v, want ErrInvalid", err)
	}
	grants, err := e.RolePermissions(roleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Fatalf("non-grantable role permission persisted: %+v", grants)
	}
}
