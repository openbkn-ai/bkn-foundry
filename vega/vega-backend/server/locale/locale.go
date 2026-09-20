// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package locale

import (
	"context"
	"os"
	"path"
	"runtime"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

const validationDetailPrefix = "VegaBackend.Validation.Detail."

var (
	localeDir = "/locale"
)

func Register() {
	var abPath string

	// Prefer the package directory so tests and arbitrary working directories can locate locale files.
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		abPath = path.Dir(filename)
		if _, err := os.Stat(abPath); err == nil {
			i18n.RegisterI18n(abPath)
			return
		}
	}
	// Fall back to cwd + /locale for backward compatibility.
	abPath, _ = os.Getwd()
	abPath += localeDir
	i18n.RegisterI18n(abPath)
}

// ValidationDetail returns a localized validation detail for the request language.
func ValidationDetail(ctx context.Context, name string, data map[string]interface{}) string {
	messageID := validationDetailPrefix + name
	return i18n.Translate(string(rest.GetLanguageByCtx(ctx)), messageID, data)
}
