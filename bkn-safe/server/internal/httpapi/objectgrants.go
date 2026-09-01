// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
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
// services write it to the creator at create time (bkn-backend and vega call
// CreateResources with COMMON_OPERATIONS, which includes it), so "whoever made
// this" is exactly who holds it. Until these handlers consulted it the operation
// was inert: every write was gated on admin-authz alone, and the person who
// built a knowledge network could not share it (bkn-studio#478).
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
func resolveGrantAuthority(c *gin.Context, e *authz.Enforcer, adminOp string, ref resourceRef) (grantAuthority, bool) {
	sub := c.GetString(ctxAccessorID)
	if sub == "" {
		replyPublicError(c, http.StatusUnauthorized)
		return "", false
	}
	admin, err := e.Check(sub, "admin-authz", "*", adminOp)
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
	// Ownership first, and read as a DIRECT grant rather than through Check:
	// Check cannot tell "granted on this object" from "granted on the whole
	// type", and the audit trail needs them apart.
	direct, err := e.ListObjectGrants(sub, ref.Type, ref.ID)
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
	ok, err := e.Check(sub, ref.Type, ref.ID, opAuthorize)
	if err != nil {
		serverError(c, err)
		return "", false
	}
	if ok {
		return authorityTypeAuthorize, true
	}
	replyPublicError(c, http.StatusForbidden)
	return "", false
}

// restrictDelegatedOps enforces the two limits on a non-administrator writing a
// grant. It replies and returns false when the request breaks either.
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
// protectAuthorizeHolder stops a delegate from touching the grant of another
// `authorize` holder — the object's creator included — or the public-access row.
//
// It guards BOTH writes, because both erase. DELETE removes every p-line the
// accessor holds on the object; POST is replace-semantics, so writing
// operations:["view_detail"] onto a holder drops everything else in the same
// motion. And restrictDelegatedOps forbids a delegate from putting `authorize`
// back. Without this, anyone the platform trusts to share ONE object could
// silently take that object away from the person who made it, and only a
// platform administrator could undo it — a role carrying `authorize` type-wide
// would be every member of that role against every object of the type.
//
// Same rule as the grant side, in the other direction: `authorize` is
// administrator-conferred, so only an administrator takes it away. Grants
// without `authorize` stay fully editable and revocable, which is what the owner
// surface exists for.
func protectAuthorizeHolder(c *gin.Context, e *authz.Enforcer, ref resourceRef, accessorID string) bool {
	// The public accessor is not a person whose access a delegate may adjust: it
	// is how the execution factory publishes a built-in toolbox to everyone. Its
	// row carries no `authorize`, so the holder check below would wave it through,
	// and removing it would un-publish the toolbox platform-wide — previously an
	// admin-authz:revoke action.
	if accessorID == authz.PublicAccessorID {
		replyPublicError(c, http.StatusForbidden)
		return false
	}
	held, err := e.ListObjectGrants(accessorID, ref.Type, ref.ID)
	if err != nil {
		serverError(c, err)
		return false
	}
	for _, grant := range held {
		for _, op := range grant.Operations {
			if op == opAuthorize {
				replyPublicError(c, http.StatusForbidden)
				return false
			}
		}
	}
	return true
}

