// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newCommunityServer builds a server the way a community binary would: no
// adminwrite mounter registered, so the rbac_basic write routes are never
// mounted. This is the whole point of the two-binary split — the endpoints do
// not exist, not merely refuse.
func newCommunityServer(t *testing.T) (*gin.Engine, *authz.Enforcer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	e, err := authz.New(db)
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	if err := e.Grant(adminSub, "*", "*"); err != nil {
		t.Fatalf("grant super-admin: %v", err)
	}
	// Explicitly clear the socket and register NOTHING — the community build.
	adminwrite.ResetForTest()
	r := New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
		Audit:         audit.New(db),
		TokenVerifier: stubVerifier{},
	})
	return r, e
}

// TestCommunityBuildOmitsRbacBasicWriteRoutes is the two-binary contract: with
// no ee mounter, every rbac_basic write endpoint 404s (does not exist), while
// the sibling read endpoints stay fully available — community may look, not
// customise. A super-admin token is used so a 404 cannot be mistaken for an
// authorization refusal (which would be 403).
func TestCommunityBuildOmitsRbacBasicWriteRoutes(t *testing.T) {
	r, _ := newCommunityServer(t)

	writes := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/safe/v1/admin/roles"},
		{http.MethodPut, "/api/safe/v1/admin/roles/some-id"},
		{http.MethodDelete, "/api/safe/v1/admin/roles/some-id"},
		{http.MethodPost, "/api/safe/v1/admin/roles/some-id/permissions"},
		{http.MethodDelete, "/api/safe/v1/admin/roles/some-id/permissions"},
	}
	for _, w := range writes {
		t.Run(w.method+" "+w.path, func(t *testing.T) {
			rec := adminReq(t, r, w.method, w.path, gin.H{"name": "x"})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("community build must 404 (route absent), got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// Reads stay available — community can view roles, just not change them.
	if rec := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil); rec.Code != http.StatusOK {
		t.Fatalf("community role read must stay available, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCommunityThenEnterpriseSocket documents that the same server code serves
// the routes once a mounter is registered — the difference between the two
// binaries is exactly whether Mount was given a mounter.
func TestCommunityThenEnterpriseSocket(t *testing.T) {
	// community: absent
	rc, _ := newCommunityServer(t)
	if rec := adminReq(t, rc, http.MethodPost, "/api/safe/v1/admin/roles", gin.H{"id": "c1", "name": "c1"}); rec.Code != http.StatusNotFound {
		t.Fatalf("community: want 404, got %d", rec.Code)
	}

	// enterprise: mounter present -> route serves, role created
	re, _, _, _ := newAdminServer(t) // registers adminwrite.Routes
	if rec := adminReq(t, re, http.MethodPost, "/api/safe/v1/admin/roles", gin.H{"id": "c1", "name": "c1"}); rec.Code != http.StatusCreated {
		t.Fatalf("enterprise: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
}
