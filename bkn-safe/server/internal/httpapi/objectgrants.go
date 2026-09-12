// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/finegrained"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
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
// isConcreteResourceID reports whether an id names ONE instance.
//
// Rejecting only the literal "*" is not enough: the casbin matcher is keyMatch,
// which treats a "*" ANYWHERE in the object as a wildcard. An id of "tb-*" is
// stored verbatim by SetObjectPermissions and then matches every tool_box whose
// id starts with "tb-", including ones created later — a grant the console can
// neither show nor revoke, because every screen there works from a concrete id.
//
// Concrete ids never contain "*" (they are ULIDs, UUIDs or slugs), so refusing
// the character outright costs nothing and closes the shape entirely.
func isConcreteResourceID(id string) bool {
	return id != "" && !strings.Contains(id, "*")
}

// opAuthorize is the resource-level operation that lets someone who is NOT a
// platform administrator hand out access to one concrete object. The domain
// lifecycle writes it as a separate system-derived owner grant on the resource
// management root. BKN child resource types never expose it: their knowledge
// network is the sole authorization root.
const opAuthorize = "authorize"

// grantableUserPageSize caps the owner-facing account picker. Deliberately not
// caller-tunable: see the Limit comment in the handler.
const grantableUserPageSize = 20

// grantAuthority is how a caller earned the right to write grants on one object.
// It is recorded in the audit trail because "the security administrator opened
// this up" and "the owner shared their own object" are different acts that would
// otherwise be indistinguishable — both arrive as the same endpoint call.
type grantAuthority string

const (
	// Platform-wide admin-authz:grant / :revoke. Unrestricted: may write any op
	// on any object, including opAuthorize itself.
	authorityAdminAuthz grantAuthority = "admin-authz"
	// A direct object grant carrying opAuthorize on this exact instance — the row
	// the creator receives. Restricted (see restrictDelegatedOps).
	authorityOwner grantAuthority = "owner"
	// A type-wide role grant carrying opAuthorize — a role saying "every object of
	// this type may be delegated by anyone holding me". Honoring it here does not
	// widen the policy, it stops ignoring it. Restricted the same way as owner.
	//
	// The three types that carry data no longer have one: sharing a knowledge
	// network, a data connection or a table is its creator's call, so the grant
	// sits on the object rather than the type (#513, #977, #1150). network_builder
	// still holds it type-wide on connector_type, stream_data_pipeline and the
	// execution-factory types (operator, skill, mcp, tool_box), and a custom role
	// may be given one anywhere — this branch is what makes such a grant decide
	// something.
	authorityTypeAuthorize grantAuthority = "type-authorize"
)

// resolveGrantAuthority decides whether the caller may write object grants on
// ref, and on what footing. adminOp is the admin-authz operation this endpoint
// corresponds to ("grant" or "revoke"). It replies to the client and returns
// false when the answer is no, so callers just return.
//
// Order matters: administrators are answered without reading any policy for the
// object, so the admin path costs what it did before this existed.
func resolveGrantAuthority(c *gin.Context, e *authz.Enforcer, db *gorm.DB, adminOp string, ref resourceRef) (grantAuthority, bool) {
	sub := c.GetString(ctxAccessorID)
	if sub == "" {
		replyPublicError(c, http.StatusUnauthorized)
		return "", false
	}
	admin, err := e.CheckContext(c.Request.Context(), sub, "admin-authz", "*", adminOp)
	if err != nil {
		serverError(c, err)
		return "", false
	}
	if admin {
		return authorityAdminAuthz, true
	}
	// A concrete instance is required from here on. An empty or wildcard-bearing id would
	// make the ownership lookup below match ANY instance the caller happens to
	// own, and casbin keyMatch would let a "type:*" role grant match it too —
	// either turns "I own one network" into "I may act on the whole type".
	if !isConcreteResourceID(ref.ID) {
		replyPublicError(c, http.StatusForbidden)
		return "", false
	}
	root, ok, err := grantAuthorizationRoot(c.Request.Context(), db, ref)
	if err != nil {
		serverError(c, err)
		return "", false
	}
	if !ok {
		replyPublicError(c, http.StatusForbidden)
		return "", false
	}
	authorized, err := e.CheckContext(c.Request.Context(), sub, root.Type, root.ID, opAuthorize)
	if err != nil {
		serverError(c, err)
		return "", false
	}
	if !authorized {
		replyPublicError(c, http.StatusForbidden)
		return "", false
	}

	// Holding authorize on the knowledge-network root is necessary but does not
	// reveal a child the caller cannot otherwise see. Reading or changing that
	// child's grant configuration therefore also requires its catalog-declared
	// view operation through the final operation decision (including requires).
	viewOp, err := resourceViewOperation(c.Request.Context(), db, ref.Type)
	if err != nil {
		serverError(c, err)
		return "", false
	}
	visible, err := e.CheckContext(c.Request.Context(), sub, ref.Type, ref.ID, viewOp)
	if err != nil {
		serverError(c, err)
		return "", false
	}
	if !visible {
		replyPublicError(c, http.StatusForbidden)
		return "", false
	}

	// Preserve the audit distinction between an instance owner and a holder of
	// broader delegated authority. The security decision above is always the
	// final operation decision; this direct read is diagnostic only.
	direct, err := e.ListObjectGrants(sub, root.Type, root.ID)
	if err != nil {
		serverError(c, err)
		return "", false
	}
	for _, grant := range direct {
		for _, op := range grant.Operations {
			if op == opAuthorize {
				return authorityOwner, true
			}
		}
	}
	return authorityTypeAuthorize, true
}

