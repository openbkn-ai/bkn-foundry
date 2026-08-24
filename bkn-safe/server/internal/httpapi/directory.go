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

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// registerDirectory mounts bkn-safe's clean user-directory API under
// /api/safe/v1/directory. Redesigned surface — consuming services migrate to it.
func registerDirectory(r *gin.Engine, dir *directory.Service) {
	g := r.Group("/api/safe/v1/directory")

	// GET /users/:id — full user detail.
	g.GET("/users/:id", func(c *gin.Context) {
		d, err := dir.GetUser(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyPublicError(c, http.StatusNotFound)
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
	// GET /departments/:id — single department detail.
	g.GET("/departments/:id", RequirePermission(e, "admin-dept", "view"), func(c *gin.Context) {
		d, err := dir.GetDepartmentDetail(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyPublicError(c, http.StatusNotFound)
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

// registerOwnerVisibleDirectoryReads mounts the directory reads that an object
// owner needs in order to name the colleague they are sharing with. They keep
// their /admin paths so the console calls one URL whoever is looking; the group
// they hang on swaps RequireAdmin for RequireAdminOrResourceOwner, and each
// handler re-applies the administrator's permission point through
// requireAdminDirectoryPermission — an owner is exempt from that check, never
// from the one that let them in.
//
// What an owner gets back is narrower than what an administrator gets: id,
// account and name — the three columns a grantee picker shows. The admin shape
// carries email, telephone, role and department membership, and opening the
// directory for the sake of a picker is no reason to hand every resource owner
// the platform's contact list. The page size is capped for the same reason.
func registerOwnerVisibleDirectoryReads(g *gin.RouterGroup, dir *directory.Service, e *authz.Enforcer) {
	// GET /users — list/search users (paginated), or ?account= for an exact
	// login lookup. Query: ?search=&offset=&limit= | ?account=
	// -> { users:[{id,account,name,email,enabled,account_type}], total }
	g.GET("/users", func(c *gin.Context) {
		if !requireAdminDirectoryPermission(c, e, "admin-user", "view") {
			return
		}
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
			if isOwnerDirectoryRead(c) {
				c.JSON(http.StatusOK, gin.H{"users": []granteeCandidate{projectGranteeCandidate(*u)}, "total": 1})
				return
			}
			c.JSON(http.StatusOK, gin.H{"users": []*directory.UserSummary{u}, "total": 1})
			return
		}
		users, total, err := dir.ListUsers(ctx, directory.UserListFilter{
			Search:         c.Query("search"),
			Enabled:        parseOptionalBool(c.Query("enabled")),
			DepartmentID:   ownerDirectorySliceFilter(c, c.Query("department_id")),
			IncludeSubtree: c.Query("include_subtree") == "true" && !isOwnerDirectoryRead(c),
			RoleID:         ownerDirectoryRoleFilter(c, c.Query("role_id")),
			Offset:         ownerDirectoryOffset(c, atoiDefault(c.Query("offset"), 0)),
			Limit:          ownerDirectoryLimit(c, atoiDefault(c.Query("limit"), 0)),
		})
		if err != nil {
			serverError(c, err)
			return
		}
		if isOwnerDirectoryRead(c) {
			out := make([]granteeCandidate, 0, len(users))
			for _, user := range users {
				out = append(out, projectGranteeCandidate(user))
			}
			// The count of everyone on the platform is not an answer this caller
			// asked for, and paired with a stable page it is the number that tells
			// an enumerator how far to keep going. Report the page.
			c.JSON(http.StatusOK, gin.H{"users": out, "total": len(out)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"users": users, "total": total})
	})

	// GET /users/:id — full user detail.
	g.GET("/users/:id", func(c *gin.Context) {
		if !requireAdminDirectoryPermission(c, e, "admin-user", "view") {
			return
		}
		d, err := dir.GetUser(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyPublicError(c, http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		if isOwnerDirectoryRead(c) {
			c.JSON(http.StatusOK, granteeCandidate{ID: d.ID, Account: d.Account, Name: d.Name})
			return
		}
		c.JSON(http.StatusOK, d)
	})

	// GET /departments — with ?parent_id= lists that parent's direct children
	// ("" = roots); without it returns the whole tree flat (paginated/searchable
	// via ?search=&offset=&limit=) so the client can build the tree.
	g.GET("/departments", func(c *gin.Context) {
		if !requireAdminDirectoryPermission(c, e, "admin-dept", "view") {
			return
		}
		ctx := c.Request.Context()
		if _, scoped := c.GetQuery("parent_id"); scoped {
			deps, err := dir.ListDepartmentsWithCounts(ctx, c.Query("parent_id"))
			if err != nil {
				serverError(c, err)
				return
			}
			if isOwnerDirectoryRead(c) {
				out := projectDepartmentNodes(deps, ownerDirectoryLimit(c, 0))
				// Say when the cap cut the branch. A tree that is silently short
				// reads as "this is all of it", and the client cannot tell the
				// difference from the payload alone.
				c.JSON(http.StatusOK, gin.H{"departments": out, "total": len(out), "truncated": len(out) < len(deps)})
				return
			}
			c.JSON(http.StatusOK, gin.H{"departments": deps, "total": len(deps)})
			return
		}
		deps, total, err := dir.ListAllDepartments(ctx, c.Query("search"),
			ownerDirectoryOffset(c, atoiDefault(c.Query("offset"), 0)),
			ownerDirectoryLimit(c, atoiDefault(c.Query("limit"), 0)))
		if err != nil {
			serverError(c, err)
			return
		}
		if isOwnerDirectoryRead(c) {
			out := projectDepartmentNodes(deps, ownerDirectoryLimit(c, 0))
			// Compare against the unpaged count, not against `deps`: the page cap
			// was already applied by the query, so `deps` is at most one page and
			// the in-memory projection can never be the thing that cut a row. The
			// flag is a boolean and stays one — an owner is told that more exists,
			// not how much, and with offset pinned there is no next page to ask
			// for anyway.
			c.JSON(http.StatusOK, gin.H{"departments": out, "total": len(out), "truncated": int64(len(out)) < total})
			return
		}
		c.JSON(http.StatusOK, gin.H{"departments": deps, "total": total})
	})

}

// granteeCandidate is the owner-facing projection of a directory user: who they
// are and what to show in a picker, and nothing that would make this endpoint
// worth reading for any other purpose.
type granteeCandidate struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Name    string `json:"name"`
}

func projectGranteeCandidate(u directory.UserSummary) granteeCandidate {
	return granteeCandidate{ID: u.ID, Account: u.Account, Name: u.Name}
}

// departmentNode is the owner-facing projection of a department: enough to draw
// the tree a grantee picker groups by, and none of the contact detail the admin
// shape carries (manager, code, email, remark) or the head counts that describe
// the organisation rather than locate it.
type departmentNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

func projectDepartmentNodes(deps []directory.DepartmentListItem, maxRows int) []departmentNode {
	if maxRows > 0 && len(deps) > maxRows {
		deps = deps[:maxRows]
	}
	out := make([]departmentNode, 0, len(deps))
	for _, d := range deps {
		out = append(out, departmentNode{ID: d.ID, Name: d.Name, ParentID: d.ParentID})
	}
	return out
}

// ownerDirectoryOffset pins an owner to the first page. Capping the page length
// alone bounds one response and nothing else: offset=0,50,100… walks the whole
// table at the same cost, which is the enumeration the cap was meant to prevent.
// A picker opens on the first page and narrows by typing; it never pages.
func ownerDirectoryOffset(c *gin.Context, requested int) int {
	if !isOwnerDirectoryRead(c) {
		return requested
	}
	return 0
}

// ownerDirectorySliceFilter drops ?department_id= for an owner, and the caller
// drops ?include_subtree= with it. Pinning offset bounds one axis; a filter that
// partitions the same table is another axis, and walking the department tree
// (which this endpoint's sibling hands out) then asking for each department in
// turn reassembles the roster the page cap was meant to withhold. An owner gets
// one window into the directory and narrows it by typing, not by slicing.
func ownerDirectorySliceFilter(c *gin.Context, requested string) string {
	if !isOwnerDirectoryRead(c) {
		return requested
	}
	return ""
}

// ownerDirectoryRoleFilter drops ?role_id= for an owner. Listing users is one
// thing; asking which accounts hold a named privileged role is a different
// question, and answering it hands over a target list.
func ownerDirectoryRoleFilter(c *gin.Context, requested string) string {
	if !isOwnerDirectoryRead(c) {
		return requested
	}
	return ""
}

func isOwnerDirectoryRead(c *gin.Context) bool {
	return c.GetString(ctxDirectoryReadAuthority) == directoryReadOwner
}

// ownerDirectoryMaxPageSize caps what an owner may pull per request. The console
// asks for 1000 when an administrator drives the same screen; an owner is here
// to find one person by name.
const ownerDirectoryMaxPageSize = 50

func ownerDirectoryLimit(c *gin.Context, requested int) int {
	if !isOwnerDirectoryRead(c) {
		return requested
	}
	if requested <= 0 || requested > ownerDirectoryMaxPageSize {
		return ownerDirectoryMaxPageSize
	}
	return requested
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
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		id := c.Param("id")
		current, err := dir.GetDepartment(c.Request.Context(), id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyPublicError(c, http.StatusNotFound)
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
			replyPublicError(c, http.StatusNotFound)
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
			replyPublicError(c, http.StatusNotFound)
			return
		}
		if errors.Is(err, directory.ErrDepartmentNotEmpty) {
			replyPublicError(c, http.StatusConflict)
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
			replyPublicError(c, http.StatusNotFound)
			return
		}
		if errors.Is(err, directory.ErrUnknownUser) {
			replyPublicError(c, http.StatusBadRequest)
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
			replyPublicError(c, http.StatusNotFound)
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
		replyPublicError(c, http.StatusBadRequest)
	case errors.Is(err, directory.ErrDuplicateDepartmentCode):
		replyPublicError(c, http.StatusConflict)
	default:
		if strings.Contains(err.Error(), "invalid department email") {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		serverError(c, err)
	}
}
