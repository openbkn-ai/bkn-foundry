// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/model"
)

// registerDirectory mounts bkn-safe's clean user-directory API under
// /api/safe/v1/directory. Redesigned surface — consuming services migrate to it.
func registerDirectory(r *gin.Engine, dir *directory.Service) {
	g := r.Group("/api/safe/v1/directory")

	// GET /users/:id — full user detail.
	g.GET("/users/:id", func(c *gin.Context) {
		d, err := dir.GetUser(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, d)
	})

	// POST /names — resolve ids to names by type. Clean replacement for the
	// ISF v1/v2 names endpoints (no method:"GET"-in-body, no strict flag).
	g.POST("/names", func(c *gin.Context) {
		var req struct {
			UserIDs       []string `json:"user_ids"`
			AppIDs        []string `json:"app_ids"`
			ContactorIDs  []string `json:"contactor_ids"`
			DepartmentIDs []string `json:"department_ids"`
			GroupIDs      []string `json:"group_ids"`
		}
		if !bind(c, &req) {
			return
		}
		ctx := c.Request.Context()
		users, err := dir.ResolveUserNames(ctx, req.UserIDs)
		if err != nil {
			serverError(c, err)
			return
		}
		apps, err := dir.ResolveAppNames(ctx, req.AppIDs)
		if err != nil {
			serverError(c, err)
			return
		}
		contactors, err := dir.ResolveContactorNames(ctx, req.ContactorIDs)
		if err != nil {
			serverError(c, err)
			return
		}
		depts, err := dir.ResolveDepartmentNames(ctx, req.DepartmentIDs)
		if err != nil {
			serverError(c, err)
			return
		}
		groups, err := dir.ResolveGroupNames(ctx, req.GroupIDs)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"user_names":       users,
			"app_names":        apps,
			"contactor_names":  contactors,
			"department_names": depts,
			"group_names":      groups,
		})
	})

	// GET /departments?parent_id= — list departments under a parent ("" = roots).
	g.GET("/departments", func(c *gin.Context) {
		deps, err := dir.ListDepartments(c.Request.Context(), c.Query("parent_id"))
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, deps)
	})

	// GET /groups/:id/members — group members, split into users and departments.
	g.GET("/groups/:id/members", func(c *gin.Context) {
		userIDs, deptIDs, err := dir.GroupMembersSplit(c.Request.Context(), c.Param("id"))
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"user_ids": userIDs, "department_ids": deptIDs})
	})

	// POST /search-org — which of user_ids/department_ids fall under any scope
	// department (transitive: the scope dept or any descendant).
	g.POST("/search-org", func(c *gin.Context) {
		var req struct {
			UserIDs       []string `json:"user_ids"`
			DepartmentIDs []string `json:"department_ids"`
			Scope         []string `json:"scope"`
		}
		if !bind(c, &req) {
			return
		}
		users, depts, err := dir.SearchOrgFull(c.Request.Context(), req.UserIDs, req.DepartmentIDs, req.Scope)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"user_ids": users, "department_ids": depts})
	})

	// POST /users-detail — batch full user records (name/account/enabled/roles/
	// parent_deps/groups). Unknown ids omitted. Backs DA umcmp GetUserInfo*.
	g.POST("/users-detail", func(c *gin.Context) {
		var req struct {
			UserIDs []string `json:"user_ids"`
		}
		if !bind(c, &req) {
			return
		}
		users, err := dir.UsersDetail(c.Request.Context(), req.UserIDs)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"users": users})
	})

	// GET /users/:id/department-ids — transitive department ids (direct + all
	// ancestors). Backs DA umcmp GetUserDeptIDs.
	g.GET("/users/:id/department-ids", func(c *gin.Context) {
		ids, err := dir.UserDeptIDs(c.Request.Context(), c.Param("id"))
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"department_ids": ids})
	})

	// POST /departments-detail — batch department info with root-first ancestor
	// chains. Unknown ids omitted. Backs DA umcmp GetDeptInfoMap.
	g.POST("/departments-detail", func(c *gin.Context) {
		var req struct {
			DepartmentIDs []string `json:"department_ids"`
		}
		if !bind(c, &req) {
			return
		}
		deps, err := dir.DepartmentInfos(c.Request.Context(), req.DepartmentIDs)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"departments": deps})
	})

}

