// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "testing"

func TestExplicitVersionEqualToRunningVersionUsesTheProxy(t *testing.T) {
	pin := mustPin(t, "3", "")
	pin.ObserveRunning("3")
	_ = pin.Observe("3", "")
	if pin.UsesCallerIdentity() || pin.ProxyBranch() != "v3" {
		t.Fatalf("version 3 is running: caller=%v branch=%q", pin.UsesCallerIdentity(), pin.ProxyBranch())
	}

	history := mustPin(t, "2", "")
	history.ObserveRunning("3")
	_ = history.Observe("2", "")
	if !history.UsesCallerIdentity() {
		t.Fatal("a history version must read as the caller")
	}

	draft := mustPin(t, "draft", "")
	draft.ObserveRunning("3")
	_ = draft.Observe("draft", "7")
	if !draft.UsesCallerIdentity() {
		t.Fatal("the draft must read as the caller")
	}

	// Without the header the explicit number stays on the safe side.
	unknown := mustPin(t, "3", "")
	_ = unknown.Observe("3", "")
	if !unknown.UsesCallerIdentity() {
		t.Fatal("no running version reported: caller identity")
	}
}
