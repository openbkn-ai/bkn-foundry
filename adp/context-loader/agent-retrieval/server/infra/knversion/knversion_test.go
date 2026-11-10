// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knversion

import (
	"net/http"
	"testing"
)

func TestResolveValidates(t *testing.T) {
	for _, tc := range []struct {
		version, revision string
		ok                bool
	}{
		{"", "", true}, {"running", "", true}, {"DRAFT", "", true}, {"7", "", true},
		{"draft", "12", true},
		{"0", "", false}, {"latest", "", false},
		{"running", "3", false}, {"", "3", false}, {"7", "3", false}, {"draft", "x", false},
	} {
		_, err := Resolve(tc.version, tc.revision)
		if (err == nil) != tc.ok {
			t.Errorf("Resolve(%q, %q) err = %v, want ok=%v", tc.version, tc.revision, err, tc.ok)
		}
	}
}

func TestFirstResponsePinsTheVersion(t *testing.T) {
	sel, _ := Resolve("running", "")
	if v, r := sel.Params(); v != "running" || r != "" {
		t.Fatalf("before any response: %q %q", v, r)
	}
	h := http.Header{}
	h.Set(HeaderVersion, "7")
	sel.Observe(200, h, nil)
	// A later response must not move the pin.
	h.Set(HeaderVersion, "8")
	sel.Observe(200, h, nil)
	if v, r := sel.Params(); v != "7" || r != "" {
		t.Fatalf("pinned: %q %q", v, r)
	}
	if sel.Label() != "7" {
		t.Fatalf("label = %q", sel.Label())
	}
}

func TestDraftPinsItsRevision(t *testing.T) {
	sel, _ := Resolve("draft", "")
	h := http.Header{}
	h.Set(HeaderVersion, "draft")
	h.Set(HeaderDraftRevision, "12")
	sel.Observe(200, h, nil)
	if v, r := sel.Params(); v != "draft" || r != "12" {
		t.Fatalf("pinned: %q %q", v, r)
	}
	if sel.Label() != "draft@12" {
		t.Fatalf("label = %q", sel.Label())
	}
}

func TestUnversionedNetworkPinsNothing(t *testing.T) {
	sel, _ := Resolve("", "")
	sel.Observe(200, http.Header{}, nil)
	if v, _ := sel.Params(); v != "" || sel.Label() != "" {
		t.Fatalf("unversioned network was pinned: %q / %q", v, sel.Label())
	}
}

func TestMismatchIsRecordedAndRenewClearsIt(t *testing.T) {
	sel, _ := Resolve("draft", "")
	sel.Observe(409, http.Header{}, []byte(`{"error_code":"BknBackend.KNVersion.DraftRevisionMismatch"}`))
	if !sel.RevisionMismatch() {
		t.Fatal("mismatch not recorded")
	}
	if sel.Renew().RevisionMismatch() {
		t.Fatal("a renewed selector must start clean")
	}
	other, _ := Resolve("draft", "")
	other.Observe(409, http.Header{}, []byte(`{"error_code":"something else"}`))
	if other.RevisionMismatch() {
		t.Fatal("an unrelated 409 is not a mismatch")
	}
}

func TestReadsRunning(t *testing.T) {
	var nilSel *Selector
	if !nilSel.ReadsRunning() {
		t.Fatal("no selector reads running")
	}
	for v, want := range map[string]bool{"": true, "running": true, "draft": false, "3": false} {
		sel, _ := Resolve(v, "")
		if sel.ReadsRunning() != want {
			t.Errorf("ReadsRunning(%q) = %v", v, !want)
		}
	}
}
