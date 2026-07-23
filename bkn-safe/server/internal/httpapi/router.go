// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package httpapi wires bkn-safe's HTTP surface: health, the authz API, the
// user-directory API, and the hydra login/consent/device provider pages.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/audit"
	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/license"
)

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Enforcer  *authz.Enforcer
	DB        *gorm.DB
	Provider  *auth.Provider
	Hydra     *auth.HydraAdmin
	Directory *directory.Service
	Users     *auth.UserStore
	// Audit records admin-API mutations. When nil, the audit middleware and the
	// audit-log read endpoint are not mounted (auditing off).
	Audit *audit.Store
	// TokenVerifier validates admin-API bearer tokens. Defaults to Hydra when
	// nil (production); tests inject a stub.
	TokenVerifier TokenVerifier
	// ClientAdmin manages login clients' redirect_uris (admin API). Defaults to
	// Hydra when nil (production); tests inject a stub.
	ClientAdmin ClientManager
	// License is the cluster license hub. When nil, the license admin and
	// internal distribution endpoints are not mounted.
	License *license.Service
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

	// AppKey (user-issued API key) store. Verification is internal, tokenless and
	// ClusterIP-only (same trust face as /authz) — the Context Loader MCP/REST
	// gateway calls /api/safe/v1/api-keys/introspect to resolve a key to its
	// owner. Self-service issue/list/revoke is mounted on /me, admin oversight on
	// /admin (both token-gated, below).
	var apiKeys *auth.APIKeyStore
	if deps.DB != nil {
		apiKeys = auth.NewAPIKeyStore(deps.DB)
		registerAPIKeyVerify(r, apiKeys)
	}

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
	// The introspection cache is scoped to /me ONLY. The frontend fires /me and
	// /me/permissions in parallel at login, so a short-TTL, singleflight-
	// deduplicated cache collapses that pair into one upstream introspection and
	// absorbs repeat pulls. It is NOT applied to /admin: that surface keeps the
	// raw verifier so a revoked/logged-out token stops working on mutating admin
	// operations immediately, rather than up to a TTL later — the revocation-lag
	// trade-off stays confined to read-only self-service. Authorization (casbin)
	// is realtime on both regardless; only the token->subject step is cached.
	meVerifier := verifier
	if meVerifier != nil {
		meVerifier = newCachingVerifier(meVerifier, verifierCacheTTL)
	}
	if deps.Enforcer != nil && verifier != nil && deps.Users != nil && deps.Directory != nil {
		admin := r.Group("/api/safe/v1/admin", RequireAdmin(verifier, deps.Enforcer))
		// Audit every mutating admin request. Use() must precede the route
		// registrations below: gin snapshots the group's handler chain at
		// register time. The middleware sits after RequireAdmin, so it only runs
		// for authenticated callers (failed-auth 401/403 are not audited).
		if deps.Audit != nil {
			admin.Use(auditMiddleware(deps.Audit, deps.Directory, deps.DB))
			registerAuditReads(admin, deps.Audit, deps.Enforcer)
		}
		registerUserAdmin(admin, deps.Users, deps.Enforcer, deps.Directory)
		registerAdminReads(admin, deps.Directory, deps.Enforcer)
		registerDeptAdmin(admin, deps.Directory, deps.Enforcer)
		registerRoleBindings(admin, deps.Enforcer, deps.DB)
		registerRoles(admin, deps.Enforcer, deps.DB)
		registerObjectGrants(admin, deps.Enforcer, deps.DB)
		// Global AppKey oversight: list/revoke any user's keys.
		if apiKeys != nil {
			registerAdminAPIKeys(admin, apiKeys, deps.Enforcer)
		}
		// Login-client redirect-uri management. Falls back to Hydra in production;
		// only mounted when a manager is available.
		clientMgr := deps.ClientAdmin
		if clientMgr == nil && deps.Hydra != nil {
			clientMgr = deps.Hydra
		}
		if clientMgr != nil {
			registerClientAdmin(admin, clientMgr, deps.Enforcer)
		}
		// Cluster license hub management (import/activate/remove + detail).
		if deps.License != nil {
			registerLicenseAdmin(admin, deps.License, deps.Enforcer)
		}
	}

	// In-cluster license distribution: modules pull the signed text and verify
	// locally. AppKey-authenticated (not anonymous, unlike /authz — upstream
	// hard rule for this surface).
	if deps.License != nil && apiKeys != nil {
		registerLicenseInternal(r, deps.License, apiKeys)
	}

	// Self-service reads under /api/safe/v1/me — token-gated (RequireUser:
	// authn only), gateway-exposed. The caller reads its own permission list.
	if deps.Enforcer != nil && verifier != nil && deps.Directory != nil {
		// Read-only /me (GET "" + GET /permissions): the login burst fires these
		// two in parallel, so they get the cached, singleflight-deduplicated
		// verifier.
		meReads := r.Group("/api/safe/v1/me", RequireUser(meVerifier))
		registerMeReads(meReads, deps.Enforcer, deps.DB, deps.Directory)

		// Mutating /me (profile PUT, AppKey issue/revoke) uses the RAW verifier so
		// a revoked/logged-out token cannot edit the profile or mint a long-lived
		// API key within the read cache's TTL window.
		meWrites := r.Group("/api/safe/v1/me", RequireUser(verifier))
		registerMeProfile(meWrites, deps.Users)
		// Self-service AppKey management (issue/list/revoke own keys).
		if apiKeys != nil {
			registerMeAPIKeys(meWrites, apiKeys)
		}
	}

	return r
}
