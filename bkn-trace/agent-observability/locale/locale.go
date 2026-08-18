// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale provides request-scoped localization for Agent Observability.
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

const messagePrefix = "AgentObservability.Message."

var registerOnce sync.Once

type negotiatedLocaleKey struct{}

// Register loads resources next to the deployed binary, retaining the source
// directory as the local-development fallback. Resource failures are handled by
// comm-go's non-fatal runtime fallback.
func Register() {
	registerOnce.Do(register)
}

func register() {
	if executable, err := os.Executable(); err == nil {
		deployedDir := filepath.Join(filepath.Dir(executable), "locale")
		if entries, readErr := os.ReadDir(deployedDir); readErr == nil && len(entries) > 0 {
			i18n.RegisterI18n(deployedDir, sharedrest.SimplifiedChinese)
			return
		}
	}

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	i18n.RegisterI18n(filepath.Dir(filename), sharedrest.SimplifiedChinese)
}

// Translate localizes an existing English response message without changing
// its surrounding machine-readable error contract.
func Translate(ctx context.Context, message string) string {
	Register()
	return i18n.Translate(sharedrest.GetLanguageByCtx(ctx), messagePrefix+message, nil)
}

// WithLanguage attaches an explicitly negotiated locale. The marker lets
// adapter-level unit tests that call handlers directly retain their legacy
// English fixtures unless they opt into the HTTP language contract.
func WithLanguage(ctx context.Context, language sharedrest.Language) context.Context {
	ctx = sharedrest.WithLanguage(ctx, language)
	return context.WithValue(ctx, negotiatedLocaleKey{}, true)
}

// IsNegotiated reports whether the request passed through language negotiation.
func IsNegotiated(ctx context.Context) bool {
	negotiated, _ := ctx.Value(negotiatedLocaleKey{}).(bool)
	return negotiated
}
