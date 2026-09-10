// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package permobject is the mode ② socket for perm_object_level — object-level
// authorization and advanced role control, implemented in the private ee code
// line (openbkn-ee) and absent from community binaries.
//
// The socket contributes one structured local opinion to core's evaluator. It
// neither replaces core nor folds over a final bool: core must merge all local
// sources before parent fallback, otherwise an Enterprise allow could revive a
// Professional explicit deny.
//
// Core remains authoritative for Community and Professional rules; this socket
// contributes the current Enterprise/Industry object-rule source, including
// time-bounded rules stored by openbkn-ee.
package permobject

import (
	"context"
	"sync/atomic"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// Capability is the name this socket registers itself under, and the string the
// capabilities endpoint reports. It is spelled the same as the licence feature
// key it grew out of, but nothing reads it from a certificate any more: the
// tier is what decides (ee-design.md §3.1), and this is a display name.
const Capability = "perm_object_level"

// Decision is the ee layer's opinion for one local specificity.
type Decision uint8

const (
	// Abstain: the ee layer has no opinion at this specificity.
	// Community builds always abstain because nothing is plugged in.
	Abstain Decision = iota
	// Allow: at least one current Enterprise source allows the operation.
	Allow
	// Deny: at least one current Enterprise source denies the operation.
	Deny
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return "abstain"
	}
}

// CoreDecision and CoreBasis are the stable structured vocabulary Core passes
// to an Enterprise provider. They mirror the public authorization contract but
// remain declared at the socket boundary so openbkn-ee never imports Core's
// internal packages.
type CoreDecision string

const (
	CoreAllow CoreDecision = "allow"
	CoreDeny  CoreDecision = "deny"
	CoreNone  CoreDecision = "none"
)

type CoreBasis string

const (
	CoreBasisDirect   CoreBasis = "direct"
	CoreBasisBundle   CoreBasis = "bundle"
	CoreBasisWildcard CoreBasis = "wildcard"
	CoreBasisNone     CoreBasis = "none"
)

// Request is one authorization question, already resolved to its subject and
// object by core.
type Request struct {
	AccessorID string
	// AccessorIDs contains the concrete accessor plus all transitive Core roles
	// and the public subject. Providers use it to merge EE rules written to the
	// same subject vocabulary; AccessorID remains the original caller.
	AccessorIDs  []string
	ResourceType string
	ResourceID   string
	Op           string
	// CoreDecision and CoreBasis are Core's structured local result before the
	// EE source is merged. They are diagnostic context only: providers return
	// their own opinion and must not apply parent fallback or produce a final
	// authorization result.
	CoreDecision CoreDecision
	CoreBasis    CoreBasis
}

// LocalOpinion separates exact-instance rules from type-wide wildcard rules.
// Core needs both because wildcard deny is terminal while wildcard allow is
// deliberately postponed until after parent fallback.
type LocalOpinion struct {
	Direct   Decision
	Wildcard Decision
}

// Authorizer is what the ee code line implements.
//
// Decide must be safe for concurrent use and must not block on I/O that can
// hang — it sits on the authorization hot path.
type Authorizer interface {
	Decide(ctx context.Context, req Request) (LocalOpinion, error)
}

// impl holds the registered implementation. atomic.Value keeps the read path
// lock-free; it is written once during assembly and only read afterwards.
var impl atomic.Value // Authorizer

// minEdition is the lowest tier allowed to reach the ee implementation. It is
// declared once at registration and read on every decision.
var minEdition licverify.Edition

// Register plugs the ee implementation into the socket and declares the lowest
// tier that may use it. It is called from the enterprise assembly entry point
// (cmd/*-ee).
//
// Registration is UNCONDITIONAL — do not check the licence first (ee-design.md
// §5.4). An entry point that registers only when a certificate is already
// present cannot be switched on by a certificate installed afterwards: the
// registry is frozen before that certificate arrives, so the only remedy left
// is restarting the service on the customer's site to fix a licensing mistake.
// Available() and Decide() re-read the tier on every call, which is where it
// belongs.
//
// It panics on a nil implementation, a second registration, a registration
// after the assembly registry is frozen, and a zero min. All four are assembly
// bugs that must be loud at startup; the last one because a paid capability
// registered without a tier would be registered as free.
//
// The second-registration guard is this package's own. entitlement.MarkAssembled
// is idempotent by name — one capability routinely has several entry points —
// so it does not catch a second Authorizer, and this socket holds exactly one:
// the later Store would silently discard the earlier implementation, leaving no
// trace anywhere. This is the layer that produces Deny over casbin, so which
// implementation decides is not something to lose quietly.
func Register(min licverify.Edition, a Authorizer) {
	if a == nil {
		panic("permobject: Register(nil)")
	}
	if load() != nil {
		panic("permobject: authorizer already registered — a second one would silently replace the first")
	}
	entitlement.MustBeAssembling("permobject")
	// Records the capability in the process-wide assembly table, which is what
	// the capabilities endpoint reports as installed. Panics on a zero or
	// unknown min, so that check is not duplicated here.
	entitlement.MarkAssembled(Capability, min)
	minEdition = min
	impl.Store(a)
}

// Registered reports whether an implementation is plugged in. It says nothing
// about the license — use Available for that.
func Registered() bool { return load() != nil }

// Available reports whether the capability is usable right now: plugged in and
// the licence in force at or above the declared tier. The tier half is re-read
// every call, so an expiry or a hot-reloaded downgrade takes effect without a
// restart.
func Available() bool {
	return load() != nil && entitlement.AtLeast(minEdition)
}

// Decide asks the ee layer for a second opinion. Community builds, clusters
// below the required tier, and clusters whose license lapsed after startup all
// get an empty opinion with a nil error, which leaves core's result untouched — the
// community authorization behaviour is the fallback, exactly as the downgrade
// path requires.
//
// Note this is an abstaining opinion, not the 404 the HTTP sockets answer with (ee-design.md
// §4.4). Nothing here is a request entry point: it is one layer inside a
// decision, and falling back to core's result is invisible from outside, so
// there is no paid surface to hide.
//
// An error from the ee layer returns Deny. The socket only ever runs in an
// enterprise build where the ee layer is the authority on restrictions; a
// transient failure that silently reverted to core's more permissive verdict
// would hand out access the enterprise policy revoked. Callers must surface
// the error rather than treat the denial as a plain policy outcome.
func Decide(ctx context.Context, req Request) (LocalOpinion, error) {
	a := load()
	if a == nil || !entitlement.AtLeast(minEdition) {
		return LocalOpinion{}, nil
	}
	d, err := a.Decide(ctx, req)
	if err != nil {
		return LocalOpinion{Direct: Deny, Wildcard: Deny}, err
	}
	return d, nil
}

func load() Authorizer {
	v := impl.Load()
	if v == nil {
		return nil
	}
	a, _ := v.(Authorizer)
	return a
}

// reset clears the socket. Tests only.
func reset() {
	impl = atomic.Value{}
	minEdition = ""
}
