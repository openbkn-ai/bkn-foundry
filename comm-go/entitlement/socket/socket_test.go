// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package socket

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

// entry stands in for whatever a real socket stores; this package never looks
// inside it.
type entry struct{ name string }

func enterprise(t *testing.T) *Registry[entry] {
	t.Helper()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	return New[entry]("testsocket")
}

func mustPanic(t *testing.T, what string, fn func()) string {
	t.Helper()
	var got any
	func() {
		defer func() { got = recover() }()
		fn()
	}()
	if got == nil {
		t.Fatalf("%s: expected panic, got none", what)
	}
	msg, _ := got.(string)
	return msg
}

func TestAddAndGet(t *testing.T) {
	r := enterprise(t)
	r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"probe"})

	got, ok := r.Get("probe")
	if !ok || got.name != "probe" {
		t.Fatalf("Get = %+v, %v; want the registered entry", got, ok)
	}
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
}

func TestAddRecordsTheCapability(t *testing.T) {
	r := enterprise(t)
	// One capability, several entries — an extra tool plus a decorator is the
	// normal shape, and the assembly registry records it once.
	r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"probe"})
	r.Add("decorate:search_schema", "context_probe", licverify.EditionEnterprise, entry{"dec"})

	caps := entitlement.Assembled()
	if len(caps) != 1 || caps[0].Name != "context_probe" || caps[0].MinEdition != licverify.EditionEnterprise {
		t.Fatalf("Assembled() = %+v, want one enterprise capability", caps)
	}
}

func TestDuplicateKeyPanics(t *testing.T) {
	r := enterprise(t)
	r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"first"})

	msg := mustPanic(t, "duplicate key", func() {
		r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"second"})
	})
	if !strings.Contains(msg, "testsocket") || !strings.Contains(msg, "registered twice") {
		t.Fatalf("panic should name the socket and the collision, got %q", msg)
	}
}

func TestMissingCapabilityPanics(t *testing.T) {
	r := enterprise(t)
	msg := mustPanic(t, "no capability", func() {
		r.Add("probe", "", licverify.EditionEnterprise, entry{"probe"})
	})
	if !strings.Contains(msg, "capability") {
		t.Fatalf("panic should name the missing field, got %q", msg)
	}
}

func TestZeroMinEditionPanics(t *testing.T) {
	r := enterprise(t)
	// This is how a paid entry would quietly become free. The check lives in
	// entitlement.MarkAssembled so no socket has to remember it.
	msg := mustPanic(t, "zero MinEdition", func() {
		r.Add("probe", "context_probe", "", entry{"probe"})
	})
	if !strings.Contains(msg, "MinEdition") {
		t.Fatalf("panic should name the missing tier, got %q", msg)
	}
}

func TestAddAfterFreezePanics(t *testing.T) {
	r := enterprise(t)
	entitlement.Freeze()

	msg := mustPanic(t, "Add after Freeze", func() {
		r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"probe"})
	})
	if !strings.Contains(msg, "after Freeze") {
		t.Fatalf("panic should say the assembly window is closed, got %q", msg)
	}
}

func TestRegistrationIgnoresTheLicence(t *testing.T) {
	// A community-licensed process still assembles everything the binary has:
	// a certificate installed later must take effect without a restart, which
	// is impossible if an unlicensed boot registered nothing.
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	r := New[entry]("testsocket")

	r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"probe"})
	if r.Len() != 1 {
		t.Fatal("registration must not depend on the licence")
	}
	if entitlement.AtLeast(licverify.EditionEnterprise) {
		t.Fatal("…while the capability itself stays unusable")
	}
}

func TestAllIsOrderedByKeyNotByRegistration(t *testing.T) {
	r := enterprise(t)
	// Registration order follows whichever Setup ran first; the catalogue must
	// not, because it gets diffed across restarts and across replicas.
	r.Add("zeta", "cap", licverify.EditionEnterprise, entry{"zeta"})
	r.Add("alpha", "cap", licverify.EditionEnterprise, entry{"alpha"})

	got := r.All()
	if len(got) != 2 || got[0].name != "alpha" || got[1].name != "zeta" {
		t.Fatalf("All() = %+v, want key order", got)
	}
}

func TestEmptyKeyPanics(t *testing.T) {
	r := enterprise(t)
	mustPanic(t, "empty key", func() {
		r.Add("", "cap", licverify.EditionEnterprise, entry{})
	})
}

func TestResetForTestEmptiesTheRegistry(t *testing.T) {
	r := enterprise(t)
	r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"probe"})

	r.ResetForTest()
	if r.Len() != 0 {
		t.Fatal("ResetForTest should empty the registry")
	}
	// Registering again must work — a test that resets is starting over.
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	r.Add("probe", "context_probe", licverify.EditionEnterprise, entry{"probe"})
}
