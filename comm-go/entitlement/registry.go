// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"fmt"
	"sort"

	"github.com/openbkn-ai/licverify"
)

// The assembly registry records what the enterprise code line plugged into this
// process, and closes at the moment the process starts serving.
//
// It answers "what is in this binary", never "what may run right now" — the
// latter is the licence's job and is re-read on every call. A capability that
// is registered but under-licensed stays registered; it just does not serve.
//
// # Why freezing matters
//
// The tool catalogue and the route tree are materialised once, when handlers
// are built. If anything could register afterwards there would be a window in
// which one caller sees sixteen tools and the next sees seventeen, and a client
// that cached the earlier catalogue gets "not found" on a tool the server
// insists it has. Gin's route tree is worse: it is built to be written at
// startup and read afterwards, so a late registration is a data race.
//
// Freezing also keeps this from drifting into a plugin system. Lazy
// registration on first call, or capabilities that appear based on a request
// header, is a different architecture with a different failure surface.

// AssembledCap is one capability plugged into this binary.
type AssembledCap struct {
	Name string
	// MinEdition is the lowest tier that may use it. Recorded so an operator
	// surface can say "this build has audit, and it needs enterprise" — which
	// is what tells someone which certificate to install.
	MinEdition licverify.Edition
}

// The registry shares entitlement.go's mutex rather than taking its own:
// SetGate and Freeze have to be ordered against each other, and two locks would
// let a gate be installed concurrently with the freeze that is supposed to
// forbid it.
var (
	frozen    bool
	assembled = map[string]licverify.Edition{}
)

// MustBeAssembling panics if the registry is already closed. Sockets call it
// before accepting a registration, so that a late plug-in fails at the point of
// the mistake rather than silently doing nothing.
//
// It is independent of the licence: registering late is a wiring bug whether or
// not the deployment paid for anything.
func MustBeAssembling(who string) {
	mu.RLock()
	defer mu.RUnlock()
	if frozen {
		panic(fmt.Sprintf("entitlement: %s registered after Freeze — extensions must be assembled before the server starts serving", who))
	}
}

// MarkAssembled records that a capability is present in this binary, along with
// the lowest tier that may use it.
//
// It is idempotent by name, because one capability routinely has several entry
// points — an extra MCP tool plus a decorator on an existing one is the normal
// shape, and both belong to the same capability. An earlier design allowed one
// implementation per capability and panicked on the second; it broke on the
// very first real use.
//
// A zero MinEdition panics. The field is how a capability declares what it
// costs, and forgetting it would silently register a paid capability as
// community — the failure this whole package exists to prevent. There is no
// licence check here on purpose: registration is unconditional so that a
// certificate installed later takes effect without a restart. What may run is
// decided per call, not per boot.
func MarkAssembled(name string, min licverify.Edition) {
	if name == "" {
		panic("entitlement: MarkAssembled with an empty capability name")
	}
	if min == "" {
		panic(fmt.Sprintf("entitlement: capability %q registered without a MinEdition — declare the lowest tier that may use it (licverify.EditionEnterprise and friends)", name))
	}
	if !min.Known() {
		// A misspelt tier is the same class of mistake as a missing one, and it
		// fails in the worse direction: an unrecognised edition ranks with
		// community, so AtLeast(min) is true for *every* licence — the paid
		// entry silently becomes free. Both checks live here rather than in each
		// socket, because this is the one place a paid capability can register
		// itself as something it is not.
		panic(fmt.Sprintf("entitlement: capability %q registered with an unknown edition %q — must be one of community, professional, enterprise, industry", name, min))
	}
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic(fmt.Sprintf("entitlement: capability %q registered after Freeze", name))
	}
	if prev, dup := assembled[name]; dup && prev != min {
		panic(fmt.Sprintf("entitlement: capability %q registered with two different minimum editions (%s and %s)", name, prev, min))
	}
	assembled[name] = min
}

// Freeze closes the registry. The assembler calls it once, after extensions are
// in place and before handlers are built.
//
// It lives here rather than in each socket package because it closes the whole
// registry: a second socket calling its own Freeze would be the second call,
// and the second call is a bug.
func Freeze() {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic("entitlement: Freeze called twice")
	}
	frozen = true
}

// Frozen reports whether the registry is closed. Call sites do not need it; it
// exists for tests and startup logging.
func Frozen() bool {
	mu.RLock()
	defer mu.RUnlock()
	return frozen
}

// Assembled lists the capabilities in this binary, sorted by name. Empty in
// every community build — the code is not there to register anything.
func Assembled() []AssembledCap {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(assembled))
	for n := range assembled {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]AssembledCap, 0, len(names))
	for _, n := range names {
		out = append(out, AssembledCap{Name: n, MinEdition: assembled[n]})
	}
	return out
}

// frozenLocked reads the flag while the caller already holds mu.
func frozenLocked() bool { return frozen }

// reset returns the whole package to its zero state. Tests only; see
// testsupport.go.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	gate = deniedGate{}
	frozen = false
	assembled = map[string]licverify.Edition{}
}
