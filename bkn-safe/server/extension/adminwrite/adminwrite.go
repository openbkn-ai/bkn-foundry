// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package adminwrite is the socket for rbac_basic — custom role, department and
// permission management, the sample Professional capability.
//
// It is the route-registration half of the open-core split (open-core-gating
// §2.5, #278 core decision one): the community binary serves only the read
// routes and never mounts the write routes at all, so a probe of
// POST /admin/roles in a community build gets 404 — the endpoint does not
// exist, rather than existing-but-refusing. The enterprise binary mounts the
// write routes in its Setup and the socket hides them per request when the
// licence does not carry the feature — so an unlicensed enterprise binary
// answers a probe exactly as the community one does.
//
// Why a typed Services surface instead of moving the raw handlers: the write
// handlers carry security invariants — a custom role is forced to source
// "custom", a wildcard grant is refused, the admin-console capability can never
// be handed out through a role permission. Those guards belong in core, next to
// the engine, so they hold no matter what the ee HTTP layer does. ee owns only
// the HTTP shape (JSON binding, status mapping); the guarded operations live
// behind this interface, which core implements over its internal packages. ee
// never imports core's internal/.
//
// The returned errors are typed so the ee layer maps them to status codes
// without duplicating the rules that produced them.
package adminwrite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// Capability is the name this socket registers itself under, and the string the
// capabilities endpoint reports. It is spelled the same as the licence feature
// key it grew out of, but nothing reads it from a certificate any more: the
// tier is what decides (ee-design.md §3.1), and this is a display name.
const Capability = "rbac_basic"

// Sentinel errors returned by Services. ee maps these to HTTP status; anything
// else is a 500.
var (
	// ErrNotFound: the target role/department does not exist.
	ErrNotFound = errors.New("adminwrite: not found")
	// ErrImmutable: the target is a built-in (seed-owned) object and cannot be
	// modified through the API.
	ErrImmutable = errors.New("adminwrite: built-in object is immutable")
	// ErrInvalid: the request is malformed against a guard. Maps to 400.
	ErrInvalid = errors.New("adminwrite: invalid request")
	// ErrForbidden: the request is well-formed but refused by a security guard
	// (e.g. granting the admin-console capability through a role permission,
	// which would turn this route into an admin-promotion path). Maps to 403.
	ErrForbidden = errors.New("adminwrite: forbidden")

	// Stable sub-errors retain the reason without exposing wrapped free-form
	// diagnostics as a client contract. They continue to match their parent
	// sentinel through errors.Is.
	ErrNoUpdatableFields      = fmt.Errorf("%w: no updatable fields provided", ErrInvalid)
	ErrWildcardGrant          = fmt.Errorf("%w: wildcard grant", ErrInvalid)
	ErrTypeWideActionExecute  = fmt.Errorf("%w: type-wide action execute", ErrInvalid)
	ErrAdminConsolePermission = fmt.Errorf("%w: admin console permission", ErrForbidden)
)

// RoleSpec creates a custom role. Source is always forced to "custom" by the
// implementation; the API cannot mint system or business roles.
type RoleSpec struct {
	ID          string // optional; generated when empty
	Name        string
	Description string
}

// RolePatch updates a custom role. Nil fields are left unchanged; a patch with
// no fields set is ErrInvalid.
type RolePatch struct {
	Name        *string
	Description *string
}

// Services is the narrow set of guarded core operations the ee write handlers
// call. Core implements it over internal/authz + internal/database; the guards
// (source=custom, built-in immutable, wildcard/admin-console refusal) live in
// that implementation, not in ee.
type Services interface {
	// RequirePermission returns core's RBAC middleware for a resource/op, so ee
	// write routes keep the same per-caller permission checks community reads
	// use. This is the RBAC layer. The licence layer is the socket's (Gate) and
	// the caller is responsible for ordering it first — an unlicensed request
	// must not reach any authorization check, including authentication, or the
	// 401/403 discloses that the endpoint exists in this binary.
	RequirePermission(resourceType, op string) gin.HandlerFunc

	// CreateRole creates a custom role and returns its id.
	CreateRole(ctx context.Context, spec RoleSpec) (id string, err error)
	// UpdateRole renames/re-describes a custom role. ErrImmutable for built-ins.
	UpdateRole(ctx context.Context, id string, patch RolePatch) error
	// DeleteRole deletes a custom role and purges its bindings and grants.
	DeleteRole(ctx context.Context, id string) error
	// GrantRolePermission grants a custom role an op over a resource pattern.
	// Refuses wildcard types/ops, type-wide action_type/execute, and the
	// admin-console capability.
	GrantRolePermission(ctx context.Context, roleID, resourceType, resourceID, op string) error
	// RevokeRolePermission revokes one operation from a custom role.
	//
	// An implementation that also satisfies RoleSetRevoker gets the whole set
	// instead, which is what an accurate multi-operation revoke needs — see that
	// interface.
	RevokeRolePermission(ctx context.Context, roleID, resourceType, resourceID, op string) error
}

