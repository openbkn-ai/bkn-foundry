// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"errors"
	"testing"
)

func mustPin(t *testing.T, knVersion, draftRevision string) *KNVersionPin {
	t.Helper()
	pin, err := NewKNVersionPin(knVersion, draftRevision)
	if err != nil {
		t.Fatalf("NewKNVersionPin(%q, %q) error = %v", knVersion, draftRevision, err)
	}
	return pin
}

func TestNewKNVersionPinValidatesRequest(t *testing.T) {
	for _, test := range []struct {
		knVersion, draftRevision string
		valid                    bool
	}{
		{"", "", true},
		{"running", "", true},
		{"draft", "", true},
		{"draft", "7", true},
		{"3", "", true},
		{"0", "", false},
		{"-1", "", false},
		{"v3", "", false},
		{"latest", "", false},
		{"3", "7", false},
		{"", "7", false},
		{"running", "7", false},
		{"draft", "x", false},
		{"draft", "-1", false},
	} {
		_, err := NewKNVersionPin(test.knVersion, test.draftRevision)
		if (err == nil) != test.valid {
			t.Errorf("NewKNVersionPin(%q, %q) error = %v, want valid=%v", test.knVersion, test.draftRevision, err, test.valid)
		}
	}
}

func TestPinSendsNothingForUnversionedNetwork(t *testing.T) {
	pin := mustPin(t, "", "")
	if got := pin.QueryString(); got != "" {
		t.Fatalf("QueryString() = %q, want empty", got)
	}
	// An unversioned network never answers with X-Kn-Version, so the pin stays open.
	if err := pin.Observe("", ""); err != nil {
		t.Fatal(err)
	}
	if got := pin.QueryString(); got != "" {
		t.Fatalf("QueryString() after an answer without version = %q, want empty", got)
	}
	if pin.UsesCallerIdentity() || pin.ProxyBranch() != "" || pin.Token() != "" {
		t.Fatalf("unversioned pin must read through the proxy on main: caller=%v branch=%q token=%q",
			pin.UsesCallerIdentity(), pin.ProxyBranch(), pin.Token())
	}
}

func TestPinFixesRunningVersionAtFirstRead(t *testing.T) {
	pin := mustPin(t, "", "")
	if err := pin.Observe("3", ""); err != nil {
		t.Fatal(err)
	}
	// Every later call asks for the version the first one was served from, even if a newer
	// version was published meanwhile.
	if got := pin.QueryString(); got != "&kn_version=3" {
		t.Fatalf("QueryString() = %q", got)
	}
	if err := pin.Observe("3", ""); err != nil {
		t.Fatalf("same version again: %v", err)
	}
	if pin.UsesCallerIdentity() || pin.ProxyBranch() != "v3" || pin.Token() != "v3" {
		t.Fatalf("running pin: caller=%v branch=%q token=%q", pin.UsesCallerIdentity(), pin.ProxyBranch(), pin.Token())
	}
}

func TestPinRejectsASecondVersion(t *testing.T) {
	pin := mustPin(t, "", "")
	_ = pin.Observe("3", "")
	err := pin.Observe("4", "")
	var changed *KNVersionChangedError
	if !errors.As(err, &changed) || changed.Pinned != "v3" || changed.Read != "v4" {
		t.Fatalf("Observe(4) error = %v, want KNVersionChangedError{v3, v4}", err)
	}
}

func TestPinOnDraftCarriesRevisionAndReadsAsCaller(t *testing.T) {
	pin := mustPin(t, "draft", "")
	if got := pin.QueryString(); got != "&kn_version=draft" {
		t.Fatalf("QueryString() before the first read = %q", got)
	}
	if err := pin.Observe("draft", "7"); err != nil {
		t.Fatal(err)
	}
	if got := pin.QueryString(); got != "&kn_version=draft&draft_revision=7" {
		t.Fatalf("QueryString() = %q", got)
	}
	if !pin.UsesCallerIdentity() || pin.ProxyBranch() != "" || pin.Token() != "draft@7" {
		t.Fatalf("draft pin: caller=%v branch=%q token=%q", pin.UsesCallerIdentity(), pin.ProxyBranch(), pin.Token())
	}
	if err := pin.Observe("draft", "8"); err == nil {
		t.Fatal("a draft read with another revision must be refused")
	}
}

func TestPinWithRequestedDraftRevisionSendsItFromTheFirstCall(t *testing.T) {
	pin := mustPin(t, "draft", "5")
	if got := pin.QueryString(); got != "&kn_version=draft&draft_revision=5" {
		t.Fatalf("QueryString() = %q", got)
	}
}

func TestPinOnHistoryVersionReadsAsCaller(t *testing.T) {
	pin := mustPin(t, "2", "")
	if got := pin.QueryString(); got != "&kn_version=2" {
		t.Fatalf("QueryString() = %q", got)
	}
	_ = pin.Observe("2", "")
	if !pin.UsesCallerIdentity() {
		t.Fatal("a version asked for by number reads as the caller, not through the proxy")
	}
	if pin.Token() != "v2" {
		t.Fatalf("Token() = %q", pin.Token())
	}
}

func TestNilPinIsHarmless(t *testing.T) {
	var pin *KNVersionPin
	if pin.QueryString() != "" || pin.UsesCallerIdentity() || pin.ProxyBranch() != "" || pin.Token() != "" ||
		pin.Observe("3", "") != nil {
		t.Fatal("a nil pin must behave like no pin")
	}
}