func restrictDelegatedOps(c *gin.Context, e *authz.Enforcer, ref resourceRef, ops []string) bool {
	for _, op := range ops {
		if op == opAuthorize {
			replyPublicError(c, http.StatusForbidden)
			return false
		}
	}
	held, err := e.AllowedOps(c.GetString(ctxAccessorID), ref.Type, ref.ID, ops)
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
			entries = append(entries, gin.H{
				"accessor_id": p.AccessorID,
				"resource":    gin.H{"type": resourceType, "id": resourceID},
				"operations":  p.Operations,
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
	g.POST("/object-grants", setObjectGrantHandler(e, db))

	// DELETE /object-grants — revoke one user's grant on one concrete resource
	// instance, leaving other grantees on the same resource untouched.
	// { accessor_id, resource{type,id} } -> 204.
	//
	// Idempotent BY DESIGN: a request naming a grant that does not exist (already
	// revoked, wrong accessor, wrong instance) still answers 204, so a retry or a
	// double-click is safe. The distinction the caller cannot see is recorded in
	// the audit trail instead — Detail carries _outcome.removed, the number of
	// p-lines actually dropped, so 0 is auditable as "matched nothing".
	//
	// The resource TYPE is validated against the catalog even though revoking an
	// unregistered type would harmlessly match nothing: a typo'd type is a silent
	// no-op the operator would read as a successful revoke, which is the worst
	// possible outcome for a security operation.
	g.DELETE("/object-grants", revokeObjectGrantHandler(e, db))
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
		if _, ok := resolveGrantAuthority(c, e, "view", ref); !ok {
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
			entry := gin.H{
				"accessor_id": policy.AccessorID,
				"resource":    gin.H{"type": ref.Type, "id": ref.ID},
				"operations":  policy.Operations,
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
		if _, ok := resolveGrantAuthority(c, e, "view", ref); !ok {
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
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
			Operations []string    `json:"operations" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if !isConcreteResourceID(req.Resource.ID) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if len(req.Operations) == 0 {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		// safe_admin:console:manage is exactly what CanAdmin tests, so granting it
		// here would promote any grantee to platform administrator through the
		// object-grant route — bypassing role binding and its escalation guards.
		// Administrative capability is role-conferred only.
		if req.Resource.Type == adminConsoleResourceType {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		// Administrator, or the object's own owner delegating it. Resolved here
		// rather than in middleware because the owner branch is a question about
		// the object named in the BODY, which middleware cannot see.
		authority, ok := resolveGrantAuthority(c, e, "grant", req.Resource)
		if !ok {
			return
		}
		if authority != authorityAdminAuthz && !protectAuthorizeHolder(c, e, req.Resource, req.AccessorID) {
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
			replyPublicError(c, http.StatusBadRequest)
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
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		for _, op := range req.Operations {
			if !valid[op] {
				replyPublicError(c, http.StatusBadRequest)
				return
			}
		}
		// Add the operations the requested ones imply (#1121). Expanded after
		// validation so a typo is still a 400 rather than something the expansion
		// quietly absorbs. Upsert semantics make this self-healing: a console that
		// clears view_detail while leaving resource_manage ticked sends a set this
		// puts back, instead of storing a grant nothing can use.
		ops, err := impliedOps(db.WithContext(c.Request.Context()), req.Resource.Type, req.Operations)
		if err != nil {
			serverError(c, err)
			return
		}
		// Checked against the EXPANDED set, not what was asked for: the implication
		// pass can add operations, and a delegate must not acquire one that way
		// that it could not have named directly.
		if authority != authorityAdminAuthz && !restrictDelegatedOps(c, e, req.Resource, ops) {
			return
		}
		// The audit Detail snapshots the request body, so without this the trail
		// would say only what was asked for and an implied operation would appear
		// on the accessor with nothing recording where it came from — the one
		// question ("why can this account see this catalog?") the trail exists to
		// answer. The seed's back-fill records its own repairs for the same
		// reason; this keeps the two paths saying the same thing.
		outcome := map[string]any{"via": string(authority)}
		if implied := addedOps(req.Operations, ops); len(implied) > 0 {
			outcome["implied_operations"] = implied
		}
		setAuditOutcome(c, outcome)
		if err := e.SetObjectPermissions(req.AccessorID, req.Resource.Type, req.Resource.ID, ops); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func revokeObjectGrantHandler(e *authz.Enforcer, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {

		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if !isConcreteResourceID(req.Resource.ID) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		// Revoking on an object is the mirror of granting on it: whoever can open
		// their own object up can close it again. No op restriction applies —
		// taking access away can only narrow, never widen.
		authority, ok := resolveGrantAuthority(c, e, "revoke", req.Resource)
		if !ok {
			return
		}
		if authority != authorityAdminAuthz && !protectAuthorizeHolder(c, e, req.Resource, req.AccessorID) {
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
		removed, err := e.RemoveAccessorResourcePolicies(req.AccessorID, req.Resource.Type, req.Resource.ID)
		if err != nil {
			serverError(c, err)
			return
		}
		setAuditOutcome(c, map[string]any{"removed": removed, "via": string(authority)})
		c.Status(http.StatusNoContent)
	}
}