// grantAuthorizationRoot applies the BKN boundary: a knowledge-network is the
// sole authorization-management root for its registered child resources.
// Other resource families manage grants on the concrete resource itself.
func grantAuthorizationRoot(ctx context.Context, db *gorm.DB, ref resourceRef) (resourceRef, bool, error) {
	var resourceType model.ResourceType
	if err := db.WithContext(ctx).First(&resourceType, "id = ?", ref.Type).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceRef{}, false, nil
		}
		return resourceRef{}, false, err
	}
	if resourceType.ParentTypeID != "knowledge_network" {
		return ref, true, nil
	}
	var parent model.ResourceParent
	err := db.WithContext(ctx).First(&parent,
		"resource_type_id = ? AND resource_id = ?", ref.Type, ref.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return resourceRef{}, false, nil
	}
	if err != nil {
		return resourceRef{}, false, err
	}
	if parent.ParentTypeID != "knowledge_network" || !isConcreteResourceID(parent.ParentID) {
		return resourceRef{}, false, nil
	}
	return resourceRef{Type: parent.ParentTypeID, ID: parent.ParentID}, true, nil
}

// resourceViewOperation resolves the view vocabulary from the registered
// operation catalog. The authorization engine does not normalize these names:
// BKN/Vega use view_detail, execution-factory uses view, and models use display.
func resourceViewOperation(ctx context.Context, db *gorm.DB, resourceType string) (string, error) {
	var ids []string
	if err := db.WithContext(ctx).Model(&model.Operation{}).
		Where("resource_type_id = ? AND id IN ?", resourceType, []string{"view_detail", "view", "display"}).
		Pluck("id", &ids).Error; err != nil {
		return "", err
	}
	for _, preferred := range []string{"view_detail", "view", "display"} {
		for _, id := range ids {
			if id == preferred {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("resource type %q does not declare a view operation", resourceType)
}

func isBKNChildResourceType(ctx context.Context, db *gorm.DB, resourceType string) (bool, error) {
	var parentTypeID string
	result := db.WithContext(ctx).Model(&model.ResourceType{}).
		Select("parent_type_id").Where("id = ?", resourceType).Scan(&parentTypeID)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, gorm.ErrRecordNotFound
	}
	return parentTypeID == "knowledge_network", nil
}

// restrictDelegatedOps enforces the two limits on a non-administrator writing a
// Professional rule. It replies and returns false when the request breaks
// either. Community bundles are protected platform state and never reach this
// helper.
//
//  1. The delegation chain is one deep: opAuthorize is administrator-conferred
//     only. A delegate handing out opAuthorize would mint another delegate, and
//     the set of people who can open an object up would grow without any
//     administrator ever acting.
//  2. A delegate cannot pass on more than it holds itself. Without this, someone
//     holding only view_detail plus opAuthorize could grant modify — writing a
//     permission that was never given to them.
//
// Administrators skip both: admin-authz:grant is the platform-level authority
// these two rules exist to protect.
func restrictDelegatedOps(c *gin.Context, e *authz.Enforcer, ref resourceRef, ops []string) bool {
	for _, op := range ops {
		if op == opAuthorize {
			replyPublicError(c, http.StatusForbidden)
			return false
		}
	}
	held, err := e.AllowedOpsContext(c.Request.Context(), c.GetString(ctxAccessorID), ref.Type, ref.ID, ops)
	if err != nil {
		serverError(c, err)
		return false
	}
	heldSet := make(map[string]bool, len(held))
	for _, op := range held {
		heldSet[op] = true
	}
	for _, op := range ops {
		if !heldSet[op] {
			replyPublicError(c, http.StatusForbidden)
			return false
		}
	}
	return true
}

func registerObjectGrants(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB) {
	// GET /policies?resource_type=&resource_id= — the p-lines written directly
	// against this exact object key, grouped by subject. -> { entries:[
	// { accessor_id, resource{type,id}, operations:[...] } ] }
	//
	// Scope, stated precisely because "who can act on this object" is easy to
	// over-read: accessor_id is the policy SUBJECT verbatim, so a role-held grant
	// appears as the role's id, unmarked and NOT expanded into its members; and a
	// type-wide grant ("type:*", e.g. a role or super-admin holding the whole
	// type) does not match a concrete resource_id and is absent. The result is
	// therefore the direct grant table for one instance, not the effective user
	// set. That is the same contract the internal endpoint has always had —
	// interpretation stays with the caller — and per-accessor effective
	// permissions remain GET /me/permissions's job.
	//
	// This is the token-gated twin of the internal GET /api/safe/v1/authz/policies:
	// that one is ClusterIP-only and unauthenticated (service-to-service), and the
	// gateway does not expose it, so a console user reviewing policies had no
	// endpoint to call. Reads are gated on admin-authz:view, which the audit role
	// holds — policy review is exactly its job — while the write points
	// (grant/revoke) stay out of its grant set.
	g.GET("/policies", RequirePermission(e, "admin-authz", "view"), func(c *gin.Context) {
		resourceType := objectGrantQueryParam(c, "resource_type", "obj_type")
		resourceID := objectGrantQueryParam(c, "resource_id", "obj_id")
		if resourceType == "" {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		policies, err := e.ResourcePolicies(resourceType, resourceID)
		if err != nil {
			serverError(c, err)
			return
		}
		entries := make([]gin.H, 0, len(policies))
		for _, p := range policies {
			ref := resourceRef{Type: resourceType, ID: resourceID}
			grants, err := objectGrantRecords(e, p.AccessorID, ref)
			if err != nil {
				serverError(c, err)
				return
			}
			decisions, err := effectiveObjectGrantDecisions(c.Request.Context(), e, db, p.AccessorID, ref,
				append(append([]string{}, p.Operations...), p.DeniedOperations...))
			if err != nil {
				serverError(c, err)
				return
			}
			entries = append(entries, gin.H{
				"accessor_id":         p.AccessorID,
				"resource":            gin.H{"type": resourceType, "id": resourceID},
				"operations":          p.Operations,
				"denied_operations":   p.DeniedOperations,
				"grants":              grants,
				"effective_decisions": decisions,
			})
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries})
	})

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
			// Managed proxies are system-owned runtime identities. Their policies
			// are maintained through the proxy lifecycle API, not by people in the
			// authorization-management UI. Keeping them out here also prevents a
			// client from resolving a proxy accessor through the user directory,
			// where proxies are intentionally invisible.
			"v0 NOT IN (SELECT proxy_account_id FROM managed_proxy_accounts)",
			"v0 <> ?", // exclude the public accessor
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
			"COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN v3 = 'deny' THEN NULL ELSE v2 END), '') AS ops, " +
			"COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN v3 = 'deny' THEN v2 ELSE NULL END), '') AS denied_ops " +
			"FROM casbin_rule WHERE " + whereSQL +
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
			Accessor  string
			Rtype     string
			Rid       string
			Ops       string
			DeniedOps string
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
			ref := resourceRef{Type: row.Rtype, ID: row.Rid}
			ops = projectDirectGrantOps(ref.Type, ops)
			grants, err := objectGrantRecords(e, row.Accessor, ref)
			if err != nil {
				serverError(c, err)
				return
			}
			deniedOps := splitGrantOps(row.DeniedOps)
			decisions, err := effectiveObjectGrantDecisions(c.Request.Context(), e, db, row.Accessor, ref,
				append(append([]string{}, ops...), deniedOps...))
			if err != nil {
				serverError(c, err)
				return
			}
			entries = append(entries, gin.H{
				"accessor_id":         row.Accessor,
				"resource":            gin.H{"type": ref.Type, "id": ref.ID},
				"operations":          ops,
				"denied_operations":   deniedOps,
				"grants":              grants,
				"effective_decisions": decisions,
			})
		}
		resp["entries"] = entries

		c.JSON(http.StatusOK, resp)
	})

	// POST /object-grants accepts one of two shapes. Community stores the reviewed
	// top-level full_business_access bundle; Professional and above may replace a
	// source-scoped allow/deny operation set. The handler derives both provenance
	// fields, normalizes direct requirements, and applies the same checks on the
	// administrator and /me delegation routes.
	g.POST("/object-grants", setObjectGrantHandler(e, db))
	g.POST("/object-grants/preview", RequirePermission(e, "admin-authz", "view"), previewObjectGrantHandler(e))

	// DELETE /object-grants revokes exactly one stable grant_id. It never deletes
	// by the Casbin tuple, so a sibling grant with identical runtime semantics but
	// a different source or lifecycle owner remains intact.
	//
	// An administrator gets idempotent 204 for an already-absent id, with
	// _outcome.removed=false in the audit record. A delegated caller cannot prove
	// authority over an opaque missing id and therefore receives 403.
	g.DELETE("/object-grants", revokeObjectGrantHandler(e, db))
}

