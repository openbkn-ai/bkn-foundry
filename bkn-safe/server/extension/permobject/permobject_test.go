// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permobject

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension"
)

// fake stands in for the ee implementation. Core must be testable with a fake
// in the socket — that is the point of depending on the interface, not on ee.
type fake struct {
	decision Decision
	err      error
	seen     Request
}

func (f *fake) Decide(_ context.Context, req Request) (Decision, error) {
	f.seen = req
	return f.decision, f.err
}

// licensed installs a gate that turns perm_object_level on or off, and clears
// the socket, so each test starts from a known assembly state.
func licensed(t *testing.T, on *bool) {
	t.Helper()
	reset()
	extension.SetGateForTest(extension.GateFunc(func(f extension.Feature) bool {
		return *on && f == extension.FeaturePermObjectLevel
	}))
}

func TestCommunityBuildAbstains(t *testing.T) {
	on := true
	licensed(t, &on)
	// Nothing registered: this is a community binary, the code is not present.
	d, err := Decide(context.Background(), Request{AccessorID: "u1", CoreVerdict: true})
	if err != nil {
		t.Fatalf("Decide err = %v, want nil", err)
	}
	if d != Abstain {
		t.Fatalf("Decide = %v, want Abstain — core's verdict must stand", d)
	}
	if Available() {
		t.Fatal("Available() must be false with an empty socket")
	}
}

func TestRegisteredDenyOverridesCoreAllow(t *testing.T) {
	on := true
	licensed(t, &on)
	Register(&fake{decision: Deny})

	d, err := Decide(context.Background(), Request{AccessorID: "u1", CoreVerdict: true})
	if err != nil {
		t.Fatalf("Decide err = %v", err)
	}
	if got := Apply(true, d); got {
		t.Fatal("an ee deny must override core's allow — that is the gap core cannot express")
	}
}

func TestRegisteredAllowOverridesCoreDeny(t *testing.T) {
	on := true
	licensed(t, &on)
	Register(&fake{decision: Allow})

	d, _ := Decide(context.Background(), Request{AccessorID: "u1", CoreVerdict: false})
	if got := Apply(false, d); !got {
		t.Fatal("an ee allow must override core's deny")
	}
}

func TestAbstainLeavesCoreVerdictAlone(t *testing.T) {
	on := true
	licensed(t, &on)
	Register(&fake{decision: Abstain})

	for _, core := range []bool{true, false} {
		d, _ := Decide(context.Background(), Request{CoreVerdict: core})
		if got := Apply(core, d); got != core {
			t.Fatalf("Apply(%v, Abstain) = %v, want %v", core, got, core)
		}
	}
}

func TestErrorFailsClosed(t *testing.T) {
	on := true
	licensed(t, &on)
	wantErr := errors.New("ee store unreachable")
	Register(&fake{decision: Allow, err: wantErr})

	d, err := Decide(context.Background(), Request{CoreVerdict: true})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Decide err = %v, want %v surfaced to the caller", err, wantErr)
	}
	if d != Deny {
		t.Fatalf("Decide = %v on error, want Deny — reverting to core's looser verdict would hand out access the enterprise policy revoked", d)
	}
}

func TestLapsedLicenseFallsBackToCommunityBehaviour(t *testing.T) {
	on := true
	licensed(t, &on)
	Register(&fake{decision: Deny})

	if !Available() {
		t.Fatal("capability should be available while licensed")
	}
	// The license expires while the process keeps running. The socket is still
	// filled, but the capability has to go dark without a restart and the
	// cluster has to fall back to community authorization behaviour.
	on = false
	if Available() {
		t.Fatal("capability must go dark when the license lapses")
	}
	d, err := Decide(context.Background(), Request{CoreVerdict: true})
	if err != nil {
		t.Fatalf("Decide err = %v, want nil", err)
	}
	if d != Abstain {
		t.Fatalf("Decide = %v after the license lapsed, want Abstain", d)
	}
}

func TestRequestCarriesCoreVerdictToEE(t *testing.T) {
	on := true
	licensed(t, &on)
	f := &fake{decision: Abstain}
	Register(f)

	req := Request{AccessorID: "u1", ResourceType: "knowledge_network", ResourceID: "kn1", Op: "view_detail", CoreVerdict: true}
	if _, err := Decide(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if f.seen != req {
		t.Fatalf("ee saw %+v, want %+v", f.seen, req)
	}
}

func TestRegisterNilPanics(t *testing.T) {
	on := true
	licensed(t, &on)
	defer func() {
		if recover() == nil {
			t.Fatal("Register(nil) should panic")
		}
	}()
	Register(nil)
}

func TestRegisterWithoutLicensePanics(t *testing.T) {
	on := false
	licensed(t, &on)
	defer func() {
		if recover() == nil {
			t.Fatal("registering without a license should panic — ee must check first")
		}
	}()
	Register(&fake{})
}
