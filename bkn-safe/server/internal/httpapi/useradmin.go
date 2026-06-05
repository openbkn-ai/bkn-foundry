package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/model"
)

// registerUserAdmin mounts the minimal user-write surface bkn-safe needs to own
// identities: create a local user and set a password. Role assignment is the
// authz role-binding endpoint. (Richer admin UI is out of scope here.)
func registerUserAdmin(r *gin.Engine, users *auth.UserStore) {
	g := r.Group("/api/safe/v1/directory")

	// POST /users — create a local (password) user. -> { id }
	g.POST("/users", func(c *gin.Context) {
		var req struct {
			ID          string `json:"id"`
			Account     string `json:"account" binding:"required"`
			Name        string `json:"name"`
			Email       string `json:"email"`
			Password    string `json:"password" binding:"required"`
			AccountType string `json:"account_type"`
		}
		if !bind(c, &req) {
			return
		}
		if req.ID == "" {
			req.ID = auth.NewID()
		}
		at := model.AccountType(req.AccountType)
		if at == "" {
			at = model.AccountTypeOther
		}
		u := &model.User{
			ID: req.ID, Account: req.Account, Name: req.Name, Email: req.Email,
			Enabled: true, AccountType: at,
		}
		if err := users.CreateLocalUser(c.Request.Context(), u, req.Password); err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": u.ID})
	})

	// PUT /users/:id/password — admin reset: sets the password and forces the
	// user to change it on next login (MustChangePassword=true).
	g.PUT("/users/:id/password", func(c *gin.Context) {
		var req struct {
			Password string `json:"password" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if err := users.ResetPassword(c.Request.Context(), c.Param("id"), req.Password); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// registerSelfServiceAuth mounts the self-service (no-admin) credential
// endpoints. change-password lets a user (e.g. the CLI, on detecting the
// initial password) change their own password by proving the old one — no
// hydra challenge, distinct from the admin reset above.
func registerSelfServiceAuth(r *gin.Engine, users *auth.UserStore) {
	// POST /api/safe/v1/auth/change-password — verify old password, set new.
	r.POST("/api/safe/v1/auth/change-password", func(c *gin.Context) {
		var req struct {
			Account     string `json:"account" binding:"required"`
			OldPassword string `json:"old_password" binding:"required"`
			NewPassword string `json:"new_password" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		// Password-strength rules are intentionally not enforced yet; only
		// reject a no-op change (new == old).
		if req.NewPassword == req.OldPassword {
			c.JSON(http.StatusBadRequest, gin.H{"error": "new password must differ from current"})
			return
		}
		err := users.ChangePassword(c.Request.Context(), req.Account, req.OldPassword, req.NewPassword)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid account or password"})
				return
			}
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}
