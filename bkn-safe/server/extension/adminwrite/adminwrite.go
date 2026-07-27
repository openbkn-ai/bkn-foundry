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
// write routes in its Setup, behind RequireFeature("rbac_basic").
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

	"github.com/gin-gonic/gin"
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
	// use. This is the RBAC layer; the license layer (RequireFeature) is ee's.
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

var mounter Mounter

// RegisterMounter installs the write-route mounter. The ee assembly calls it
// once, before Freeze. A second call, or a call after the socket is frozen,
// panics — both are assembly bugs (see extension.Claim for the same rule on the
// capability sockets).
//
// It is intentionally not license-checked here: ee gates each route with
// RequireFeature inside the mounter, so a professional-only cluster still
// mounts the routes and lets RequireFeature refuse the enterprise ones. The
// community binary simply never calls this.
func RegisterMounter(m Mounter) {
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
