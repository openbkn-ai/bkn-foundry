// Package httpapi wires bkn-safe's HTTP surface: health, the authz API, the
// user-directory API, and the hydra login/consent/device provider pages.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/directory"
)

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Enforcer  *authz.Enforcer
	DB        *gorm.DB
	Provider  *auth.Provider
	Hydra     *auth.HydraAdmin
	Directory *directory.Service
	Users     *auth.UserStore
	// TokenVerifier validates admin-API bearer tokens. Defaults to Hydra when
	// nil (production); tests inject a stub.
	TokenVerifier TokenVerifier
}

// New builds the gin engine with all routes mounted.
func New(deps Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health/ready", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/health/alive", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Internal authz API (service-to-service, ClusterIP, unauthenticated):
	// check/operations/policies/resources. Callers (DA/vega) resolve identity at
	// their own boundary and pass accessor_id.
	registerAuthz(r, deps.Enforcer, deps.DB)

	// hydra login/consent/device provider pages.
	if deps.Provider != nil && deps.Hydra != nil {
		registerAuth(r, deps.Provider, deps.Hydra)
	}

	// Internal user-directory reads (name resolution, batch lookups) — ClusterIP.
	if deps.Directory != nil {
		registerDirectory(r, deps.Directory)
	}
	// Self-service change-password (browser/CLI-facing, own credential proof).
	if deps.Users != nil {
		registerSelfServiceAuth(r, deps.Users)
	}

	// Admin API under /api/safe/v1/admin — token-gated (RequireAdmin: verify
	// bearer token + casbin super-admin check) and the ONLY mutating surface
	// exposed via the gateway. user/dept/role CRUD, role-bindings, admin reads.
	verifier := deps.TokenVerifier
	if verifier == nil && deps.Hydra != nil {
		verifier = deps.Hydra
	}
	if deps.Enforcer != nil && verifier != nil && deps.Users != nil && deps.Directory != nil {
		admin := r.Group("/api/safe/v1/admin", RequireAdmin(verifier, deps.Enforcer))
		registerUserAdmin(admin, deps.Users, deps.Enforcer)
		registerAdminReads(admin, deps.Directory)
		registerDeptAdmin(admin, deps.Directory)
		registerRoleBindings(admin, deps.Enforcer, deps.DB)
		registerRoles(admin, deps.Enforcer, deps.DB)
	}

	// Self-service reads under /api/safe/v1/me — token-gated (RequireUser:
	// authn only), gateway-exposed. The caller reads its own permission list.
	if deps.Enforcer != nil && verifier != nil && deps.Directory != nil {
		me := r.Group("/api/safe/v1/me", RequireUser(verifier))
		registerMe(me, deps.Enforcer, deps.DB, deps.Directory)
	}

	return r
}
