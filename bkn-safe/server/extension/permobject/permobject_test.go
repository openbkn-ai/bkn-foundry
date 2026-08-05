// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permobject

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
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

// licensed installs a gate that swings between enterprise and community, and
// clears the socket, so each test starts from a known assembly state. The
// pointer lets a test flip the tier mid-run, which is how the hot-activation
// and lapse cases are written.
func licensed(t *testing.T, on *bool) {
	t.Helper()
	reset()
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		if *on {
			return entitlement.Snapshot{Licensed: true, Edition: licverify.EditionEnterprise}
		}
		return entitlement.Snapshot{Edition: licverify.EditionCommunity}
	}))
}

// register plugs a fake in at the tier this capability really costs.
func register(a Authorizer) { Register(licverify.EditionEnterprise, a) }

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
	register(&fake{decision: Deny})

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
	register(&fake{decision: Allow})

	d, _ := Decide(context.Background(), Request{AccessorID: "u1", CoreVerdict: false})
	if got := Apply(false, d); !got {
		t.Fatal("an ee allow must override core's deny")
	}
}

func TestAbstainLeavesCoreVerdictAlone(t *testing.T) {
	on := true
	licensed(t, &on)
	register(&fake{decision: Abstain})

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
	register(&fake{decision: Allow, err: wantErr})

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
	register(&fake{decision: Deny})

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
	register(f)

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
	Register(licverify.EditionEnterprise, nil)
}

// Two Authorizers is an assembly bug that has to be loud. atomic.Value would
// let the second one through whenever the concrete types match, silently
// discarding the first — and this is the layer that produces Deny over casbin,
// so a silently swapped implementation is an authorization change nobody sees.
//
// The guard used to live in extension.Claim, which this migration deleted;
// entitlement.MarkAssembled is idempotent by name and does not replace it. It
// is written against two DIFFERENT concrete types on purpose: with the same
// type, atomic.Value's own "store of inconsistently typed value" panic would
// mask a missing guard and this test would pass for the wrong reason.
func TestSecondRegistrationPanics(t *testing.T) {
	on := true
	licensed(t, &on)
	register(&fake{decision: Allow})

	defer func() {
		if recover() == nil {
			t.Fatal("第二次 Register 必须 panic——否则第一个实现被静默丢弃，启动日志里没有任何痕迹")
		}
	}()
	register(&otherFake{})
}

// otherFake is a second Authorizer implementation with a distinct concrete
// type. See TestSecondRegistrationPanics for why the type has to differ.
type otherFake struct{}

func (*otherFake) Decide(context.Context, Request) (Decision, error) { return Abstain, nil }

// A capability registered without a tier would be a paid capability registered
// as free. The socket delegates the check to MarkAssembled; this pins that it
// is actually reached, so dropping the delegation cannot pass silently.
func TestRegisterWithoutATierPanics(t *testing.T) {
	on := true
	licensed(t, &on)
	defer func() {
		if recover() == nil {
			t.Fatal("零值 MinEdition 必须 panic——否则付费能力会被登记成免费的")
		}
	}()
	Register("", &fake{})
}

// Registering must also put the capability in the process-wide assembly table,
// which is what the capabilities endpoint reports as installed. Without it an
// enterprise binary is indistinguishable from a community one there.
func TestRegisteringPutsTheCapabilityInTheAssemblyTable(t *testing.T) {
	on := false
	licensed(t, &on)
	register(&fake{})

	for _, cap := range entitlement.Assembled() {
		if cap.Name != Capability {
			continue
		}
		if cap.MinEdition != licverify.EditionEnterprise {
			t.Fatalf("MinEdition = %q, want enterprise", cap.MinEdition)
		}
		return
	}
	t.Fatalf("%q 不在装配表里：%v", Capability, entitlement.Assembled())
}

// A tier above the declared minimum inherits the capability. An == comparison
// would cost an industry customer an enterprise capability for paying more
// (ee-design.md §3.1/§3.3).
func TestHigherTiersInheritTheCapability(t *testing.T) {
	for _, ed := range []licverify.Edition{licverify.EditionEnterprise, licverify.EditionIndustry} {
		t.Run(string(ed), func(t *testing.T) {
			reset()
			entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
				return entitlement.Snapshot{Licensed: true, Edition: ed}
			}))
			register(&fake{decision: Deny})
			if !Available() {
				t.Fatalf("%s 拿不到企业能力——上层档位必须继承下层", ed)
			}
		})
	}
}

// Professional is a paid tier, and still below this capability's minimum. The
// fallback is core's own verdict, not a refusal: this socket sits inside a
// decision, not at a request entry point (ee-design.md §4.4).
func TestPaidButLowerTierFallsBackToCore(t *testing.T) {
	reset()
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Licensed: true, Edition: licverify.EditionProfessional}
	}))
	register(&fake{decision: Deny})

	if Available() {
		t.Fatal("专业档不该拿到企业能力")
	}
	d, err := Decide(context.Background(), Request{CoreVerdict: true})
	if err != nil {
		t.Fatalf("Decide err = %v, want nil", err)
	}
	if d != Abstain {
		t.Fatalf("Decide = %v，want Abstain——档位不够要回落 core 判定", d)
	}
}

// Registering while unlicensed has to work, or a certificate installed after
// boot could never switch the capability on without a restart.
func TestRegisterWithoutLicenseIsAllowedAndStaysInactive(t *testing.T) {
	on := false
	licensed(t, &on)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Register must not refuse while unlicensed: %v", r)
		}
	}()
	register(&fake{})

	// Registered, but the licence still decides — Available() is that judgement.
	// Flipping the licence must take effect
	// with no further registration — that is the property the panic prevented.
	if Available() {
		t.Fatal("an unlicensed registration must not be active")
	}
	on = true
	if !Available() {
		t.Fatal("installing the licence must activate the already-registered implementation")
	}
}
