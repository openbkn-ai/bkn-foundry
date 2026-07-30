// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package entitlement answers one question for every service in this
// repository: what has this deployment paid for, and is a given paid capability
// allowed right now.
//
// It is deliberately not called "authorization". Authorization is about who the
// caller is and what that user may do — casbin, roles, permissions, all of it in
// bkn-safe. Entitlement is about the deployment's licence, which is the same
// answer for every caller. Confusing the two is how a paid feature ends up
// gated per-user, or a permission check ends up disabled by an expired licence.
//
// # What lives here and what does not
//
//	licverify   the certificate: signature, validity, fingerprint binding,
//	            the Edition vocabulary and its ordering
//	here        the running process's view of it: fetch, snapshot, grace,
//	            plus the registry of what enterprise code plugged itself in
//	services    the sockets — where a capability attaches (MCP tools, routes)
//	openbkn-ee  the paid implementations themselves
//
// The split matters when deciding where a change goes: anything about the
// certificate itself belongs upstream in licverify; anything that would look
// almost identical in nine services belongs here.
//
// # Only the edition is gated on
//
// A paid capability declares the lowest tier that may use it and the check is
// always AtLeast. The certificate's features[] is carried and displayed but
// never decides anything — see Features.
//
// # The judgement is re-read, never cached
//
// Every call goes through the installed Gate to the current snapshot. A licence
// that expires, is replaced, or arrives late has to take effect without
// restarting anything, which a cached bool would prevent. The cost is a pointer
// read and an integer compare, not a signature verification — verification and
// fetching happen in the background.
//
// Design: bkn-docs docs/shared/licensing/ee-design.md.
package entitlement

import (
	"sync"

	"github.com/openbkn-ai/licverify"
)

// Snapshot is one point-in-time reading of the licence. Everything a caller can
// ask is derived from it, so a Gate only has to produce this.
type Snapshot struct {
	// Licensed reports whether a valid licence is in force. False covers all
	// of "never installed", "expired past grace" and "failed verification" —
	// the product behaves identically in each, which is the point.
	Licensed bool
	// Edition is the tier in force. It is EditionCommunity whenever Licensed
	// is false: an enterprise binary without a licence behaves as community,
	// and there is deliberately no separate "unlicensed" tier.
	Edition licverify.Edition
	// State is the certificate's own view (valid, grace, expired, trial…).
	// It exists for operator surfaces and logs. Never gate on it — Licensed
	// and Edition already carry the decision, and duplicating it invites two
	// call sites to disagree.
	State licverify.State
	// Features is what the licence carries, for display and audit only.
	Features []string
}

// Gate produces the current snapshot. Implementations must read live state on
// every call rather than memoising a decision.
type Gate interface {
	Snapshot() Snapshot
}

// GateFunc adapts a plain function to Gate.
type GateFunc func() Snapshot

func (f GateFunc) Snapshot() Snapshot { return f() }

// deniedGate is the zero value: community, unlicensed. It is what a process
// uses before SetGate runs, so a wiring mistake fails towards withholding paid
// capability rather than giving it away. Community capability is unaffected —
// it is never gated at all.
type deniedGate struct{}

func (deniedGate) Snapshot() Snapshot {
	return Snapshot{Edition: licverify.EditionCommunity}
}

var (
	mu   sync.RWMutex
	gate Gate = deniedGate{}
)

// SetGate installs the licence gate. Call it once during startup, before the
// registry is frozen.
//
// Swapping the gate after Freeze panics: from that moment some call sites have
// already judged against the old one, and a process where half the capabilities
// answer to a different licence than the other half is not debuggable.
func SetGate(g Gate) {
	mu.Lock()
	defer mu.Unlock()
	if frozenLocked() {
		panic("entitlement: SetGate after Freeze — the licence gate must be installed during assembly")
	}
	if g == nil {
		panic("entitlement: SetGate(nil)")
	}
	gate = g
}

// Current returns the tier in force. Safe on the hot path.
func Current() licverify.Edition { return current().Edition }

// AtLeast reports whether the licence in force is min or a tier above it. This
// is the only authorisation question a paid capability should ask.
func AtLeast(min licverify.Edition) bool { return current().Edition.AtLeast(min) }

// Licensed reports whether a valid licence is in force. It is for operator
// surfaces: it distinguishes "community deployment" from "enterprise binary
// whose licence has not taken effect", which Edition alone cannot.
func Licensed() bool { return current().Licensed }

// State returns the certificate's own state, for operator surfaces and logs.
func State() licverify.State { return current().State }

// Features returns the feature keys the licence carries.
//
// It is for display and audit reconciliation ONLY. No code path may branch on
// it — not here, not in a service, and above all not in a frontend, where the
// check has no force at all. Gating is by edition; a features[]-driven check is
// fine-grained licensing reintroduced through the back door, in the one place
// it cannot be enforced.
//
// To show or hide by capability, ask the capabilities surface, which reports
// what is actually usable in this build rather than letting the caller infer it.
func Features() []string {
	f := current().Features
	// Copy: the caller must not be able to mutate the snapshot's slice.
	return append([]string(nil), f...)
}

// Snap returns the whole snapshot, for operator endpoints that report several
// of these at once and want them mutually consistent.
func Snap() Snapshot { return current() }

func current() Snapshot {
	mu.RLock()
	g := gate
	mu.RUnlock()
	return g.Snapshot()
}
