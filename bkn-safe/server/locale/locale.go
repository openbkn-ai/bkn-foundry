// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale loads BKN Safe's translation resources.
package locale

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// Register loads resources bundled next to the deployed binary, with the source
// directory retained as a fallback for local development and tests.
func Register() {
	if executable, err := os.Executable(); err == nil {
		deployedDir := filepath.Join(filepath.Dir(executable), "locale")
		if entries, readErr := os.ReadDir(deployedDir); readErr == nil && len(entries) > 0 {
			i18n.RegisterI18n(deployedDir)
			return
		}
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	i18n.RegisterI18n(filepath.Dir(filename))
}
