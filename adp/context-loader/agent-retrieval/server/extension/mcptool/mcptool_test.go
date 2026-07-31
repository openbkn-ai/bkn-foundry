// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcptool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

func sample(key string) ExtraTool {
	return ExtraTool{
		Capability: "context_probe",
		MinEdition: licverify.EditionEnterprise,
		Key:        key,
		Name:       key,
		Desc:       "sample",
		Input:      json.RawMessage(`{"type":"object"}`),
		Handle: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("enterprise"), nil
		},
	}
}

func sampleDecorator() Decorator {
	return Decorator{
		Capability: "context_probe",
		MinEdition: licverify.EditionEnterprise,
		After: func(_ context.Context, _ mcp.CallToolRequest, res *mcp.CallToolResult) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("decorated"), nil
		},
	}
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

func enterprise() entitlement.Gate { return entitlement.FixedGate(licverify.EditionEnterprise) }

func TestRegisterRejectsIncompleteTool(t *testing.T) {
	ResetForTest(enterprise())
	broken := sample("probe")
	broken.Handle = nil
	mustPanic(t, "Register without a handler", func() { Register(broken) })
}

func TestRegisterRequiresCapability(t *testing.T) {
	ResetForTest(enterprise())
	anon := sample("probe")
	anon.Capability = ""
	msg := mustPanic(t, "Register without a Capability", func() { Register(anon) })
	if !strings.Contains(msg, "Capability") {
		t.Fatalf("panic should name the missing field, got %q", msg)
	}
}

func TestRegisterRequiresMinEdition(t *testing.T) {
	ResetForTest(enterprise())
	free := sample("probe")
	free.MinEdition = ""
	// The zero value would silently register a paid tool as community. The
	// registry catches it, so the check lives in one place for every socket.
	msg := mustPanic(t, "Register without a MinEdition", func() { Register(free) })
	if !strings.Contains(msg, "MinEdition") {
		t.Fatalf("panic should name the missing field, got %q", msg)
	}
}

func TestRegisterTwicePanics(t *testing.T) {
	ResetForTest(enterprise())
	Register(sample("probe"))
	msg := mustPanic(t, "second Register", func() { Register(sample("probe")) })
	if !strings.Contains(msg, "registered twice") {
		t.Fatalf("panic should name the collision, got %q", msg)
	}
}

func TestDecorateTwicePanics(t *testing.T) {
	ResetForTest(enterprise())
	Decorate("search_schema", sampleDecorator())
	msg := mustPanic(t, "second Decorate", func() { Decorate("search_schema", sampleDecorator()) })
	if !strings.Contains(msg, "registered twice") {
		t.Fatalf("panic should name the collision, got %q", msg)
	}
}

func TestDecorateRequiresAfter(t *testing.T) {
	ResetForTest(enterprise())
	patchOnly := Decorator{
		Capability: "context_probe",
		MinEdition: licverify.EditionEnterprise,
		PatchInput: func(in json.RawMessage) json.RawMessage { return in },
	}
	// A schema patch with no After advertises an input property nothing reads:
	// a client that sends it gets silence. Invisible at runtime, so it has to
	// fail at assembly.
	msg := mustPanic(t, "Decorate without After", func() { Decorate("search_schema", patchOnly) })
	if !strings.Contains(msg, "no After hook") {
		t.Fatalf("panic should explain the missing hook, got %q", msg)
	}
}

func TestOneCapabilitySpansToolAndDecorator(t *testing.T) {
	ResetForTest(enterprise())
	// The normal shape of a paid capability: an extra tool plus a decorator on
	// an existing one. The registry records the capability once.
	Register(sample("probe"))
	Decorate("search_schema", sampleDecorator())

	got := entitlement.Assembled()
	if len(got) != 1 || got[0].Name != "context_probe" || got[0].MinEdition != licverify.EditionEnterprise {
		t.Fatalf("Assembled() = %+v, want a single enterprise capability", got)
	}
}

func TestRegistrationIsUnconditional(t *testing.T) {
	// An unlicensed process still assembles everything: a certificate installed
	// later has to take effect without a restart, which is impossible if an
	// unlicensed boot registered nothing.
	ResetForTest(entitlement.FixedGate(licverify.EditionCommunity))
	Register(sample("probe"))

	if len(Extras()) != 1 {
		t.Fatal("the tool should be registered regardless of the licence")
	}
	if Extras()[0].Allowed() {
		t.Fatal("…but it must not be allowed to serve")
	}
}

func TestRegisterAfterFreezePanics(t *testing.T) {
	ResetForTest(enterprise())
	entitlement.Freeze()
	msg := mustPanic(t, "Register after Freeze", func() { Register(sample("probe")) })
	if !strings.Contains(msg, "after Freeze") {
		t.Fatalf("panic should say the window is closed, got %q", msg)
	}
}

func TestExtrasAreOrderedByKey(t *testing.T) {
	ResetForTest(enterprise())
	Register(sample("zeta"))
	Register(sample("alpha"))

	got := Extras()
	if len(got) != 2 || got[0].Key != "alpha" || got[1].Key != "zeta" {
		t.Fatalf("Extras() should be key-ordered, got %v, %v", got[0].Key, got[1].Key)
	}
}

func TestPatchIsIdentityWithoutPatchInput(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{}}`)
	var d Decorator
	if got := string(d.Patch(in)); got != string(in) {
		t.Fatalf("Patch without PatchInput = %q, want the schema untouched", got)
	}
}
