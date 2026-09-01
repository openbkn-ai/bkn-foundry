// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

// TokenVerifier resolves a bearer access token to its subject (the accessor id),
// or errors if the token is invalid/inactive. *auth.HydraAdmin implements it via
// hydra introspection; tests supply a stub.
type TokenVerifier interface {
	VerifyToken(ctx context.Context, token string) (subject string, err error)
}

// ctxAccessorID is the gin context key under which RequireAdmin stores the
// authenticated caller's accessor id for downstream handlers.
const ctxAccessorID = "accessor_id"

// RequireAdmin is the gin middleware guarding the admin API. It verifies the
// bearer token (authn) and confirms the caller may administer (authz, via the
// casbin super-admin/safe_admin capability). Internal service-to-service APIs
// (/authz, /directory) are NOT guarded by this — they stay ClusterIP-internal.
func RequireAdmin(v TokenVerifier, e *authz.Enforcer) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := bearerToken(c)
		if tok == "" {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		sub, err := v.VerifyToken(c.Request.Context(), tok)
		if err != nil {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		ok, err := e.CanAdmin(sub)
		if err != nil {
			abortInternalError(c)
			return
		}
		if !ok {
			abortPublicError(c, http.StatusForbidden)
			return
		}
		c.Set(ctxAccessorID, sub)
		c.Next()
	}
}

// RequireActiveAccount closes the gap between token validity and local account
// state. A token can remain introspectable briefly after an administrator is
// disabled; management and owner-delegation routes must stop at the local state
// immediately instead of continuing to honor cached Casbin grants.
func RequireActiveAccount(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sub := c.GetString(ctxAccessorID)
		if sub == "" {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		active, err := activeAccount(c, db, sub)
		if err != nil {
			abortPublicError(c, http.StatusServiceUnavailable)
			return
		}
		if !active {
			abortPublicError(c, http.StatusForbidden)
			return
		}
		c.Next()
	}
}

// RequirePermission guards one concrete admin operation after RequireAdmin has
// authenticated the caller and stored ctxAccessorID. safe_admin:console:manage
// only opens the admin surface; this middleware enforces the endpoint's real
// resource/action permission.
func RequirePermission(e *authz.Enforcer, resourceType, op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authorizePermission(c, e, resourceType, op) {
			return
		}
		c.Next()
	}
}

func authorizePermission(c *gin.Context, e *authz.Enforcer, resourceType, op string) bool {
	sub := c.GetString(ctxAccessorID)
	if sub == "" {
		abortPublicError(c, http.StatusUnauthorized)
		return false
	}
	ok, err := e.Check(sub, resourceType, "*", op)
	if err != nil {
		abortInternalError(c)
		return false
	}
	if !ok {
		abortPublicError(c, http.StatusForbidden)
		return false
	}
	return true
}

// PermissionPoint is one (resource type, operation) admin permission point, e.g.
// {"admin-role", "permissions"}.
type PermissionPoint struct {
	ResourceType string
	Op           string
}

func (p PermissionPoint) String() string { return p.ResourceType + ":" + p.Op }

