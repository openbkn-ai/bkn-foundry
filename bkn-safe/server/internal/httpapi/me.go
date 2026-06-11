package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/authz"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/model"
)

// registerMe mounts the self-service reads under /api/safe/v1/me.
// Token-gated by RequireUser: the accessor id comes from the verified bearer
// token, never from the request — a caller can only read its own data.
// Frontends call these once after login to drive menu/button visibility; the
// backend still enforces every request via /authz/check.
func registerMe(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB, dir *directory.Service) {
	// GET "" -> the caller's identity and roles:
	// { id, account, name, email, account_type, departments:[ids],
	//   roles:[names], role_ids:[ids] }
	// Role names resolve via the roles table; a dangling binding (role row
	// deleted) falls back to the raw id rather than being dropped.
	g.GET("", func(c *gin.Context) {
		sub := c.GetString(ctxAccessorID)

		user, err := dir.GetUser(c.Request.Context(), sub)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no user for token subject: " + sub})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}

		roleIDs, err := e.RolesForAccessor(sub)
		if err != nil {
			serverError(c, err)
			return
		}
		var roleRows []model.Role
		if len(roleIDs) > 0 {
			if err := db.WithContext(c.Request.Context()).
				Where("id IN ?", roleIDs).Find(&roleRows).Error; err != nil {
				serverError(c, err)
				return
			}
		}
		nameByID := map[string]string{}
		for _, r := range roleRows {
			nameByID[r.ID] = r.Name
		}
		roleNames := make([]string, 0, len(roleIDs))
		for _, id := range roleIDs {
			if n, ok := nameByID[id]; ok {
				roleNames = append(roleNames, n)
			} else {
				roleNames = append(roleNames, id)
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"id":           user.ID,
			"account":      user.Account,
			"name":         user.Name,
			"email":        user.Email,
			"account_type": user.AccountType,
			"departments":  user.Departments,
			"roles":        roleNames,
			"role_ids":     roleIDs,
		})
	})

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
