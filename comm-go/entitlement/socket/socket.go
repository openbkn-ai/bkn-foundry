// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package socket holds the part every extension socket repeats: a keyed
// registry that only accepts entries during assembly, records which paid
// capability each entry belongs to, and hands them back in a stable order.
//
// # What belongs here and what does not
//
// Sockets differ in almost everything that matters to their users. The MCP tool
// socket keys by tool name, carries JSON schemas, and hides an unlicensed tool
// by answering the way mcp-go answers one it has never heard of. An HTTP route
// socket keys by method and path, has no schema to speak of, answers 404, and —
// in bkn-backend, where the same route is mounted under both /api/bkn-backend
// and /api/ontology-manager — cannot even decide by itself where its entries
// get mounted.
//
// So this package deliberately holds no opinion about any of that. It does not
// know what an entry is, how it is served, or what refusing looks like. What it
// does know are the four invariants that are the same everywhere, and that are
// easy to forget when writing socket number three:
//
//  1. Registration happens during assembly, never after the server starts.
//  2. Every entry declares the capability it belongs to and the lowest tier
//     that may use it.
//  3. Two entries must not claim the same key — the second would silently
//     replace the first.
//  4. Listing is stable, because tool catalogues and route tables are compared
//     across restarts.
//
// # Registration is unconditional
//
// Nothing here consults the licence. A socket registers everything the binary
// has, and whether an entry may serve is decided per call from the licence in
// force at that moment (entitlement.AtLeast). Registering only what the current
// certificate allows would mean a process that booted without one could never
// pick up a certificate installed later — it would have to be restarted, on a
// customer's site, to fix a licensing mistake.
package socket

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

// Registry stores one socket's entries. T is whatever that socket's entry looks
// like; this package never inspects it.
//
// The zero value is not usable — use New, which names the socket for the panic
// messages. "mcptool: tool \"search_schema\" registered twice" tells whoever
// hits it where to look; "socket: duplicate key" does not.
type Registry[T any] struct {
	kind string

	mu    sync.RWMutex
	items map[string]T
}

// New creates a registry for a socket. kind is the socket's name, used in panic
// messages ("mcptool", "httproute").
func New[T any](kind string) *Registry[T] {
	return &Registry[T]{kind: kind, items: map[string]T{}}
}

// Add registers an entry under key, on behalf of capability, requiring at least
// min.
//
// It panics on every condition that would otherwise fail silently at runtime:
// registering after the assembly window closed, a duplicate key, a missing
// capability name, or a MinEdition that is zero or unrecognised — those last two
// being how a paid entry accidentally registers as free.
func (r *Registry[T]) Add(key, capability string, min licverify.Edition, item T) {
	if key == "" {
		panic(r.kind + ": registration with an empty key")
	}
	if capability == "" {
		panic(fmt.Sprintf("%s: %q registered without a capability name — several entries usually share one, and the assembly registry records it by name", r.kind, key))
	}

	// Outside the assembly window nothing may register: the catalogue this
	// socket feeds is materialised once, when handlers are built.
	entitlement.MustBeAssembling(r.kind + ":" + key)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.items[key]; dup {
		panic(fmt.Sprintf("%s: %q registered twice — the second registration would silently replace the first", r.kind, key))
	}
	// MarkAssembled rejects a zero or misspelt MinEdition, so those checks live
	// in one place for every socket rather than being repeated (and eventually
	// forgotten). An unrecognised tier is the dangerous one: it ranks with
	// community, so AtLeast is true for every licence.
	entitlement.MarkAssembled(capability, min)

	r.items[key] = item
}

// Get returns the entry registered under key.
func (r *Registry[T]) Get(key string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[key]
	return item, ok
}

// All returns every entry, ordered by key. Ordering is by key rather than by
// registration order so that the answer does not depend on which init ran
// first — catalogues get diffed across restarts and across replicas.
func (r *Registry[T]) All() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.items))
	for k := range r.items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]T, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.items[k])
	}
	return out
}

// Len reports how many entries are registered. A community binary has none:
// the code that would register them is not in the build.
func (r *Registry[T]) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// ResetForTest empties this registry — and only this registry.
//
// It deliberately does not touch entitlement's assembly table, and
// entitlement.ResetForTest does not touch this one. A socket commonly owns
// several registries (mcptool has one for tools and one for decorators), so
// coupling them would mean resetting the first wipes the capability claims that
// the second's entries still depend on — trading one kind of skew for another.
//
// So a socket's own reset helper has to clear both halves. mcptool.ResetForTest
// is the reference shape: reset every registry it owns, then call
// entitlement.SetGateForTest, which resets the assembly table on its way in.
//
// Getting this wrong is not a production risk — both resets refuse to run
// outside a test binary — but it makes a test lie: "a community build assembled
// nothing" passes while the socket registry is still full.
//
// Guarded because it is linked into production binaries: clearing a live
// registry is exactly what Add's assembly-window panic exists to prevent.
func (r *Registry[T]) ResetForTest() {
	if !testing.Testing() {
		panic(r.kind + ": ResetForTest is test-only and must never run in a production binary")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = map[string]T{}
}
