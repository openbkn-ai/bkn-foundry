// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package locale

import (
	"context"
	"log"
	"os"
	"path"
	"runtime"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

var (
	localeDir = "/locale"
)

const validationDetailPrefix = "OntologyQuery.Validation.Detail."

// ValidationDetail renders a request-validation detail using the effective request locale.
func ValidationDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), validationDetailPrefix+name, templateData)
}

func Register() {
	var abPath string

	// UT MODE
	if os.Getenv("I18N_MODE_UT") == "true" {
		_, filename, _, ok := runtime.Caller(0)
		if ok {
			abPath = path.Dir(filename)
		} else {
			log.Fatal("failed to get absolute path")
		}
	} else {
		abPath, _ = os.Getwd()
		abPath += localeDir
		if _, err := os.Stat(abPath); err != nil {
			_, filename, _, ok := runtime.Caller(0)
			if ok {
				abPath = path.Dir(filename)
			} else {
				log.Fatal("failed to get absolute path")
			}
		}
	}
	i18n.RegisterI18n(abPath)
}
