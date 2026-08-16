// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale loads BKN Safe's translation resources.
package locale

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

var registerOnce sync.Once

// Register loads resources bundled next to the deployed binary, with the source
// directory retained as a fallback for local development and tests.
func Register() {
	registerOnce.Do(register)
}

func register() {
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

// Translate resolves a BKN Safe message after ensuring the bundled catalog is
// registered. Callers pass the effective request language selected by the
// platform language middleware.
func Translate(language, messageID string) string {
	Register()
	return i18n.Translate(language, messageID, nil)
}
