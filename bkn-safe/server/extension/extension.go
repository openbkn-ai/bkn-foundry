// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package extension is the socket core exposes to the enterprise code line.
// It is deliberately outside internal/ — Go's internal rule would otherwise
// make it unimportable from openbkn-ee, and the whole open-core model rests on
// ee being able to plug in from another repository.
//
// Two mechanisms live here, and they are not the same thing:
//
//   - Gate (mode ①): the feature is implemented in this open repository and
//     the license decides whether it is on. rbac_basic is the sample.
//   - Socket (mode ②): the feature is implemented in the private ee repository
//     and registers itself into a typed interface here at enterprise startup.
//     Community binaries physically lack the code; the socket stays nil and
//     the call site degrades. perm_object_level is the first one.
//
// Dependency direction is always ee -> core. Core knows nothing about ee; a
// single `import ".../openbkn-ee/..."` anywhere in core collapses the model,
// so CI lints for it.
//
// Design: license-server docs/design/open-core-gating.md §2.5.
package extension

import (
	"fmt"
	"sort"
	"sync"
)

// Feature is a license feature key. The authoritative dictionary is
// license-server docs/design/license-service.md §1.5 — that file is the single
// source, this block mirrors it. A key that has been signed into a customer
// license is a permanent API: removing one takes two major versions of
// deprecation, because a silent removal makes an existing customer's
// capability vanish.
type Feature string

const (
	// Professional — mode ①, implemented in this repository, license-gated.
	FeatureSourceSync         Feature = "source_sync"
	FeatureRBACBasic          Feature = "rbac_basic"
	FeatureImpactGraph        Feature = "impact_graph"
	FeatureConnectorCertified Feature = "connector_certified"

	// Enterprise — mode ②, implemented in the private ee code line.
	FeaturePermObjectLevel Feature = "perm_object_level"
	FeatureAudit           Feature = "audit"
	FeatureOpsDashboard    Feature = "ops_dashboard"
	FeatureBranding        Feature = "branding"
)

// modeTwo lists the features whose implementation lives in the private ee code
// line. Everything else is mode ① — implemented here, license-gated.
//
// The distinction matters to /capabilities: a mode ① feature is usable as soon
// as the license carries it, while a mode ② feature also needs its code to be
// present, which a community binary never has. Reporting a licensed-but-absent
// enterprise feature as usable would make the frontend offer an entry point
// that 404s.
var modeTwo = map[Feature]bool{
	FeaturePermObjectLevel: true,
	FeatureAudit:           true,
	FeatureOpsDashboard:    true,
	FeatureBranding:        true,
}

// NeedsExtension reports whether the feature is implemented in the ee code line
// (mode ②) rather than in this repository (mode ①).
func (f Feature) NeedsExtension() bool { return modeTwo[f] }

// Usable reports whether a licensed feature can actually be served by this
// binary: mode ① needs only the license, mode ② also needs its socket filled.
func Usable(f Feature) bool {
	if !Enabled(f) {
		return false
	}
	if f.NeedsExtension() {
		return registeredLocked(f)
	}
	return true
}

// Gate answers "is this licensed feature on right now". Implementations must
// read the current verified license snapshot on every call and must not cache
// a boolean — a hot-reloaded or expired license has to take effect globally
// without restarting anything (open-core-gating §2.7 rule 5).
type Gate interface {
	Enabled(f Feature) bool
}

// gateFunc adapts a plain function to Gate.
type gateFunc func(Feature) bool

func (g gateFunc) Enabled(f Feature) bool { return g(f) }

// GateFunc builds a Gate from a function.
func GateFunc(fn func(Feature) bool) Gate { return gateFunc(fn) }

// deniedGate is the zero value: nothing paid is on. It is what a binary uses
// before SetGate runs, so a wiring mistake fails closed on paid capability
// rather than handing it out for free. It never affects community capability,
// which is not gated at all.
type deniedGate struct{}

func (deniedGate) Enabled(Feature) bool { return false }

var (
	mu       sync.RWMutex
	gate     Gate = deniedGate{}
	frozen   bool
	claimed  = map[Feature]string{}
	assembly []string
)

// SetGate installs the license gate. Call once during startup, before Freeze.
// Calling it after the registry is frozen panics: a gate swapped at runtime
// would mean some call sites judge against one license and some against
// another.
func SetGate(g Gate) {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic("extension: SetGate after Freeze — the license gate must be installed during assembly")
	}
	if g == nil {
		panic("extension: SetGate(nil)")
	}
	gate = g
}

// Enabled reports whether a licensed feature is on. Safe on the hot path.
func Enabled(f Feature) bool {
	mu.RLock()
	g := gate
	mu.RUnlock()
	return g.Enabled(f)
}

// Claim records that an implementation has taken the socket for a feature.
// Typed sockets call it from their own Register; callers outside this package
// tree should not.
//
// Three deliberate panics, all of them assembly bugs that must surface at
// startup rather than silently:
//
//   - claiming a feature twice: the second implementation would overwrite the
//     first without a trace
//   - claiming after Freeze: the capability set is already published and some
//     call sites have already judged against the old one
//   - claiming an unlicensed feature: ee is expected to check its license
//     before registering, so reaching here means the check was skipped
func Claim(f Feature, impl string) {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic(fmt.Sprintf("extension: %s registered after Freeze (by %s) — extensions must be assembled before the server runs", f, impl))
	}
	if prev, dup := claimed[f]; dup {
		panic(fmt.Sprintf("extension: %s already registered by %s, cannot re-register with %s", f, prev, impl))
	}
	if !gate.Enabled(f) {
		panic(fmt.Sprintf("extension: %s registered by %s without a license for it — check the license before registering", f, impl))
	}
	claimed[f] = impl
	assembly = append(assembly, fmt.Sprintf("%s=%s", f, impl))
}

// Freeze closes the registry. Call it once, after assembly and before the
// server starts serving. Freezing twice is itself an assembly bug.
func Freeze() {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic("extension: Freeze called twice")
	}
	frozen = true
}

// Frozen reports whether the registry is closed. Call sites do not need this;
// it exists for tests and for startup logging.
func Frozen() bool {
	mu.RLock()
	defer mu.RUnlock()
	return frozen
}

// Registered lists the feature keys that have an implementation plugged in —
// the mode ② half of what /capabilities reports. Sorted for stable output.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(claimed))
	for f := range claimed {
		out = append(out, string(f))
	}
	sort.Strings(out)
	return out
}

// Assembly describes what was plugged in, for the startup log line.
func Assembly() []string {
	mu.RLock()
	defer mu.RUnlock()
	return append([]string(nil), assembly...)
}

// registeredLocked reports whether a feature has an implementation. Callers
// hold no lock; typed sockets use their own stored value instead.
func registeredLocked(f Feature) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := claimed[f]
	return ok
}

// Available reports whether a mode ② capability is usable: an implementation
// is plugged in AND the license still says yes. The second half matters
// because a license can expire after assembly — the socket stays filled but
// the capability has to go dark without a restart.
func Available(f Feature) bool {
	return registeredLocked(f) && Enabled(f)
}

// reset returns the registry to its zero state. Tests only.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	gate = deniedGate{}
	frozen = false
	claimed = map[Feature]string{}
	assembly = nil
}
