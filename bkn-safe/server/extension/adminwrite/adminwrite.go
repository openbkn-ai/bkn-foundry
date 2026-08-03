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
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension"
)

// Sentinel errors returned by Services. ee maps these to HTTP status; anything
// else is a 500.
var (
	// ErrNotFound: the target role/department does not exist.
	ErrNotFound = errors.New("adminwrite: not found")
	// ErrImmutable: the target is a built-in (seed-owned) object and cannot be
	// modified through the API.
	ErrImmutable = errors.New("adminwrite: built-in object is immutable")
	// ErrInvalid: the request is malformed against a guard (wildcard grant,
	// empty patch). Maps to 400. The wrapped message is safe to surface.
	ErrInvalid = errors.New("adminwrite: invalid request")
	// ErrForbidden: the request is well-formed but refused by a security guard
	// (e.g. granting the admin-console capability through a role permission,
	// which would turn this route into an admin-promotion path). Maps to 403.
	ErrForbidden = errors.New("adminwrite: forbidden")
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
	// use. This is the RBAC layer; the licence layer is the socket's (see
// requireFeature), and it runs first — an unlicensed request must never reach
// an authorization check, or the permission error would disclose the endpoint.
	RequirePermission(resourceType, op string) gin.HandlerFunc

	// CreateRole creates a custom role and returns its id.
	CreateRole(ctx context.Context, spec RoleSpec) (id string, err error)
	// UpdateRole renames/re-describes a custom role. ErrImmutable for built-ins.
	UpdateRole(ctx context.Context, id string, patch RolePatch) error
	// DeleteRole deletes a custom role and purges its bindings and grants.
	DeleteRole(ctx context.Context, id string) error
	// GrantRolePermission grants a custom role an op over a resource pattern.
	// Refuses wildcard types/ops and the admin-console capability (ErrInvalid).
	GrantRolePermission(ctx context.Context, roleID, resourceType, resourceID, op string) error
	// RevokeRolePermission revokes a custom role's op over a resource pattern.
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

// Mounter registers the rbac_basic write routes onto g using svc. The ee build
// provides one; the community build leaves it nil, so the routes never exist.
type Mounter func(g *gin.RouterGroup, svc Services)

var (
	mounter Mounter
	// gatedOn is the feature the socket itself enforces per request. Empty
	// means the caller used the legacy RegisterMounter and is gating inside
	// its own mounter.
	gatedOn extension.Feature
)

// RegisterMounterGated installs the write-route mounter and tells the socket
// which feature to enforce. This is the form to use.
//
// The socket owns the refusal, not the caller. That is the whole point of the
// signature: an entitlement gap has to be indistinguishable from the route not
// existing (ee-design.md §4.4/§4.5 — 缺证书 → 装作没有；缺权限 → 明确拒绝),
// and leaving that decision to each caller means each caller can get it wrong
// independently. One of them did, and shipped a 403 carrying the feature name.
//
// The ee assembly calls this once, before Freeze. A second call, or a call
// after the socket is frozen, panics — both are assembly bugs.
func RegisterMounterGated(f extension.Feature, m Mounter) {
	if f == "" {
		panic("adminwrite: RegisterMounterGated with an empty feature")
	}
	registerMounter(m)
	gatedOn = f
}

// RegisterMounter installs a mounter that gates itself.
//
// Deprecated: use RegisterMounterGated so the socket enforces the tier and
// produces the refusal. This form is kept only so the enterprise code line can
// switch over in its own commit rather than breaking the moment core changes;
// it will be removed once it has no callers.
func RegisterMounter(m Mounter) { registerMounter(m) }

func registerMounter(m Mounter) {
	if m == nil {
		panic("adminwrite: RegisterMounter(nil)")
	}
	if frozen {
		panic("adminwrite: RegisterMounter after Freeze — write routes must be assembled before the server runs")
	}
	if mounter != nil {
		panic("adminwrite: mounter already registered")
	}
	mounter = m
}

// requireFeature hides the routes when the licence does not carry the feature.
//
// 404 with no body, which is byte-for-byte what an unmounted route answers:
// gin has no NoRoute handler here, so a community build replies "404 page not
// found" as text/plain. Anything richer — a JSON body, an error code, the
// feature name — is a fingerprint that tells a prober the paid surface exists
// in this binary, which is exactly what the two-binary split is meant to deny.
//
// The reason still gets recorded, just not to the caller: the log is where an
// operator finds out this was an entitlement gap rather than a missing route
// (ee-design.md §7.5 requires the two to be distinguishable server-side).
func requireFeature(f extension.Feature) gin.HandlerFunc {
	return func(c *gin.Context) {
		if extension.Enabled(f) {
			c.Next()
			return
		}
		slog.Info("adminwrite: route hidden, feature not licensed",
			"feature", string(f), "method", c.Request.Method, "path", c.Request.URL.Path)
		c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("404 page not found"))
		c.Abort()
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
func Mount(g *gin.RouterGroup, svc Services) bool {
	frozen = true
	if mounter == nil {
		return false
	}
	if gatedOn != "" {
		g = g.Group("", requireFeature(gatedOn))
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
	frozen = false
}