func previewObjectGrantHandler(e *authz.Enforcer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
			Bundle     string      `json:"bundle" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		bundleOps, supported := authz.CommunityBundleOperations(req.Resource.Type)
		if req.Bundle != authz.ActFullBusinessAccess || !supported || !isConcreteResourceID(req.Resource.ID) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		records, err := e.PolicyRecords(authz.PolicyFilter{
			AccessorID: req.AccessorID,
			Object:     req.Resource.Type + ":" + req.Resource.ID,
		})
		if err != nil {
			serverError(c, err)
			return
		}
		legacy := make([]gin.H, 0)
		legacyAllows := map[string]bool{}
		for _, record := range records {
			if record.PolicySource != authz.PolicySourceLegacy {
				continue
			}
			legacy = append(legacy, policyRecordJSON(record))
			if record.Active && record.Effect == authz.EffectAllow {
				legacyAllows[record.Operation] = true
			}
		}
		added := make([]string, 0, len(bundleOps))
		for _, operation := range bundleOps {
			if !legacyAllows[operation] {
				added = append(added, operation)
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"accessor_id":       req.AccessorID,
			"resource":          gin.H{"type": req.Resource.Type, "id": req.Resource.ID},
			"bundle":            req.Bundle,
			"bundle_operations": bundleOps,
			"added_operations":  added,
			"legacy_grants":     legacy,
		})
	}
}

func policyRecordJSON(record authz.PolicyRecord) gin.H {
	return gin.H{
		"grant_id": record.GrantID, "accessor_id": record.AccessorID,
		"operation": record.Operation, "effect": record.Effect,
		"policy_source": record.PolicySource, "authority_source": record.AuthoritySource,
		"active": record.Active, "inherited": false,
	}
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
		"COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN v3 = 'deny' THEN NULL ELSE v2 END), '') AS ops, " +
		"COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN v3 = 'deny' THEN v2 ELSE NULL END), '') AS denied_ops " +
		"FROM casbin_rule WHERE " + whereSQL +
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
		K         string
		Cnt       int64
		Ops       string
		DeniedOps string
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
				"object":            gin.H{"type": rtype, "id": rid},
				"grantee_count":     r.Cnt,
				"operations":        ops,
				"denied_operations": splitGrantOps(r.DeniedOps),
			})
		} else {
			groups = append(groups, gin.H{
				"accessor_id":       r.K,
				"object_count":      r.Cnt,
				"operations":        ops,
				"denied_operations": splitGrantOps(r.DeniedOps),
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups, "total": total})
}

func splitGrantOps(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, ",")
}

// isUserAccessor reports whether id is a known user row (real user or app
// account; both are model.User distinguished by account_type).
func isUserAccessor(c *gin.Context, db *gorm.DB, id string) (bool, error) {
	var n int64
	if err := db.WithContext(c.Request.Context()).Model(&model.User{}).
		Where("id = ? AND enabled = ?", id, true).Count(&n).Error; err != nil {
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

// registerMeObjectGrants mounts the self-service mirror of the object-grant
// writes under /api/safe/v1/me. Same handlers as the administrator surface — the
// authority test inside them (resolveGrantAuthority) is what differs, and it
// already accepts both an administrator and the object's owner.
//
// Why /me and not a group of its own: the gateway routes exactly three bkn-safe
// prefixes (/api/safe/v1/admin, /me, /capabilities). A fourth would need an
// ingress change on every cluster before the feature worked anywhere, for a
// surface that is genuinely self-service — "the objects I own", the same footing
// as /me/api-keys being "the keys I own".
//
// The platform-wide listing is deliberately NOT mirrored here. An owner may read
// and write the grants on an object they own, one object at a time; "show me
// every grant on the platform" stays with the administrator.
func registerMeObjectGrants(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB, dir *directory.Service) {
	// GET /object-grants?resource_type=&resource_id= — who currently holds what
	// on ONE object. The share UI opens with this: an owner about to hand their
	// network to a colleague has to see who already has it, and submitting
	// without that read would silently replace someone else's operation set
	// (POST is replace, not merge).
	g.GET("/object-grants", func(c *gin.Context) {
		ref := resourceRef{
			Type: objectGrantQueryParam(c, "resource_type", "obj_type"),
			ID:   objectGrantQueryParam(c, "resource_id", "obj_id"),
		}
		if ref.Type == "" || ref.ID == "" {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		// Reading who has access is itself a privilege on the object: the same
		// authority that lets a caller change the grants lets it see them.
		if _, ok := resolveGrantAuthority(c, e, db, "view", ref); !ok {
			return
		}
		policies, err := e.ResourcePolicies(ref.Type, ref.ID)
		if err != nil {
			serverError(c, err)
			return
		}
		ids := make([]string, 0, len(policies))
		for _, policy := range policies {
			ids = append(ids, policy.AccessorID)
		}
		// Names are resolved here rather than left to the caller: the owner-facing
		// surface has no user directory of its own (that is admin-only), so a
		// client would have nothing to turn an accessor id into a person with.
		named, err := grantAccessorNames(c, db, ids)
		if err != nil {
			serverError(c, err)
			return
		}
		entries := make([]gin.H, 0, len(policies))
		for _, policy := range policies {
			grants, err := objectGrantRecords(e, policy.AccessorID, ref)
			if err != nil {
				serverError(c, err)
				return
			}
			decisions, err := effectiveObjectGrantDecisions(c.Request.Context(), e, db, policy.AccessorID, ref,
				append(append([]string{}, policy.Operations...), policy.DeniedOperations...))
			if err != nil {
				serverError(c, err)
				return
			}
			entry := gin.H{
				"accessor_id":         policy.AccessorID,
				"resource":            gin.H{"type": ref.Type, "id": ref.ID},
				"operations":          policy.Operations,
				"denied_operations":   policy.DeniedOperations,
				"grants":              grants,
				"effective_decisions": decisions,
			}
			// A row whose subject is a role, or a user since deleted, resolves to
			// nothing. It is still shown — hiding a grant that exists would be
			// worse than showing a bare id.
			if who, ok := named[policy.AccessorID]; ok {
				entry["accessor_account"] = who.Account
				entry["accessor_name"] = who.Name
			}
			entries = append(entries, entry)
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries})
	})

	// GET /grantable-users?resource_type=&resource_id=&search=&limit= — the people
	// an owner may pick when sharing ONE object.
	//
	// The platform user directory is admin-only, which left the owner surface
	// unusable: you cannot grant to someone you cannot name. Rather than opening
	// the directory to every logged-in account, this read is gated on the very
	// same authority as writing grants on the object named in the query — you can
	// look up candidates exactly when you have something to give them.
	g.GET("/grantable-users", func(c *gin.Context) {
		ref := resourceRef{
			Type: objectGrantQueryParam(c, "resource_type", "obj_type"),
			ID:   objectGrantQueryParam(c, "resource_id", "obj_id"),
		}
		if ref.Type == "" || ref.ID == "" {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		// Search is mandatory. Holding `authorize` on one object says nothing about
		// being allowed to page through the platform's accounts, and an empty
		// search turned this into exactly that — the per-object gate below is not
		// a bound on WHO is listed, only on who may ask.
		search := strings.TrimSpace(c.Query("search"))
		if search == "" {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if _, ok := resolveGrantAuthority(c, e, db, "view", ref); !ok {
			return
		}
		enabled := true
		users, _, err := dir.ListUsers(c.Request.Context(), directory.UserListFilter{
			Search: search,
			// A disabled account cannot log in, so granting it access is a grant
			// that does nothing; keep it out of the picker.
			Enabled: &enabled,
			// Fixed page size, not a caller-supplied one. A picker has no use for
			// a tunable limit, and letting the query string reach the slice
			// pre-allocation inside ListUsers is a memory-exhaustion path for the
			// sake of nothing (CodeQL go/uncontrolled-allocation-size). Narrow the
			// search instead of asking for more rows.
			Limit: grantableUserPageSize,
		})
		if err != nil {
			serverError(c, err)
			return
		}
		out := make([]gin.H, 0, len(users))
		for _, user := range users {
			out = append(out, gin.H{"id": user.ID, "account": user.Account, "name": user.Name})
		}
		c.JSON(http.StatusOK, gin.H{"users": out})
	})
	g.POST("/object-grants", setObjectGrantHandler(e, db))
	g.DELETE("/object-grants", revokeObjectGrantHandler(e, db))
}

// grantAccessorNames resolves grant subjects to accounts, skipping ids that are
// not users (casbin stores role subjects in the same column).
func grantAccessorNames(c *gin.Context, db *gorm.DB, ids []string) (map[string]model.User, error) {
	out := map[string]model.User{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.User
	if err := db.WithContext(c.Request.Context()).Model(&model.User{}).
		Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, nil
}

func setObjectGrantHandler(e *authz.Enforcer, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req objectGrantWriteRequest
		if !bind(c, &req) {
			return
		}

		_, bundleTarget := authz.CommunityBundleOperations(req.Resource.Type)
		communityShape := req.Bundle == authz.ActFullBusinessAccess && req.Operations == nil &&
			(req.Effect == "" || req.Effect == authz.EffectAllow) && bundleTarget
		fineShape := req.Bundle == "" && req.Operations != nil
		if !finegrained.Available() && !communityShape {
			// This response must remain independent of whether the paid code is
			// absent, merely unlicensed, or currently downgraded.
			replyUnsupportedGrantShape(c)
			return
		}
		if !communityShape && !fineShape {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if req.AccessorID == "" || req.Resource.Type == "" || !isConcreteResourceID(req.Resource.ID) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}

		effect := req.Effect
		if effect == "" {
			effect = authz.EffectAllow
		}
		if effect != authz.EffectAllow && effect != authz.EffectDeny {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if fineShape && len(*req.Operations) == 0 {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		// safe_admin:console:manage is exactly what CanAdmin tests, so granting it
		// here would promote any grantee to platform administrator through the
		// object-grant route — bypassing role binding and its escalation guards.
		if req.Resource.Type == adminConsoleResourceType {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		managed, err := managedproxy.IsManaged(c.Request.Context(), db, req.AccessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		if managed {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		authority, ok := resolveGrantAuthority(c, e, db, "grant", req.Resource)
		if !ok {
			return
		}
		if effect == authz.EffectDeny && authority != authorityAdminAuthz {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		ok, err = isUserAccessor(c, db, req.AccessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		if !ok {
			replyPublicError(c, http.StatusBadRequest)
			return
		}

		authoritySource := authz.AuthoritySourceAdminAuthz
		if authority != authorityAdminAuthz {
			authoritySource = authz.AuthoritySourceOwnerDelegate
		}
		outcome := map[string]any{"via": string(authority), "effect": effect}
		if communityShape {
			// The Community compatibility bundle is a protected source. Unlike a
			// Professional rule it cannot be created, replaced, or revoked by an
			// object-level delegate, even when that delegate happens to hold every
			// operation represented by the bundle.
			if authority != authorityAdminAuthz {
				replyPublicError(c, http.StatusForbidden)
				return
			}
			if err := e.GrantCommunityBundle(req.AccessorID, req.Resource.Type, req.Resource.ID, authoritySource); err != nil {
				serverError(c, err)
				return
			}
			outcome["bundle"] = authz.ActFullBusinessAccess
			setAuditOutcome(c, outcome)
			c.Status(http.StatusNoContent)
			return
		}

		valid, err := catalogOpSet(db, req.Resource.Type)
		if err != nil {
			serverError(c, err)
			return
		}
		if len(valid) == 0 {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		bknChild, err := isBKNChildResourceType(c.Request.Context(), db, req.Resource.Type)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		for _, op := range *req.Operations {
			if !valid[op] {
				replyPublicError(c, http.StatusBadRequest)
				return
			}
			// authorize is managed only through its dedicated platform/lifecycle
			// contract; BKN child types must not expose it even if an old catalog
			// row survives until #1432's directory migration.
			if op == opAuthorize && (bknChild || authority != authorityAdminAuthz) {
				replyPublicError(c, http.StatusForbidden)
				return
			}
		}
		ops := append([]string(nil), (*req.Operations)...)
		if effect == authz.EffectAllow {
			ops, err = e.NormalizeOperations(c.Request.Context(), req.Resource.Type, ops)
			if err != nil {
				serverError(c, err)
				return
			}
		}
		if authority != authorityAdminAuthz && !restrictDelegatedOps(c, e, req.Resource, ops) {
			return
		}
		if required := addedOps(*req.Operations, ops); len(required) > 0 {
			outcome["required_operations"] = required
		}
		setAuditOutcome(c, outcome)
		if err := e.SetProfessionalObjectPermissions(req.AccessorID, req.Resource.Type, req.Resource.ID,
			ops, effect, authoritySource); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type objectGrantWriteRequest struct {
	AccessorID string      `json:"accessor_id" binding:"required"`
	Resource   resourceRef `json:"resource" binding:"required"`
	// Operations is a pointer so an omitted field (Community shape) can be
	// distinguished from an explicitly empty Professional set.
	Operations *[]string `json:"operations"`
	Bundle     string    `json:"bundle"`
	Effect     string    `json:"effect"`
}

func objectGrantRecords(e *authz.Enforcer, accessorID string, ref resourceRef) ([]gin.H, error) {
	records, err := e.PolicyRecords(authz.PolicyFilter{
		AccessorID: accessorID,
		Object:     ref.Type + ":" + ref.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]gin.H, 0, len(records))
	for _, record := range records {
		out = append(out, policyRecordJSON(record))
	}
	return out, nil
}

func projectDirectGrantOps(resourceType string, operations []string) []string {
	out := make([]string, 0, len(operations))
	seen := map[string]bool{}
	for _, operation := range operations {
		projected := []string{operation}
		if operation == authz.ActFullBusinessAccess {
		if operation == authz.ActFullBusinessAccess {
			if bundleOps, supported := authz.CommunityBundleOperations(resourceType); supported {
				projected = bundleOps
			}
		}
		}
		for _, item := range projected {
			if !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
	}
	return out
}

func effectiveObjectGrantDecisions(ctx context.Context, e *authz.Enforcer, db *gorm.DB,
	accessorID string, ref resourceRef, operations []string) ([]gin.H, error) {
	operations = projectDirectGrantOps(ref.Type, operations)
	out := make([]gin.H, 0, len(operations))
	for _, operation := range operations {
		decision, err := e.OperationDecision(ctx, accessorID, ref.Type, ref.ID, operation)
		if err != nil {
			return nil, err
		}
		item := gin.H{"operation": operation, "decision": decision.Decision, "basis": decision.Basis}
		if len(decision.Requirements) > 0 {
			item["requires"] = decision.Requirements
		}
		if decision.DeniedRequirement != "" {
			item["denied_requirement"] = decision.DeniedRequirement
			item["requirement_basis"] = decision.RequirementBasis
		}
		if decision.Basis == authz.BasisInherited {
			var parent model.ResourceParent
			if err := db.WithContext(ctx).First(&parent,
				"resource_type_id = ? AND resource_id = ?", ref.Type, ref.ID).Error; err != nil &&
				!errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			} else if err == nil {
				var op model.Operation
				if err := db.WithContext(ctx).First(&op,
					"resource_type_id = ? AND id = ?", ref.Type, operation).Error; err != nil &&
					!errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, err
				} else if err == nil {
					item["inherited_from"] = gin.H{
						"resource":  gin.H{"type": parent.ParentTypeID, "id": parent.ParentID},
						"operation": op.ParentOperationID,
					}
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// addedOps returns the members of normalized that were not in requested, in
// normalized order — the direct requirements added on top of the requested set.
func addedOps(requested, normalized []string) []string {
	asked := make(map[string]bool, len(requested))
	for _, op := range requested {
		asked[op] = true
	}
	var out []string
	for _, op := range normalized {
		if !asked[op] {
			out = append(out, op)
		}
	}
	return out
}

func revokeObjectGrantHandler(e *authz.Enforcer, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			GrantID string `json:"grant_id" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		req.GrantID = strings.TrimSpace(req.GrantID)
		if req.GrantID == "" || len(req.GrantID) > 64 {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		records, err := e.PolicyRecords(authz.PolicyFilter{GrantID: req.GrantID})
		if err != nil {
			serverError(c, err)
			return
		}
		if len(records) == 0 {
			allowed, err := e.CheckContext(c.Request.Context(), c.GetString(ctxAccessorID),
				"admin-authz", "*", "revoke")
			if err != nil {
				serverError(c, err)
				return
			}
			if !allowed {
				replyPublicError(c, http.StatusForbidden)
				return
			}
			setAuditOutcome(c, map[string]any{"grant_id": req.GrantID, "removed": false})
			c.Status(http.StatusNoContent)
			return
		}
		record := records[0]
		// Role grants have their own rbac_basic route, capability gate and
		// admin-role:permissions check. Letting a stable ID through this
		// user-object route would bypass all three, particularly after a downgrade
		// where role configuration must remain active but its writer is closed.
		if record.PolicySource == authz.PolicySourceRolePermission {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		resourceType, resourceID, ok := strings.Cut(record.Object, ":")
		if !ok || resourceType == "" || !isConcreteResourceID(resourceID) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		ref := resourceRef{Type: resourceType, ID: resourceID}
		managed, err := managedproxy.IsManaged(c.Request.Context(), db, record.AccessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		if managed {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		// Revoking on an object is the mirror of granting on it: whoever can open
		// their own object up can close it again. No op restriction applies —
		// taking access away can only narrow, never widen.
		authority, ok := resolveGrantAuthority(c, e, db, "revoke", ref)
		if !ok {
			return
		}
		// A delegated owner may revoke only an ordinary allow produced by the
		// delegated Professional writer. Stable source identity makes this check
		// sufficient: selecting that row cannot remove an administrator deny,
		// authorize, bundle, legacy, system or role sibling.
		if authority != authorityAdminAuthz {
			ordinaryOwnerGrant := record.PolicySource == authz.PolicySourceProfessionalRule &&
				record.AuthoritySource == authz.AuthoritySourceOwnerDelegate &&
				record.Effect == authz.EffectAllow && record.Operation != opAuthorize
			if !ordinaryOwnerGrant {
				replyPublicError(c, http.StatusForbidden)
				return
			}
		}
		removed, err := e.RevokePolicy(req.GrantID)
		if err != nil {
			serverError(c, err)
			return
		}
		setAuditOutcome(c, map[string]any{
			"grant_id": req.GrantID, "policy_source": record.PolicySource,
			"authority_source": record.AuthoritySource, "removed": removed, "via": string(authority),
		})
		c.Status(http.StatusNoContent)
	}
}