// PermissionPoint is one (resource type, operation) RBAC permission point, e.g.
// {"admin-role", "permissions"}.
type PermissionPoint struct {
	ResourceType string
	Op           string
}

// AnyPermissionRequirer is an OPTIONAL extension of Services: a middleware that
// passes when the caller holds ANY of several permission points. It exists for
// routes whose guarding point was renamed — the canonical point plus the point
// that used to guard the same operation, so custom roles built on the old point
// survive the upgrade.
//
// It is a separate interface rather than a Services method so that adding it
// does not break existing Services implementations (notably ee's test doubles).
// requireAnyPermission falls back to the canonical point alone when the
// implementation does not provide it.
type AnyPermissionRequirer interface {
	RequireAnyPermission(points ...PermissionPoint) gin.HandlerFunc
}

// requireAnyPermission returns the any-of middleware when svc supports it, or
// the single-point middleware for points[0] (the canonical point) when it does
// not. Callers must list the canonical point first.
func requireAnyPermission(svc Services, points ...PermissionPoint) gin.HandlerFunc {
	if len(points) == 0 {
		panic("adminwrite: requireAnyPermission needs at least one point")
	}
	if r, ok := svc.(AnyPermissionRequirer); ok {
		return r.RequireAnyPermission(points...)
	}
	return svc.RequirePermission(points[0].ResourceType, points[0].Op)
}

// RoleSetRevoker is an OPTIONAL extension of Services: revoke a SET of
// operations from a role over one resource pattern, in a single call.
//
// The set is what makes the answer correct. An operation another operation
// implies cannot be dropped while that implying operation is kept (#1121), and
// deciding that one operation at a time — against the live state, where an
// operation earlier in the caller's list is still held — makes the outcome
// depend on the order: revoking ["view_detail", "resource_manage"] keeps
// view_detail, while ["resource_manage", "view_detail"] drops both. Judged
// against the remainder, computed once before anything is removed, both orders
// drop both.
//
// Like AnyPermissionRequirer, this is a separate interface rather than a
// Services method so that adding it does not break existing Services
// implementations (notably ee's test doubles). revokeRolePermissions falls back
// to the per-operation method when the implementation does not provide it,
// which restores the order dependence for that implementation only — core's
// does provide it.
type RoleSetRevoker interface {
	RevokeRolePermissions(ctx context.Context, roleID, resourceType, resourceID string, ops []string) error
}

// Mounter registers the rbac_basic write routes onto g using svc. The ee build
// provides one; the community build leaves it nil, so the routes never exist.
type Mounter func(g *gin.RouterGroup, svc Services)

var (
	mounter Mounter
	// minEdition is the lowest tier allowed to reach the write routes. It is
	// declared once at registration and read on every request; the zero value
	// only ever survives in a community build, where nothing is mounted.
	minEdition licverify.Edition
)

// RegisterMounter installs the write-route mounter and declares the lowest tier
// that may use it.
//
// The socket owns the refusal, not the caller. That is the whole point of the
// signature: an entitlement gap has to be indistinguishable from the route not
// existing (ee-design.md §4.4/§4.5: no licence looks absent; insufficient
// permission is explicitly denied),
// and leaving that decision to each caller means each caller can get it wrong
// independently. One of them did, and shipped a 403 carrying the feature name.
//
// Registration is unconditional — do not check the licence first (ee-design.md
// §5.4). A mounter installed only when a certificate is already present cannot
// be switched on by one imported afterwards, because the registry is frozen by
// then; the customer would have to restart the service on their own site to fix
// a licensing mistake. What may pass is decided per request, in Gate.
//
// The ee assembly calls this once, before Freeze. A second call, a call after
// the socket is frozen, and a zero min are all assembly bugs and all panic —
// the last one because a paid capability registered without a tier would be
// registered as free, which is the failure this whole mechanism exists to
// prevent (entitlement.MarkAssembled makes the same check).
func RegisterMounter(min licverify.Edition, m Mounter) {
	if m == nil {
		panic("adminwrite: RegisterMounter(nil)")
	}
	if frozen {
		panic("adminwrite: RegisterMounter after Freeze — write routes must be assembled before the server runs")
	}
	if mounter != nil {
		panic("adminwrite: mounter already registered")
	}
	entitlement.MustBeAssembling("adminwrite")
	// Records the capability in the process-wide assembly table, which is what
	// the capabilities endpoint reports as installed. Panics on a zero or
	// unknown min, so that check is not duplicated here.
	entitlement.MarkAssembled(Capability, min)
	mounter = m
	minEdition = min
}

