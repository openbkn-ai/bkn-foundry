package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"bkn-safe/internal/authz"
)

// registerMe mounts the self-service permission read under /api/safe/v1/me.
// Token-gated by RequireUser: the accessor id comes from the verified bearer
// token, never from the request — a caller can only read its own grants.
// Frontends call this once after login to drive menu/button visibility; the
// backend still enforces every request via /authz/check.
func registerMe(g *gin.RouterGroup, e *authz.Enforcer) {
	// GET /permissions -> { is_admin, permissions:[ { resource{type,id}, operations:[...] } ] }
	// Includes role-inherited grants; type-wide patterns keep id "*".
	g.GET("/permissions", func(c *gin.Context) {
		accessorID := c.GetString(ctxAccessorID)
		isAdmin, err := e.CanAdmin(accessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		grants, err := e.AccessorPermissions(accessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"is_admin":    isAdmin,
			"permissions": grantsJSON(grants),
		})
	})
}
