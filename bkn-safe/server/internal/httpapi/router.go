// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package httpapi wires bkn-safe's HTTP surface: health, the authz API, the
// user-directory API, and the hydra login/consent/device provider pages.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/license"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
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
	// AccessLog records login/logout outcomes separately from management audit.
	AccessLog *accesslog.Store
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
	httperrors.Register()

	r := gin.New()
	r.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		if strings.HasPrefix(c.Request.URL.Path, "/health/") {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		abortInternalError(c)
	}))
	r.Use(sharedrest.LanguageMiddleware())

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
		registerManagedProxyAccounts(r, managedproxy.New(deps.DB))
	}

	// hydra login/consent/device provider pages.
	if deps.Provider != nil && deps.Hydra != nil {
		registerAuth(r, deps.Provider, deps.Hydra, deps.AccessLog)
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
		admin := r.Group("/api/safe/v1/admin", sharedrest.PrivateNoCacheMiddleware(), RequireAdmin(verifier, deps.Enforcer), RequireActiveAccount(deps.DB))
		// Audit every mutating admin request. Use() must precede the route
		// registrations below: gin snapshots the group's handler chain at
		// register time. The middleware sits after RequireAdmin, so it only runs
		// for authenticated callers (failed-auth 401/403 are not audited).
		if deps.Audit != nil {
			admin.Use(auditMiddleware(deps.Audit, deps.Directory, deps.DB))
			registerAuditReads(admin, deps.Audit, deps.Enforcer)
		}
		if deps.AccessLog != nil {
			registerAccessLogReads(admin, deps.AccessLog, deps.Enforcer)
		}
		registerUserAdmin(admin, deps.Users, deps.Enforcer, deps.Directory)
		registerAdminReads(admin, deps.Directory, deps.Enforcer)
		// Directory reads an object owner needs to name a grantee. Same prefix,
		// weaker door: RequireAdminOrResourceOwner in place of RequireAdmin. Kept
		// off the `admin` group because gin fixes a group's handler chain at
		// register time — the relaxation has to be its own group or it would be
		// no relaxation at all.
		ownerDirectory := r.Group("/api/safe/v1/admin", sharedrest.PrivateNoCacheMiddleware(),
			RequireAdminOrResourceOwner(verifier, deps.Enforcer), RequireActiveAccount(deps.DB))
		if deps.Audit != nil {
			ownerDirectory.Use(auditMiddleware(deps.Audit, deps.Directory, deps.DB))
		}
		registerOwnerVisibleDirectoryReads(ownerDirectory, deps.Directory, deps.Enforcer)
		registerDeptAdmin(admin, deps.Directory, deps.Enforcer)
		registerRoleBindings(admin, deps.Enforcer, deps.DB)
		registerRoles(admin, deps.Enforcer, deps.DB)
		registerObjectGrants(admin, deps.Enforcer, deps.DB)
		// rbac_basic write routes (custom role create/update/delete + role
		// permission grant/revoke) are mounted by the enterprise build through
		// the adminwrite socket. In a community binary no mounter was registered,
		// so Mount is a no-op and those endpoints stay absent (404). The guarded
		// operations live in newAdminWriteServices; ee owns only the HTTP shape.
		// The licence gate goes in FRONT of RequireAdmin, on its own group at the
		// same prefix. Adding it to `admin` would put it behind authentication,
		// and then an unauthenticated probe would get 401 from an enterprise
		// binary where a community one answers 404 — the paid surface would be
		// identifiable without any credential at all. Audit sits after the gate
		// for the same reason: a hidden route must not produce a record shaped
		// differently from a route that does not exist.
		gatedAdmin := r.Group("/api/safe/v1/admin", sharedrest.PrivateNoCacheMiddleware(), adminwrite.Gate(), RequireAdmin(verifier, deps.Enforcer), RequireActiveAccount(deps.DB))
		if deps.Audit != nil {
			gatedAdmin.Use(auditMiddleware(deps.Audit, deps.Directory, deps.DB))
		}
		if adminwrite.Mount(gatedAdmin, newAdminWriteServices(deps.Enforcer, deps.DB)) {
			slog.Info("rbac_basic admin write routes mounted (enterprise build)")
		}
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

	// In-cluster license distribution. The WHOLE /internal/license group is a
	// tokenless service surface — /current, /status, /capabilities alike — the
	// same trust face as /authz and /api-keys/introspect above. Anything added to
	// this group inherits that: if a route would not be safe to answer for any
	// pod in the cluster, it does not belong here.
	//
	// Tokenless is a property of the surface, not a shortcut. Modules pull the
	// signed text and verify it locally against a key compiled into their own
	// binary, so the group hands out evidence, not verdicts — withholding it can
	// deny a licence, serving it cannot manufacture one, and a leaked certificate
	// confers no power to forge one. The boundary is ClusterIP plus the chart's
	// networkPolicy, not a bearer token. The alternative was a per-service
	// credential with no defined issuer, rotation story, or revocation signal —
	// an unanswered question that blocked every service needing to read a licence.
	if deps.License != nil {
		registerLicenseInternal(r, deps.License)
	}

	// Self-service reads under /api/safe/v1/me — token-gated (RequireUser:
	// authn only), gateway-exposed. The caller reads its own permission list.
	if deps.Enforcer != nil && verifier != nil && deps.Directory != nil {
		// Read-only /me (GET "" + GET /permissions): the login burst fires these
		// two in parallel, so they get the cached, singleflight-deduplicated
		// verifier.
		meReads := r.Group("/api/safe/v1/me", sharedrest.PrivateNoCacheMiddleware(), RequireUser(meVerifier))
		registerMeReads(meReads, deps.Enforcer, deps.DB, deps.Directory)

		// What this deployment can do, for the frontend's menu. Authn only:
		// it describes the cluster, not the caller. Enforcement stays at each
		// gated call site (open-core-gating §2.5).
		caps := r.Group("/api/safe/v1", sharedrest.PrivateNoCacheMiddleware(), RequireUser(meVerifier))
		registerCapabilities(caps, deps.License)

		// Mutating /me (profile PUT, AppKey issue/revoke) uses the RAW verifier so
		// a revoked/logged-out token cannot edit the profile or mint a long-lived
		// API key within the read cache's TTL window.
		meWrites := r.Group("/api/safe/v1/me", sharedrest.PrivateNoCacheMiddleware(), RequireUser(verifier))
		if deps.Audit != nil {
			meWrites.Use(auditMiddleware(deps.Audit, deps.Directory, deps.DB))
		}
		registerMeProfile(meWrites, deps.Users)
		// Object-grant delegation: sharing an object you own is a write, so it
		// belongs on the raw-verifier group with the rest of the mutating /me
		// surface — and it must be audited like any other authorization change.
		registerMeObjectGrants(meWrites, deps.Enforcer, deps.DB, deps.Directory)
		if deps.AccessLog != nil && deps.Directory != nil {
			registerLogout(meWrites, deps.AccessLog, deps.Directory)
		}
		// Self-service AppKey management (issue/list/revoke own keys).
		if apiKeys != nil {
			registerMeAPIKeys(meWrites, apiKeys)
		}
	}

	return r
}