// RequireAnyPermission guards an admin operation that accepts MORE THAN ONE
// permission point, passing when the caller holds any of them. Its only intended
// use is a renamed point: list the canonical point FIRST and the superseded one
// after it, so a deployment whose custom roles were granted the old point keeps
// working across the upgrade.
//
// Seeded roles need no such grace — reconcileSeedRoles rewrites their grants
// from grants.json on every boot — but CUSTOM roles are never rewritten, so a
// hard switch would silently lock out any custom role built on the old point.
// Passing via a superseded point is logged (accessor + point + route) so the
// grace period can be ended on evidence rather than on a guess: once the logs
// are quiet, drop the legacy point and go back to RequirePermission.
func RequireAnyPermission(e *authz.Enforcer, points ...PermissionPoint) gin.HandlerFunc {
	return func(c *gin.Context) {
		sub := c.GetString(ctxAccessorID)
		if sub == "" {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		for i, p := range points {
			ok, err := e.Check(sub, p.ResourceType, "*", p.Op)
			if err != nil {
				abortInternalError(c)
				return
			}
			if !ok {
				continue
			}
			if i > 0 {
				slog.Warn("admin request authorized via a superseded permission point",
					"accessor_id", sub, "point", p.String(), "canonical_point", points[0].String(),
					"method", c.Request.Method, "path", c.FullPath())
			}
			c.Next()
			return
		}
		abortPublicError(c, http.StatusForbidden)
	}
}

// RequireUser is the gin middleware guarding self-service APIs (/me). It only
// authenticates: verify the bearer token and stash the subject as the caller's
// accessor id. No authz check — any logged-in accessor may read its own data.
func RequireUser(v TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := bearerToken(c)
		if tok == "" {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		sub, err := v.VerifyToken(c.Request.Context(), tok)
		if err != nil {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		c.Set(ctxAccessorID, sub)
		c.Next()
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header,
// or "" when absent/malformed.
func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// ctxDirectoryReadAuthority records how a caller earned a directory read on the
// relaxed group below: "admin" for the platform administrator, "owner" for the
// holder of a concrete object grant. Handlers branch on it because the two are
// not entitled to the same rows or the same columns.
const ctxDirectoryReadAuthority = "directory_read_authority"

const (
	directoryReadAdmin = "admin"
	directoryReadOwner = "owner"
)

// RequireAdminOrResourceOwner guards the directory reads an object owner needs
// in order to name the person they are sharing with. Administrators pass as they
// always did; anyone else passes only while holding `authorize` on at least one
// CONCRETE object.
//
// "Concrete" is the whole point, and it is why this reads ListObjectGrants
// rather than asking the enforcer. A Check/EffectivePermissions answer folds in
// type-wide role grants, and the seeded network_builder role carries `authorize`
// on catalog, connector_type, operator, skill and stream_data_pipeline — so
// every member of that role would pass without owning anything at all, turning
// "the person who built this may look up a colleague" into "this role may read
// the directory". ListObjectGrants skips type-wide rows (see its rid == "*"
// filter), leaving exactly the grants a domain service wrote to a creator.
//
// Fail-closed throughout: a lookup error is a refusal, never a pass.
func RequireAdminOrResourceOwner(v TokenVerifier, e *authz.Enforcer) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := bearerToken(c)
		if tok == "" {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		sub, err := v.VerifyToken(c.Request.Context(), tok)
		if err != nil {
			abortPublicError(c, http.StatusUnauthorized)
			return
		}
		admin, err := e.CanAdmin(sub)
		if err != nil {
			abortInternalError(c)
			return
		}
		if admin {
			c.Set(ctxAccessorID, sub)
			c.Set(ctxDirectoryReadAuthority, directoryReadAdmin)
			c.Next()
			return
		}
		grants, err := e.ListObjectGrants(sub, "", "")
		if err != nil {
			abortInternalError(c)
			return
		}
		for _, grant := range grants {
			for _, op := range grant.Operations {
				if op == opAuthorize {
					c.Set(ctxAccessorID, sub)
					c.Set(ctxDirectoryReadAuthority, directoryReadOwner)
					c.Next()
					return
				}
			}
		}
		abortPublicError(c, http.StatusForbidden)
	}
}

// requireAdminDirectoryPermission applies the administrator's own permission
// point when the caller is one. An owner reached the handler through the object
// grant instead, and holds no admin-user/admin-dept point by construction, so
// asking would refuse every one of them.
func requireAdminDirectoryPermission(c *gin.Context, e *authz.Enforcer, resourceType, op string) bool {
	if c.GetString(ctxDirectoryReadAuthority) == directoryReadOwner {
		return true
	}
	return authorizePermission(c, e, resourceType, op)
}
