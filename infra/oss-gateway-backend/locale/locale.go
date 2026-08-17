// Package locale loads and resolves OSS Gateway translation resources.
package locale

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

var registerOnce sync.Once

// Register loads deployed resources, falling back to the source directory for
// local development and tests.
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

// Translate renders a resource with the effective request locale.
func Translate(ctx context.Context, messageID string, templateData map[string]interface{}) string {
	Register()
	return i18n.Translate(sharedrest.GetLanguageByCtx(ctx), messageID, templateData)
}