// registerAdminReads mounts the admin-only directory READ endpoints (single
// user detail, department list) under the /admin group, so the CLI/web admin
// surface reaches them through the gateway. The internal (ClusterIP) equivalents
// stay on /api/safe/v1/directory for service-to-service callers.
func registerAdminReads(g *gin.RouterGroup, dir *directory.Service, e *authz.Enforcer) {
	// GET /users — list/search users (paginated), or ?account= for an exact
	// login lookup. Query: ?search=&offset=&limit= | ?account=
	// -> { users:[{id,account,name,email,enabled,account_type}], total }
	g.GET("/users", RequirePermission(e, "admin-user", "view"), func(c *gin.Context) {
		ctx := c.Request.Context()
		if acct := c.Query("account"); acct != "" {
			u, err := dir.FindUserByAccount(ctx, acct)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusOK, gin.H{"users": []directory.UserSummary{}, "total": 0})
				return
			}
			if err != nil {
				serverError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"users": []*directory.UserSummary{u}, "total": 1})
			return
		}
		users, total, err := dir.ListUsers(ctx, directory.UserListFilter{
			Search:         c.Query("search"),
			Enabled:        parseOptionalBool(c.Query("enabled")),
			DepartmentID:   c.Query("department_id"),
			IncludeSubtree: c.Query("include_subtree") == "true",
			RoleID:         c.Query("role_id"),
			Offset:         atoiDefault(c.Query("offset"), 0),
			Limit:          atoiDefault(c.Query("limit"), 0),
		})
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"users": users, "total": total})
	})

	// GET /users/:id — full user detail.
	g.GET("/users/:id", RequirePermission(e, "admin-user", "view"), func(c *gin.Context) {
		d, err := dir.GetUser(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, d)
	})

	// GET /departments — with ?parent_id= lists that parent's direct children
	// ("" = roots); without it returns the whole tree flat (paginated/searchable
	// via ?search=&offset=&limit=) so the client can build the tree.
	g.GET("/departments", RequirePermission(e, "admin-dept", "view"), func(c *gin.Context) {
		ctx := c.Request.Context()
		if _, scoped := c.GetQuery("parent_id"); scoped {
			deps, err := dir.ListDepartmentsWithCounts(ctx, c.Query("parent_id"))
			if err != nil {
				serverError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"departments": deps, "total": len(deps)})
			return
		}
		deps, total, err := dir.ListAllDepartments(ctx, c.Query("search"), atoiDefault(c.Query("offset"), 0), atoiDefault(c.Query("limit"), 0))
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"departments": deps, "total": total})
	})

	// GET /departments/:id — single department detail.
	g.GET("/departments/:id", RequirePermission(e, "admin-dept", "view"), func(c *gin.Context) {
		d, err := dir.GetDepartmentDetail(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "department not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, d)
	})

	// GET /departments/:id/members — users directly mapped into the department.
	g.GET("/departments/:id/members", RequirePermission(e, "admin-dept", "view"), func(c *gin.Context) {
		members, err := dir.DepartmentMembers(c.Request.Context(), c.Param("id"))
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"users": members, "total": len(members)})
	})
}

// atoiDefault parses s as an int, returning def on empty/invalid input.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// parseOptionalBool parses "true"/"false"; any other value returns nil.
func parseOptionalBool(s string) *bool {
	switch s {
	case "true":
		v := true
		return &v
	case "false":
		v := false
		return &v
	default:
		return nil
	}
}

