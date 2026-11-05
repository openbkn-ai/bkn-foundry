// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

type fullBuilds struct{ BuildRuntime }

func installForWorker(t *testing.T, gate entitlement.GateFunc) {
	t.Helper()
	ResetForTest()
	entitlement.SetGateForTest(gate)
	t.Cleanup(func() { ResetForTest(); entitlement.ResetForTest() })
	Register(Providers{Source: UnavailableSource{}, Concepts: UnavailableConcepts{}, Builds: fullBuilds{}})
}

func TestWorkerAccessOnlyCleansOnADefiniteAnswer(t *testing.T) {
	cases := []struct {
		name string
		snap entitlement.Snapshot
		want WorkerAccess
	}{
		{"licensed", entitlement.Snapshot{Licensed: true, Edition: licverify.EditionProfessional, State: licverify.StateValid}, WorkerRun},
		{"in grace", entitlement.Snapshot{Licensed: true, Edition: licverify.EditionEnterprise, State: licverify.StateGrace}, WorkerRun},
		{"hub silent", entitlement.Snapshot{Edition: licverify.EditionCommunity, Undetermined: true}, WorkerPause},
		{"no certificate", entitlement.Snapshot{Edition: licverify.EditionCommunity}, WorkerClean},
		{"past grace", entitlement.Snapshot{Edition: licverify.EditionCommunity, State: licverify.StateFallback}, WorkerClean},
		{"rejected certificate", entitlement.Snapshot{Edition: licverify.EditionCommunity, State: licverify.StateInvalid}, WorkerClean},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installForWorker(t, func() entitlement.Snapshot { return tc.snap })
			providers, access := ForWorker()
			if access != tc.want {
				t.Fatalf("access = %v, want %v", access, tc.want)
			}
			if providers.Builds == nil {
				t.Fatal("cleanup and resume both need the installed runtime")
			}
		})
	}
}

func TestWorkerAccessReadsOneSnapshotPerDecision(t *testing.T) {
	// A hub answering between two reads must not turn "not licensed yet" plus
	// "answered" into a revocation of a licensed deployment's work.
	reads := 0
	installForWorker(t, func() entitlement.Snapshot {
		reads++
		if reads == 1 {
			return entitlement.Snapshot{Edition: licverify.EditionCommunity, Undetermined: true}
		}
		return entitlement.Snapshot{Licensed: true, Edition: licverify.EditionProfessional}
	})
	reads = 0
	if _, access := ForWorker(); access != WorkerPause || reads != 1 {
		t.Fatalf("access = %v after %d reads", access, reads)
	}
}

func TestWorkerAccessWithoutProvidersDoesNothing(t *testing.T) {
	ResetForTest()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(func() { ResetForTest(); entitlement.ResetForTest() })
	if _, access := ForWorker(); access != WorkerNotInstalled {
		t.Fatalf("access = %v", access)
	}
}

func TestMissingProvidersNamesWhatTheAssemblyLeftOut(t *testing.T) {
	ResetForTest()
	if MissingProviders() != nil {
		t.Fatal("a build without the extension has nothing to report")
	}
	installForWorker(t, func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionCommunity} })
	missing := MissingProviders()
	want := map[string]bool{"datasets": true, "publication": true, "refresh sources": true, "refresh tasks": true}
	if len(missing) != len(want) {
		t.Fatalf("missing = %v", missing)
	}
	for _, name := range missing {
		if !want[name] {
			t.Fatalf("unexpected %q in %v", name, missing)
		}
	}
}
