// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

func newPropertyGrantServicesForTest(t *testing.T) (*propertyGrantManagementServices, *authz.Enforcer) {
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
	return &propertyGrantManagementServices{enforcer: enforcer}, enforcer
}

func TestPropertyGrantManagementAuthoritySeparatesPlatformAndObjectScopes(t *testing.T) {
	services, enforcer := newPropertyGrantServicesForTest(t)
	if err := enforcer.GrantObjectPermission("security-admin", "admin-authz", "*", "grant"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("security-admin", "admin-authz", "*", "revoke"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("grant-only", "admin-authz", "*", "grant"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("owner", "object_type", "kn-1/customer", opAuthorize); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("role-admin", "admin-role", "*", "permissions"); err != nil {
		t.Fatal(err)
	}

	platform, err := services.AuthorizeUserGrants(t.Context(), "security-admin", "kn-1/customer")
	if err != nil || !platform.Allowed || !platform.Unrestricted {
		t.Fatalf("platform authority = %+v, err=%v", platform, err)
	}
	grantOnly, err := services.AuthorizeUserGrants(t.Context(), "grant-only", "kn-1/customer")
	if err != nil || grantOnly.Allowed {
		t.Fatalf("grant-only authority = %+v, err=%v", grantOnly, err)
	}
	owner, err := services.AuthorizeUserGrants(t.Context(), "owner", "kn-1/customer")
	if err != nil || !owner.Allowed || owner.Unrestricted {
		t.Fatalf("owner authority = %+v, err=%v", owner, err)
	}
	other, err := services.AuthorizeUserGrants(t.Context(), "owner", "kn-1/order")
	if err != nil || other.Allowed {
		t.Fatalf("other-object authority = %+v, err=%v", other, err)
	}
	roleAllowed, err := services.AuthorizePlatformRoleGrants(t.Context(), "role-admin")
	if err != nil || !roleAllowed {
		t.Fatalf("role admin allowed = %v, err=%v", roleAllowed, err)
	}
	ownerRoleAllowed, err := services.AuthorizePlatformRoleGrants(t.Context(), "owner")
	if err != nil || ownerRoleAllowed {
		t.Fatalf("object owner role authority = %v, err=%v", ownerRoleAllowed, err)
	}
}

type effectiveLevelResolver struct{}

func (effectiveLevelResolver) Resolve(_ context.Context, request permdata.Request) (permdata.Resolution, error) {
	return permdata.Resolution{Entries: []permdata.ResolutionEntry{{
		ObjectTypeRef: request.Items[0].ObjectTypeRef,
		Properties: []permdata.ResolvedProperty{
			{Name: request.Items[0].Properties[0], Level: propertyaccess.Full, Explicit: true},
			{Name: request.Items[0].Properties[1], Level: propertyaccess.Masked, Explicit: true},
		},
	}}}, nil
}

func TestEffectivePropertyLevelsUsesRuntimeBaseClamp(t *testing.T) {
	services, enforcer := newPropertyGrantServicesForTest(t)
	if err := enforcer.GrantObjectPermission("owner", "object_type", "kn-1/customer", "view_detail"); err != nil {
		t.Fatal(err)
	}
	permdata.ResetForTest()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(func() {
		permdata.ResetForTest()
		entitlement.ResetForTest()
	})
	permdata.Register(licverify.EditionEnterprise, effectiveLevelResolver{})

	levels, err := services.EffectivePropertyLevels(t.Context(), "owner", "kn-1/customer", []string{"name", "mobile"})
	if err != nil {
		t.Fatal(err)
	}
	if levels["name"] != propertyaccess.Schema {
		t.Fatalf("name level = %s, want schema (full extension level clamped by base)", levels["name"])
	}
	if levels["mobile"] != propertyaccess.Schema {
		t.Fatalf("mobile level = %s, want schema (masked extension level clamped by base)", levels["mobile"])
	}
}
