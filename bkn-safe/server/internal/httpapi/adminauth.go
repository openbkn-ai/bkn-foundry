// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"bkn-safe/internal/authz"
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		sub, err := v.VerifyToken(c.Request.Context(), tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or inactive token"})
			return
		}
		ok, err := e.CanAdmin(sub)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not authorized for admin operations"})
			return
		}
		c.Set(ctxAccessorID, sub)
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
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authenticated accessor"})
		return false
	}
	ok, err := e.Check(sub, resourceType, "*", op)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not authorized for " + resourceType + ":" + op})
		return false
	}
	return true
}

// RequireUser is the gin middleware guarding self-service APIs (/me). It only
// authenticates: verify the bearer token and stash the subject as the caller's
// accessor id. No authz check — any logged-in accessor may read its own data.
func RequireUser(v TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := bearerToken(c)
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		sub, err := v.VerifyToken(c.Request.Context(), tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or inactive token"})
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