// registerDeptAdmin mounts the department write surface (create/update/delete)
// under the admin group. Delete refuses a non-empty department (409).
func registerDeptAdmin(g *gin.RouterGroup, dir *directory.Service, e *authz.Enforcer) {
	// POST /departments — create a department node. Server-assigns the id when
	// the body omits it. parent_id "" makes it a root. -> { id }
	g.POST("/departments", RequirePermission(e, "admin-dept", "create"), func(c *gin.Context) {
		var req struct {
			ID        string `json:"id"`
			Name      string `json:"name" binding:"required"`
			ParentID  string `json:"parent_id"`
			Type      string `json:"type"`
			ManagerID string `json:"manager_id"`
			Code      string `json:"code"`
			Email     string `json:"email"`
			Remark    string `json:"remark"`
		}
		if !bind(c, &req) {
			return
		}
		if req.ID == "" {
			req.ID = auth.NewID()
		}
		writeIn := directory.DepartmentWriteInput{
			Name:      req.Name,
			ParentID:  req.ParentID,
			Type:      req.Type,
			ManagerID: req.ManagerID,
			Code:      req.Code,
			Email:     req.Email,
			Remark:    req.Remark,
		}
		if err := dir.ValidateDepartmentWrite(c.Request.Context(), writeIn, ""); err != nil {
			writeDepartmentValidationError(c, err)
			return
		}
		d := &model.Department{ID: req.ID}
		directory.ApplyDepartmentWrite(d, writeIn)
		if err := dir.CreateDepartment(c.Request.Context(), d); err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": d.ID})
	})

	// PUT /departments/:id — update mutable fields. Only fields present in the
	// body are changed.
	g.PUT("/departments/:id", RequirePermission(e, "admin-dept", "edit"), func(c *gin.Context) {
		var req directory.DepartmentPatchRequest
		if !bind(c, &req) {
			return
		}
		fields := directory.PatchDepartmentFields(req)
		if len(fields) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no updatable fields provided"})
			return
		}
		id := c.Param("id")
		current, err := dir.GetDepartment(c.Request.Context(), id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "department not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		if err := dir.ValidateDepartmentPatch(c.Request.Context(), id, *current, fields); err != nil {
			writeDepartmentValidationError(c, err)
			return
		}
		err = dir.UpdateDepartment(c.Request.Context(), id, fields)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "department not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /departments/:id — remove an empty department. 409 if it still has
	// child departments or member users.
	g.DELETE("/departments/:id", RequirePermission(e, "admin-dept", "delete"), func(c *gin.Context) {
		err := dir.DeleteDepartment(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "department not found"})
			return
		}
		if errors.Is(err, directory.ErrDepartmentNotEmpty) {
			c.JSON(http.StatusConflict, gin.H{"error": "department has child departments or members"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// POST /departments/:id/members — assign users to the department (the write
	// counterpart of GET .../members). Idempotent. { user_ids:[...] }
	// 404 if the department is unknown; 400 if any user id is unknown (in which
	// case nothing is written).
	g.POST("/departments/:id/members", RequirePermission(e, "admin-dept", "members"), func(c *gin.Context) {
		var req struct {
			UserIDs []string `json:"user_ids" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		err := dir.AddDepartmentMembers(c.Request.Context(), c.Param("id"), req.UserIDs)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "department not found"})
			return
		}
		if errors.Is(err, directory.ErrUnknownUser) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /departments/:id/members — remove users from the department.
	// Idempotent. { user_ids:[...] }. 404 if the department is unknown.
	g.DELETE("/departments/:id/members", RequirePermission(e, "admin-dept", "members"), func(c *gin.Context) {
		var req struct {
			UserIDs []string `json:"user_ids" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		err := dir.RemoveDepartmentMembers(c.Request.Context(), c.Param("id"), req.UserIDs)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "department not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func writeDepartmentValidationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, directory.ErrUnknownUser):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, directory.ErrDuplicateDepartmentCode):
		c.JSON(http.StatusConflict, gin.H{"error": "department code already exists"})
	default:
		if strings.Contains(err.Error(), "invalid department email") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		serverError(c, err)
	}
}
