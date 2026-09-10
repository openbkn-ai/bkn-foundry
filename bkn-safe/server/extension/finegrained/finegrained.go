// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package finegrained is the assembly marker for the shared
// perm_fine_grained object-grant surface.
//
// The handlers live in Core because Community and paid editions share one
// route. An Enterprise assembly registers this marker unconditionally; each
// request then combines the compiled-in marker with the live entitlement.
// Community builds never register it, while an unlicensed or downgraded EE
// build keeps it assembled but unavailable.
package finegrained

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

const Capability = "perm_fine_grained"

var (
	mu         sync.RWMutex
	minEdition licverify.Edition
)

// Register declares that this binary contains the Professional object-grant
// request shapes. It must run during EE assembly, before entitlement.Freeze.
func Register(min licverify.Edition) {
	mu.Lock()
	defer mu.Unlock()
	if minEdition != "" {
		panic("finegrained: capability already registered")
	}
	entitlement.MustBeAssembling("finegrained")
	entitlement.MarkAssembled(Capability, min)
	minEdition = min
}

// Assembled reports only whether the paid request shapes are compiled into the
// current binary. It deliberately says nothing about the live licence.
func Assembled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return minEdition != ""
}

// Available reports whether the paid request shapes may be used now.
func Available() bool {
	mu.RLock()
	min := minEdition
	mu.RUnlock()
	return min != "" && entitlement.AtLeast(min)
}

func reset() {
	mu.Lock()
	defer mu.Unlock()
	minEdition = ""
}