// Gate hides the routes when the licence in force is below the tier declared at
// registration.
//
// 404 with no body, which is byte-for-byte what an unmounted route answers:
// gin has no NoRoute handler here, so a community build replies "404 page not
// found" as text/plain. Anything richer — a JSON body, an error code, the
// capability name — is a fingerprint that tells a prober the paid surface exists
// in this binary, which is exactly what the two-binary split is meant to deny.
//
// The tier is read per request, so a certificate imported after boot takes
// effect on the next call and a lapsed one goes dark just as fast. Neither
// needs a restart.
//
// The reason still gets recorded, just not to the caller: the log is where an
// operator finds out this was an entitlement gap rather than a missing route
// (ee-design.md §7.5 requires the two to be distinguishable server-side).
func Gate() gin.HandlerFunc {
	min := minEdition
	if min == "" {
		// Community build: nothing registered, so Mount will not add any route
		// under this group and there is nothing to hide. Never nil — a caller
		// that wires Gate() unconditionally must not have to branch on it.
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if entitlement.AtLeast(min) {
			c.Next()
			return
		}
		// The reason goes to the log, never to the caller. §7.5 requires an
		// operator to be able to tell an entitlement gap from a missing route;
		// the caller must not be able to tell them apart at all.
		slog.Info("adminwrite: route hidden, licence below the required tier",
			"capability", Capability, "min_edition", string(min),
			"edition", string(entitlement.Current()),
			"method", c.Request.Method, "path", c.Request.URL.Path)

		// gin's own answer for an unmounted route, byte for byte: status 404,
		// Content-Type "text/plain" (gin.MIMEPlain), body "404 page not found"
		// with no trailing newline and no other header.
		//
		// Reproduced here rather than delegated, because there is nothing to
		// delegate to: gin only runs its not-found handler when no route
		// matched, and this route did match. Both obvious shortcuts are wrong
		// and were tried — writing the header by hand yields
		// "text/plain; charset=utf-8", and net/http's NotFound adds a trailing
		// newline plus X-Content-Type-Options: nosniff. Each is a fingerprint
		// even though the status and body look right, which is why the test
		// compares the whole response rather than the status code.
		c.Abort()
		c.Data(http.StatusNotFound, gin.MIMEPlain, []byte("404 page not found"))
	}
}

var frozen bool

// Freeze closes the socket. Call once, after assembly. The router's Mount is
// the natural freeze point.
func Freeze() { frozen = true }

// Mount registers the write routes if an ee mounter is present, and freezes the
// socket. In a community build no mounter was registered, so this is a no-op
// and the write routes stay absent (404). Returns whether routes were mounted,
// for the startup log.
// Mount does NOT apply Gate() itself. gin appends group middleware to the
// parent chain, so a gate added here would run after whatever the caller
// already put on the group — in bkn-safe that is RequireAdmin, and an
// unauthenticated probe would then get 401 from an enterprise binary where the
// community one answers 404. Ordering is the caller's to get right, and
// router.go puts Gate() first; TestGateRunsBeforeAuthentication is what checks
// that it stays that way.
func Mount(g *gin.RouterGroup, svc Services) bool {
	frozen = true
	if mounter == nil {
		return false
	}
	mounter(g, svc)
	return true
}

// Mounted reports whether an ee write-route mounter is present. For tests and
// startup logging.
func Mounted() bool { return mounter != nil }

// resetForTest clears the socket. Tests only; guarded by testing.Testing in
// the exported wrapper.
func resetForTest() {
	mounter = nil
	minEdition = ""
	frozen = false
}
