// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale loads BKN Safe's translation resources.
package locale

import (
	"path/filepath"
	"runtime"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// Register loads the catalog from the package directory so tests and deployed
// binaries use the same resources independent of the process working directory.
func Register() {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	i18n.RegisterI18n(filepath.Dir(filename))
}
