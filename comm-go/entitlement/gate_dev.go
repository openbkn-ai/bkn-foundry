//go:build ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"os"
	"strings"

	"github.com/openbkn-ai/licverify"
)

// DefaultGate is the development stub: OPENBKN_EDITION=enterprise puts the
// process in that tier without a certificate.
//
// It exists only under -tags ee_dev and is absent from a release binary. That
// separation is the point, and it has to be a compile-time one: services run on
// customer hardware where the customer has root and owns the environment, so a
// stub that shipped and merely logged a warning would be a licence bypass with
// a polite note attached.
//
// Each service's release job asserts its artefact's build configuration on the
// artefact itself, because the mistake this guards against lives in build tags
// and Dockerfiles rather than in any .go file. agent-retrieval's release
// workflow has the reference implementation; copy it when a service starts
// linking this package. The assertion belongs on the pipelines that produce
// binaries — this module produces none, so it cannot carry its own.
//
// The wording here tracks reality on purpose, in both directions: a comment
// claiming a defence that is not there lets the next person relax on the
// strength of it, and one denying a defence that is there gets it built twice.
//
// The environment is re-read on every call rather than resolved once, so the
// stub has the same shape as the shipped gate: a licence that changes under a
// running process takes effect without a restart. Same shape means the SetGate
// call site, every AtLeast check and every test written against them survive
// the swap untouched.
func DefaultGate() Gate {
	return GateFunc(func() Snapshot {
		ed := licverify.Edition(strings.TrimSpace(os.Getenv("OPENBKN_EDITION")))
		if ed == "" || ed == licverify.EditionCommunity {
			return Snapshot{Edition: licverify.EditionCommunity, State: licverify.StateUnlicensed}
		}
		if !ed.Known() {
			// Edition is a plain string conversion, so a typo would sail
			// through as Licensed: true with an edition that satisfies no bar
			// at all — "it says licensed and nothing works", with nothing
			// pointing at the misspelling. The whole value of this stub is
			// saving a developer that afternoon.
			panic("entitlement: OPENBKN_EDITION=" + string(ed) + " is not a known edition (community, professional, enterprise, industry)")
		}
		return Snapshot{
			Licensed: true,
			Edition:  ed,
			State:    licverify.StateValid,
			Features: splitList(os.Getenv("OPENBKN_FEATURES")),
		}
	})
}

// GateWithRunner mirrors the release signature so callers do not need build
// tags of their own. The stub has nothing to run in the background.
func GateWithRunner() (Gate, func(stop <-chan struct{})) { return DefaultGate(), nil }

// splitList parses the optional features list. It feeds display surfaces only —
// nothing gates on features (see Features).
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
