package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/model"
)

// registerUserAdmin mounts the user-write surface bkn-safe needs to own
// identities: create/update/delete a local user and set a password. Role
// assignment is the authz role-binding endpoint; department membership can be
// set inline (department_ids) or via the department member endpoints. The
// enforcer is used to purge an accessor's casbin bindings/grants on delete; the
// directory service applies department_ids. Mounted under the admin group
// (RequireAdmin) — these are privileged, token-gated operations.
func registerUserAdmin(g *gin.RouterGroup, users *auth.UserStore, e *authz.Enforcer, dir *directory.Service) {
	// POST /users — create a local (password) user, optionally placing it in
	// departments (department_ids). -> { id }
	g.POST("/users", func(c *gin.Context) {
		var req struct {
			ID            string   `json:"id"`
			Account       string   `json:"account" binding:"required"`
			Name          string   `json:"name"`
			Email         string   `json:"email"`
			Telephone     string   `json:"telephone"`
			Password      string   `json:"password"` // optional: defaults to the platform initial password
			AccountType   string   `json:"account_type"`
			DepartmentIDs []string `json:"department_ids"` // optional: initial department membership
		}
		if !bind(c, &req) {
			return
		}
		ctx := c.Request.Context()
		// Validate departments BEFORE creating the user, so an unknown id fails
		// the request without leaving an orphaned user behind.
		if err := dir.DepartmentsExist(ctx, req.DepartmentIDs); errors.Is(err, directory.ErrUnknownDepartment) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		} else if err != nil {
			serverError(c, err)
			return
		}
		if req.ID == "" {
			req.ID = auth.NewID()
		}
		// No password given → hand out the platform initial password. The user is
		// forced to change it on first login (CreateLocalUser sets the flag).
		if req.Password == "" {
			req.Password = auth.DefaultInitialPassword
		}
		at := model.AccountType(req.AccountType)
		if at == "" {
			at = model.AccountTypeOther
		}
		u := &model.User{
			ID: req.ID, Account: req.Account, Name: req.Name, Email: req.Email,
			Telephone: req.Telephone, Enabled: true, AccountType: at,
		}
		if err := users.CreateLocalUser(ctx, u, req.Password); err != nil {
			serverError(c, err)
			return
		}
		if len(req.DepartmentIDs) > 0 {
			if err := dir.SetUserDepartments(ctx, u.ID, req.DepartmentIDs); err != nil {
				serverError(c, err)
				return
			}
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

	// PUT /users/:id — update mutable profile fields and/or department membership.
	// Only the keys present in the body are changed (account and password are not
	// editable here). A bool "enabled" is always applied when present, so this
	// doubles as the enable/disable path. "department_ids", when present, REPLACES
	// the user's full department set (an empty array clears it); omit the key to
	// leave memberships untouched.
	g.PUT("/users/:id", func(c *gin.Context) {
		var req struct {
			Name          *string   `json:"name"`
			Email         *string   `json:"email"`
			Telephone     *string   `json:"telephone"`
			Enabled       *bool     `json:"enabled"`
			AccountType   *string   `json:"account_type"`
			DepartmentIDs *[]string `json:"department_ids"`
		}
		if !bind(c, &req) {
			return
		}
		ctx := c.Request.Context()
		id := c.Param("id")
		fields := map[string]any{}
		if req.Name != nil {
			fields["name"] = *req.Name
		}
		if req.Email != nil {
			fields["email"] = *req.Email
		}
		if req.Telephone != nil {
			fields["telephone"] = *req.Telephone
		}
		if req.Enabled != nil {
			fields["enabled"] = *req.Enabled
		}
		if req.AccountType != nil {
			fields["account_type"] = *req.AccountType
		}
		if len(fields) == 0 && req.DepartmentIDs == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no updatable fields provided"})
			return
		}
		// Validate departments up-front so a bad id fails before any write lands.
		if req.DepartmentIDs != nil {
			if err := dir.DepartmentsExist(ctx, *req.DepartmentIDs); errors.Is(err, directory.ErrUnknownDepartment) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			} else if err != nil {
				serverError(c, err)
				return
			}
		}
		if len(fields) > 0 {
			err := users.UpdateUser(ctx, id, fields)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
				return
			}
			if err != nil {
				serverError(c, err)
				return
			}
		}
		if req.DepartmentIDs != nil {
			err := dir.SetUserDepartments(ctx, id, *req.DepartmentIDs)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
				return
			}
			if err != nil {
				serverError(c, err)
				return
			}
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /users/:id — remove the user, its directory memberships, and all of
	// its casbin role bindings / direct grants.
	g.DELETE("/users/:id", func(c *gin.Context) {
		id := c.Param("id")
		err := users.DeleteUser(c.Request.Context(), id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		if e != nil {
			if err := e.RemoveAccessor(id); err != nil {
				serverError(c, err)
				return
			}
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
