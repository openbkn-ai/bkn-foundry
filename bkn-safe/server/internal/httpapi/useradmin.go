package httpapi

import (
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

	// PUT /users/:id/password — reset a local user's password.
	g.PUT("/users/:id/password", func(c *gin.Context) {
		var req struct {
			Password string `json:"password" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if err := users.SetPassword(c.Request.Context(), c.Param("id"), req.Password); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}
