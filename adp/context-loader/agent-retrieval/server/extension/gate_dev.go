//go:build ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package extension

import (
	"os"
	"strings"
)

// DefaultGate is the development stub: OPENBKN_FEATURES="context_probe,audit"
// turns the listed features on.
//
// It exists only under -tags ee_dev and is absent from a release binary, which
// is the whole point of splitting the two. Modules are installed on customer
// premises, where root and the environment belong to the customer; a stub that
// shipped and merely logged a warning would be a licence bypass, because nobody
// reads a log they did not ask for. CI asserts that a release artefact contains
// no OPENBKN_FEATURES string.
//
// The environment is re-read on every call rather than cached into a bool, so
// that this stub has the same semantic shape as the shipped gate: a licence
// that is hot-reloaded or expires must take effect globally without a restart.
// Same shape means the SetGate call site, every Enabled check and every test
// written against them survive the swap untouched.
func DefaultGate() Gate {
	return GateFunc(func(f Feature) bool {
		for _, item := range strings.Split(os.Getenv("OPENBKN_FEATURES"), ",") {
			if strings.TrimSpace(item) == string(f) {
				return true
			}
		}
		return false
	})
}
