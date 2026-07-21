// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/authz"
	"bkn-safe/internal/model"
)

// registerObjectGrants mounts the object-level authorization management API
// under /admin (admin-only). It manages the "grant a specific object to a
// specific user" matrix that sits ON TOP of role-based RBAC: each grant binds
// one user accessor to concrete ops on one concrete resource instance
// (catalog/operator/model/knowledge_network/…).
//
// This is the gateway-exposed, audited management surface for object grants.
// The internal /api/safe/v1/authz/policies endpoints stay for service-to-service
// "grant the creator access on resource create" calls; here every write is
// validated (known user, concrete resource, catalog-registered ops) so the UI
// can't mint dead policies.
//
// Grantees are USERS only. Departments are intentionally unsupported: casbin
// holds no user→department membership rules, so a department grant would be a
// dead policy that never matches at enforce time (see RolePermissions path for
// the role-based alternative).
func registerObjectGrants(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB) {
	// GET /object-grants?accessor_id=&resource_type=&resource_id=&search=&offset=&limit=
	// Aliases: obj_type=resource_type, obj_id=resource_id.
	// -> { entries:[...], total, summary?:{ grants, objects, grantees } }
	// limit omitted = return all matches (backward compatible). limit present:
	// defaults to 50, capped at 500. search matches user account/name or resource id.
	g.GET("/object-grants", RequirePermission(e, "admin-authz", "view"), func(c *gin.Context) {
		accessorID := c.Query("accessor_id")
		resourceType := objectGrantQueryParam(c, "resource_type", "obj_type")
		resourceID := objectGrantQueryParam(c, "resource_id", "obj_id")
		search := strings.TrimSpace(c.Query("search"))

		// Read the casbin_rule grant table directly (not casbin's in-memory
		// GetPolicy) so filtering, grouping and pagination all happen in SQL:
		// the query is O(page) instead of materializing every grant. Object keys
		// are "type:id" (obj()); splitObjectKey splits on the FIRST colon, so the
		// rtype/rid expressions below mirror it with INSTR/SUBSTR — portable
		// across sqlite (tests) and MariaDB (prod). casbin autosave keeps this
		// table in sync with the in-memory model on every grant/revoke.
		const rtypeExpr = "SUBSTR(v1, 1, INSTR(v1, ':') - 1)"
		const ridExpr = "SUBSTR(v1, INSTR(v1, ':') + 1)"

		where := []string{
			"ptype = 'p'",
			"INSTR(v1, ':') > 0",               // has the type:id shape
			ridExpr + " NOT IN ('', '*')",      // concrete instance only (skip type-wide / bare "*")
			"v0 NOT IN (SELECT id FROM roles)", // role subjects are not user object grants
			"v0 <> ?",                          // exclude the public accessor
		}
		args := []any{authz.PublicAccessorID}
		if accessorID != "" {
			where = append(where, "v0 = ?")
			args = append(args, accessorID)
		}
		if resourceType != "" {
			where = append(where, rtypeExpr+" = ?")
			args = append(args, resourceType)
		}
		if resourceID != "" {
			where = append(where, ridExpr+" = ?")
			args = append(args, resourceID)
		}
		if search != "" {
			like := "%" + search + "%"
			where = append(where,
				"(v0 IN (SELECT id FROM users WHERE account LIKE ? OR name LIKE ?) OR "+ridExpr+" LIKE ?)")
			args = append(args, like, like, like)
		}
		whereSQL := strings.Join(where, " AND ")
		qdb := db.WithContext(c.Request.Context())

		// Grouped views for the admin UI: group_by=object lists distinct objects
		// (each with its grantee count + union of ops), group_by=grantee lists
		// distinct grantees (each with its object count). The UI paginates GROUPS
		// (e.g. 10 objects/page), which a flat grant page cannot serve — one
		// object's grants may span pages, so client-side grouping would have to
		// pull every grant. Grouping happens in SQL, so a page stays small
		// regardless of the total grant count.
		if gb := c.Query("group_by"); gb == "object" || gb == "grantee" {
			listGroupedObjectGrants(c, qdb, gb, whereSQL, args)
			return
		}

		// total = number of (accessor, object) groups after filtering.
		var total int64
		if err := qdb.Raw(
			"SELECT COUNT(*) FROM (SELECT 1 FROM casbin_rule WHERE "+whereSQL+" GROUP BY v0, v1) t",
			args...).Scan(&total).Error; err != nil {
			serverError(c, err)
			return
		}

		resp := gin.H{"total": total}
		if c.Query("include_summary") == "true" {
			var objects, grantees int64
			if err := qdb.Raw("SELECT COUNT(DISTINCT v1) FROM casbin_rule WHERE "+whereSQL, args...).
				Scan(&objects).Error; err != nil {
				serverError(c, err)
				return
			}
			if err := qdb.Raw("SELECT COUNT(DISTINCT v0) FROM casbin_rule WHERE "+whereSQL, args...).
				Scan(&grantees).Error; err != nil {
				serverError(c, err)
				return
			}
			resp["summary"] = gin.H{"grants": total, "objects": objects, "grantees": grantees}
		}

		// entries page: one row per (accessor, object), ops aggregated. Ordered by
		// (v0, v1) so paging is deterministic.
		//
		// GROUP_CONCAT(DISTINCT v2) is safe against MariaDB's default 1024-byte
		// group_concat_max_len: DISTINCT collapses the ops to the operation
		// VOCABULARY (a fixed ~dozen ids like view_detail/modify/authorize), not
		// per-grant, so the concatenation is bounded by vocabulary size — not grant
		// count — and stays far under 1024. Op ids contain no ",", so splitting the
		// result on "," below is safe.
		rowsSQL := "SELECT v0 AS accessor, " + rtypeExpr + " AS rtype, " + ridExpr + " AS rid, " +
			"GROUP_CONCAT(DISTINCT v2) AS ops FROM casbin_rule WHERE " + whereSQL +
			" GROUP BY v0, v1 ORDER BY v0, v1"
		rowArgs := append([]any{}, args...)
		if _, limitSet := c.GetQuery("limit"); limitSet {
			limit := atoiDefault(c.Query("limit"), 0)
			if limit <= 0 {
				limit = 50
			}
			if limit > 500 {
				limit = 500
			}
			offset := atoiDefault(c.Query("offset"), 0)
			if offset < 0 {
				offset = 0
			}
			rowsSQL += " LIMIT ? OFFSET ?"
			rowArgs = append(rowArgs, limit, offset)
		}

		var rows []struct {
			Accessor string
			Rtype    string
			Rid      string
			Ops      string
		}
		if err := qdb.Raw(rowsSQL, rowArgs...).Scan(&rows).Error; err != nil {
			serverError(c, err)
			return
		}

		entries := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			var ops []string
			if row.Ops != "" {
				ops = strings.Split(row.Ops, ",")
			}
			entries = append(entries, gin.H{
				"accessor_id": row.Accessor,
				"resource":    gin.H{"type": row.Rtype, "id": row.Rid},
				"operations":  ops,
			})
		}
		resp["entries"] = entries

		c.JSON(http.StatusOK, resp)
	})

	// POST /object-grants — set (replace) a user's exact op set on one concrete
	// resource instance. { accessor_id, resource{type,id}, operations:[...] }
	// Upsert semantics: the grant's ops become exactly `operations`. An empty
	// list is rejected (use DELETE to revoke) so an accidental empty body can't
	// silently wipe a grant.
	g.POST("/object-grants", RequirePermission(e, "admin-authz", "grant"), func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
			Operations []string    `json:"operations" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if req.Resource.ID == "" || req.Resource.ID == "*" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "object grants target a concrete resource id (not \"*\")"})
			return
		}
		if len(req.Operations) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "operations required; use DELETE to revoke a grant"})
			return
		}
		// safe_admin:console:manage is exactly what CanAdmin tests, so granting it
		// here would promote any grantee to platform administrator through the
		// object-grant route — bypassing role binding and its escalation guards.
		// Administrative capability is role-conferred only.
		if req.Resource.Type == adminConsoleResourceType {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin console capability is granted by role binding, not by object grants"})
			return
		}
		// Grantee must be a user (apps are user rows too). Departments/groups are
		// rejected: their grants never match at enforce time.
		ok, err := isUserAccessor(c, db, req.AccessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "accessor_id must be a known user (object grants support user grantees only)"})
			return
		}
		// Ops must be registered for the resource type — blocks typos that would
		// create policies no /check can ever satisfy.
		valid, err := catalogOpSet(db, req.Resource.Type)
		if err != nil {
			serverError(c, err)
			return
		}
		if len(valid) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown resource type: " + req.Resource.Type})
			return
		}
		for _, op := range req.Operations {
			if !valid[op] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "operation not registered for " + req.Resource.Type + ": " + op})
				return
			}
		}
		if err := e.SetObjectPermissions(req.AccessorID, req.Resource.Type, req.Resource.ID, req.Operations); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /object-grants — revoke one user's grant on one concrete resource
	// instance, leaving other grantees on the same resource untouched.
	// { accessor_id, resource{type,id} }
	g.DELETE("/object-grants", RequirePermission(e, "admin-authz", "revoke"), func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if req.Resource.ID == "" || req.Resource.ID == "*" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "object grants target a concrete resource id (not \"*\")"})
			return
		}
		if err := e.RemoveAccessorResourcePolicies(req.AccessorID, req.Resource.Type, req.Resource.ID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// listGroupedObjectGrants serves the grouped, paginated object-grant views the
// admin UI needs: group_by=object (distinct objects, each with a grantee count)
// or group_by=grantee (distinct grantees, each with an object count). Both carry
// the union of operations. Grouping + pagination run in SQL so a page is a
// handful of groups no matter how many grants exist. `whereSQL`/`args` are the
// same concrete-grant filter the flat listing uses (roles/public/type-wide
// already excluded, plus any request filters).
func listGroupedObjectGrants(c *gin.Context, qdb *gorm.DB, groupBy, whereSQL string, args []any) {
	keyCol, cntCol := "v1", "v0" // group_by=object: key on the object, count grantees
	if groupBy == "grantee" {
		keyCol, cntCol = "v0", "v1"
	}

	var total int64
	if err := qdb.Raw(
		"SELECT COUNT(*) FROM (SELECT 1 FROM casbin_rule WHERE "+whereSQL+" GROUP BY "+keyCol+") t",
		args...).Scan(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	// GROUP_CONCAT(DISTINCT v2): as in the flat listing, DISTINCT collapses ops to
	// the fixed operation vocabulary (a comma-free ~dozen ids), so the result
	// stays well under group_concat_max_len and splits cleanly on ",".
	sql := "SELECT " + keyCol + " AS k, COUNT(DISTINCT " + cntCol + ") AS cnt, " +
		"GROUP_CONCAT(DISTINCT v2) AS ops FROM casbin_rule WHERE " + whereSQL +
		" GROUP BY " + keyCol + " ORDER BY " + keyCol
	rowArgs := append([]any{}, args...)
	if _, limitSet := c.GetQuery("limit"); limitSet {
		limit := atoiDefault(c.Query("limit"), 0)
		if limit <= 0 {
			limit = 50
		}
		if limit > 500 {
			limit = 500
		}
		offset := atoiDefault(c.Query("offset"), 0)
		if offset < 0 {
			offset = 0
		}
		sql += " LIMIT ? OFFSET ?"
		rowArgs = append(rowArgs, limit, offset)
	}

	var rows []struct {
		K   string
		Cnt int64
		Ops string
	}
	if err := qdb.Raw(sql, rowArgs...).Scan(&rows).Error; err != nil {
		serverError(c, err)
		return
	}

	groups := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		var ops []string
		if r.Ops != "" {
			ops = strings.Split(r.Ops, ",")
		}
		if groupBy == "object" {
			rtype, rid, _ := strings.Cut(r.K, ":")
			groups = append(groups, gin.H{
				"object":        gin.H{"type": rtype, "id": rid},
				"grantee_count": r.Cnt,
				"operations":    ops,
			})
		} else {
			groups = append(groups, gin.H{
				"accessor_id":  r.K,
				"object_count": r.Cnt,
				"operations":   ops,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups, "total": total})
}

// isUserAccessor reports whether id is a known user row (real user or app
// account; both are model.User distinguished by account_type).
func isUserAccessor(c *gin.Context, db *gorm.DB, id string) (bool, error) {
	var n int64
	if err := db.WithContext(c.Request.Context()).Model(&model.User{}).
		Where("id = ?", id).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

func objectGrantQueryParam(c *gin.Context, primary, alias string) string {
	if v := c.Query(primary); v != "" {
		return v
	}
	return c.Query(alias)
}

// catalogOpSet returns the resource type's registered operation ids as a set
// (membership-test form of catalogOps).
func catalogOpSet(db *gorm.DB, resourceType string) (map[string]bool, error) {
	ops, err := catalogOps(db, resourceType)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ops))
	for _, op := range ops {
		set[op] = true
	}
	return set, nil
}
