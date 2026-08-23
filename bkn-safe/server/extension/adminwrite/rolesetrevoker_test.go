// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
)

// legacyServices implements exactly the Services method set and nothing more —
// the shape an existing implementation outside this repository has, including
// ee's test doubles. It must keep satisfying Services after RoleSetRevoker is
// introduced, which is the whole reason that is a separate interface.
type legacyServices struct {
	revoked []string
}

func (s *legacyServices) RequirePermission(string, string) gin.HandlerFunc { return nil }
func (s *legacyServices) CreateRole(context.Context, RoleSpec) (string, error) {
	return "", nil
}
func (s *legacyServices) UpdateRole(context.Context, string, RolePatch) error { return nil }
func (s *legacyServices) DeleteRole(context.Context, string) error            { return nil }
func (s *legacyServices) GrantRolePermission(context.Context, string, string, string, string) error {
	return nil
}
func (s *legacyServices) RevokeRolePermission(_ context.Context, _, _, _, op string) error {
	s.revoked = append(s.revoked, op)
	return nil
}

// setServices adds the optional set form on top.
type setServices struct {
	legacyServices
	sets [][]string
}

func (s *setServices) RevokeRolePermissions(_ context.Context, _, _, _ string, ops []string) error {
	s.sets = append(s.sets, ops)
	return nil
}

func TestRevokeRolePermissionsFallsBackForALegacyImplementation(t *testing.T) {
	var svc Services = &legacyServices{}
	if _, ok := svc.(RoleSetRevoker); ok {
		t.Fatal("the legacy shape must not satisfy RoleSetRevoker")
	}
	if err := revokeRolePermissions(t.Context(), svc, "r-1", "catalog", "c1",
		[]string{"view_detail", "resource_manage"}); err != nil {
		t.Fatal(err)
	}
	got := svc.(*legacyServices).revoked
	if len(got) != 2 || got[0] != "view_detail" || got[1] != "resource_manage" {
		t.Fatalf("fallback did not revoke one at a time in order: %v", got)
	}
}

func TestRevokeRolePermissionsPrefersTheSetForm(t *testing.T) {
	svc := &setServices{}
	if err := revokeRolePermissions(t.Context(), svc, "r-1", "catalog", "c1",
		[]string{"view_detail", "resource_manage"}); err != nil {
		t.Fatal(err)
	}
	if len(svc.sets) != 1 || len(svc.sets[0]) != 2 {
		t.Fatalf("set form not used: %v", svc.sets)
	}
	if len(svc.revoked) != 0 {
		t.Fatalf("per-operation path also ran: %v", svc.revoked)
	}
}
