// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package projecthooks

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

func panics(f func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	f()
	return false
}

// Hooks are registered once, while assembling: the built-in knowledge base and an edition's can
// never both be.
func TestRegisterOnceWhileAssembling(t *testing.T) {
	defer SetForTest(Hooks{})()
	current, registered = Hooks{}, false
	entitlement.ResetForTest()
	defer entitlement.ResetForTest()
	Register(Hooks{})
	if panics(func() { Register(Hooks{}) }) == false {
		t.Fatal("a second registration was accepted")
	}
	current, registered = Hooks{}, false
	entitlement.Freeze()
	if panics(func() { Register(Hooks{}) }) == false {
		t.Fatal("a registration after Freeze was accepted")
	}
}
