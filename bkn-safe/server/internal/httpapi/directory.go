package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/directory"
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
