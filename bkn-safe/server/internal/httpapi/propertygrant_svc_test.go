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
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
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
	return &propertyGrantManagementServices{enforcer: enforcer, db: db}, enforcer
}

func seedKnowledgeNetworkObjectAuthority(t *testing.T, db *gorm.DB, enforcer *authz.Enforcer) {
	t.Helper()
	if err := db.Create(&model.ResourceType{ID: "knowledge_network", Name: "Knowledge network"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceType{
		ID: "object_type", Name: "Object type", ParentTypeID: "knowledge_network",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{
		ResourceTypeID: "object_type", ID: "view_detail", Name: "View detail",
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, parent := range []model.ResourceParent{
		{ResourceTypeID: "object_type", ResourceID: "kn-1/customer", ParentTypeID: "knowledge_network", ParentID: "kn-1"},
		{ResourceTypeID: "object_type", ResourceID: "kn-1/hidden", ParentTypeID: "knowledge_network", ParentID: "kn-1"},
		{ResourceTypeID: "object_type", ResourceID: "kn-2/order", ParentTypeID: "knowledge_network", ParentID: "kn-2"},
	} {
		if err := db.Create(&parent).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := enforcer.GrantObjectPermission("owner", "knowledge_network", "kn-1", opAuthorize); err != nil {
		t.Fatal(err)
	}
	for _, objectTypeRef := range []string{"kn-1/customer", "kn-2/order"} {
		if err := enforcer.GrantObjectPermission("owner", "object_type", objectTypeRef, "view_detail"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPropertyGrantManagementAuthoritySeparatesPlatformAndObjectScopes(t *testing.T) {
	services, enforcer := newPropertyGrantServicesForTest(t)
	seedKnowledgeNetworkObjectAuthority(t, services.db, enforcer)
	if err := enforcer.GrantObjectPermission("security-admin", "admin-authz", "*", "grant"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("security-admin", "admin-authz", "*", "revoke"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("grant-only", "admin-authz", "*", "grant"); err != nil {
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
	other, err := services.AuthorizeUserGrants(t.Context(), "owner", "kn-2/order")
	if err != nil || other.Allowed {
		t.Fatalf("other-object authority = %+v, err=%v", other, err)
	}
	hidden, err := services.AuthorizeUserGrants(t.Context(), "owner", "kn-1/hidden")
	if err != nil || hidden.Allowed {
		t.Fatalf("hidden child authority = %+v, err=%v", hidden, err)
	}
	roleAuthority, err := services.AuthorizeRoleGrants(t.Context(), "role-admin", "kn-1/customer")
	if err != nil || !roleAuthority.Allowed || !roleAuthority.Unrestricted {
		t.Fatalf("role admin authority = %+v, err=%v", roleAuthority, err)
	}
	ownerRoleAuthority, err := services.AuthorizeRoleGrants(t.Context(), "owner", "kn-1/customer")
	if err != nil || !ownerRoleAuthority.Allowed || ownerRoleAuthority.Unrestricted {
		t.Fatalf("object owner role authority = %+v, err=%v", ownerRoleAuthority, err)
	}
	otherRoleAuthority, err := services.AuthorizeRoleGrants(t.Context(), "owner", "kn-2/order")
	if err != nil || otherRoleAuthority.Allowed {
		t.Fatalf("other-object role authority = %+v, err=%v", otherRoleAuthority, err)
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
